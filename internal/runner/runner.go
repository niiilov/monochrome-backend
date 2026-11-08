package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mono/internal/cluster"
	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/extract"
	"mono/internal/guard"
	"mono/internal/ingest"
	"mono/internal/ingest/live"
	"mono/internal/journal"
	"mono/internal/llm"
	"mono/internal/normalize"
	"mono/internal/prompts"
	"mono/internal/rank"
	"mono/internal/render"
	"mono/internal/summarize"
	"mono/internal/timeutil"
	"mono/internal/topic"
	"mono/internal/verify"
)

// errNoClient is returned when the pipeline has nothing to call.
// Journal rows and run_report.json are still written when OutDir is set.
var errNoClient = errors.New("не задан клиент LLM: укажите LLM_API_KEY, LLM_BASE_URL и LLM_MODEL или добавьте --mock")

// Deps are the collaborators of one runner.
// Meter must be the meter that already wraps Client. When Meter is nil and
// Client is set, Run adds its own meter so the report can count calls.
type Deps struct {
	Config config.Config
	// ConfigRaw is the configuration file as read from disk.
	// The API key is not in that file. An empty slice is hashed from the
	// struct with secrets cleared.
	ConfigRaw []byte
	Client    llm.Client
	Meter     *llm.Meter
	Version   string
	Now       func() time.Time
}

type pipeline struct {
	deps Deps
}

// New builds a runner. The interface lives in this package since S0.
func New(deps Deps) Runner {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if strings.TrimSpace(deps.Version) == "" {
		deps.Version = "dev"
	}
	if deps.Config.LLM.MaxConcurrency <= 0 {
		deps.Config.LLM.MaxConcurrency = config.Default().LLM.MaxConcurrency
	}
	return &pipeline{deps: deps}
}

type work struct {
	deps     Deps
	client   llm.Client
	meter    *llm.Meter
	params   domain.RunParams
	progress ProgressFunc
	mu       sync.Mutex

	started time.Time
	period  domain.Period

	articles []domain.Article
	inPeriod []domain.Article
	context  []domain.Article
	journal  *journal.Journal
	mentions []domain.Mention
	events   []domain.Event
	model    domain.DigestModel

	digestMD string
	post     string
	email    string
	html     string
	verify   domain.VerifyReport

	datasetHash string
	durations   map[string]int64
}

// Run executes the digest pipeline and writes the output files.
// A failure of one article is stored on its journal row and does not abort the run.
// A missing model client is an error, but the journal and run_report.json are still saved.
func (p *pipeline) Run(ctx context.Context, params domain.RunParams, progress ProgressFunc) (*domain.RunState, domain.RunReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	w := newWork(p.deps, params, progress)
	err := w.execute(ctx)
	w.sealUndecided(err)
	if verr := w.journal.Validate(w.journalIDs()); verr != nil && err == nil {
		err = verr
	}
	state := w.snapshot()
	report := w.report(state)
	if paramsReady(params) {
		if werr := writeRun(params.OutDir, w.journal.Entries(), &report, w.texts()); werr != nil && err == nil {
			err = werr
		}
	}
	return state, report, err
}

func newWork(deps Deps, params domain.RunParams, progress ProgressFunc) *work {
	client := deps.Client
	meter := deps.Meter
	if client != nil && meter == nil {
		meter = llm.NewMeter(deps.Config.LLM)
		client = llm.WithMeter(client, meter)
	}
	if client != nil {
		client = llm.WithLimiter(client, deps.Config.LLM.MaxConcurrency)
	}
	now := deps.Now()
	return &work{
		deps:      deps,
		client:    client,
		meter:     meter,
		params:    params,
		progress:  progress,
		started:   now,
		period:    timeutil.NewPeriod(params.ControlTime),
		journal:   journal.New(),
		durations: map[string]int64{},
		verify:    domain.VerifyReport{Findings: []domain.VerifyFinding{}},
	}
}

func paramsReady(p domain.RunParams) bool {
	return strings.TrimSpace(p.OutDir) != "" && !p.ControlTime.IsZero() && strings.TrimSpace(p.DatasetPath) != ""
}

func (w *work) execute(ctx context.Context) error {
	if err := w.checkParams(); err != nil {
		return err
	}
	if err := w.load(ctx); err != nil {
		return err
	}
	w.normalize()
	w.applyPeriod()
	w.applyGuard()
	if w.client == nil {
		return errNoClient
	}
	ctx = extract.WithNoter(ctx, w.journal)
	passed, err := w.classify(ctx)
	if err != nil {
		return err
	}
	extractErrs, err := w.extract(ctx, passed)
	if err != nil {
		return err
	}
	if err := w.cluster(ctx, passed, extractErrs); err != nil {
		return err
	}
	if err := w.rank(ctx); err != nil {
		return err
	}
	if err := w.compose(ctx); err != nil {
		return err
	}
	return w.gate(ctx)
}

func (w *work) checkParams() error {
	if strings.TrimSpace(w.params.DatasetPath) == "" {
		return errors.New("не указан путь к набору")
	}
	if w.params.ControlTime.IsZero() {
		return errors.New("не задано контрольное время")
	}
	if strings.TrimSpace(w.params.OutDir) == "" {
		return errors.New("не указан каталог результата")
	}
	return nil
}

func (w *work) load(ctx context.Context) error {
	start := w.begin("load", 1)
	defer w.finish("load", start, 1)
	paths := datasetPaths(w.params)
	articles, rows, err := ingest.LoadDataset(ctx, paths, w.deps.Config.Ingest)
	if err != nil {
		return fmt.Errorf("загрузка набора: %w", err)
	}
	// Live collection is off unless the run asked for it. The dataset path stays the same.
	if w.deps.Config.Live.Enabled {
		articles, rows = live.Merge(articles, rows, live.Collect(ctx, w.deps.Config.Live))
	}
	w.articles = articles
	w.seedJournal(rows)
	sum, hashErr := hashDataset(paths)
	if hashErr == nil {
		w.datasetHash = sum
	}
	return nil
}

func datasetPaths(p domain.RunParams) []string {
	paths := []string{p.DatasetPath}
	for _, extra := range p.ExtraPaths {
		extra = strings.TrimSpace(extra)
		if extra != "" {
			paths = append(paths, extra)
		}
	}
	return paths
}

// seedJournal keeps one row per ID, in dataset order.
// A repeated ID stays a note on the first row: the journal keeps a single line per ID.
func (w *work) seedJournal(problems []domain.JournalEntry) {
	byArticle := map[string]struct{}{}
	for _, article := range w.articles {
		if article.ID != "" {
			byArticle[article.ID] = struct{}{}
		}
	}
	problemByID := map[string]domain.JournalEntry{}
	var extras []domain.JournalEntry
	var duplicates []domain.JournalEntry
	for _, row := range problems {
		if row.ID == "" {
			continue
		}
		if row.ReasonCode == domain.ReasonDuplicateID {
			duplicates = append(duplicates, row)
			continue
		}
		if _, known := byArticle[row.ID]; known {
			problemByID[row.ID] = row
			continue
		}
		if _, seen := problemByID[row.ID]; seen {
			continue
		}
		problemByID[row.ID] = row
		extras = append(extras, row)
	}
	for _, article := range w.articles {
		if article.ID == "" {
			continue
		}
		if row, ok := problemByID[article.ID]; ok && strings.TrimSpace(article.ReadError) != "" {
			w.journal.Add(row)
			continue
		}
		w.journal.Add(domain.JournalEntry{
			ID:         article.ID,
			Source:     article.Source,
			SourceKind: article.SourceKind,
		})
		if strings.TrimSpace(article.ReadError) != "" {
			w.journal.Set(article.ID, domain.StatusExcluded, "", domain.ReasonReadError,
				journal.Text(domain.ReasonReadError, journal.Data{}), article.ReadError)
		}
	}
	for _, row := range extras {
		w.journal.Add(row)
	}
	for _, row := range duplicates {
		w.journal.Note(row.ID, "ingest", row.Reason)
	}
}

func (w *work) normalize() {
	start := w.begin("normalize", len(w.articles))
	defer w.finish("normalize", start, len(w.articles))
	normalize.ApplyAll(w.articles)
	for _, article := range w.articles {
		w.putMeta(article)
	}
}

func (w *work) applyPeriod() {
	var pending []domain.Article
	for _, article := range w.articles {
		if strings.TrimSpace(article.ReadError) != "" {
			continue
		}
		if _, ok := w.journal.Get(article.ID); !ok {
			continue
		}
		row, _ := w.journal.Get(article.ID)
		if row.Status != "" {
			continue
		}
		pending = append(pending, article)
	}
	start := w.begin("period", len(pending))
	defer w.finish("period", start, len(pending))
	in, out, unconfirmed, notes := timeutil.FilterPeriod(pending, w.period, w.deps.Config.Period)
	w.context = out
	w.inPeriod = in
	w.writeBackTime(in)
	w.writeBackTime(out)
	w.writeBackTime(unconfirmed)
	for id, note := range notes {
		w.journal.Note(id, "period", note)
	}
	for _, article := range out {
		w.exclude(article.ID, domain.ReasonOutOfPeriod, journal.Data{Period: w.period}, "")
		w.putMeta(article)
	}
	for _, article := range unconfirmed {
		w.exclude(article.ID, domain.ReasonDateUnconfirmed, journal.Data{}, "")
		w.putMeta(article)
	}
	for _, article := range in {
		w.putMeta(w.articleByID(article.ID))
	}
}

func (w *work) applyGuard() {
	start := w.begin("guard", len(w.articles))
	defer w.finish("guard", start, len(w.articles))
	flags := map[string][]string{}
	for i := range w.articles {
		got := scanFlags(w.articles[i])
		w.articles[i].Flags = got
		flags[w.articles[i].ID] = got
		w.putMeta(w.articles[i])
	}
	w.inPeriod = stampFlags(w.inPeriod, flags)
	w.context = stampFlags(w.context, flags)
}

func scanFlags(article domain.Article) []string {
	findings := guard.Scan(article.Title + "\n" + article.Text)
	if len(findings) == 0 {
		return nil
	}
	var flags []string
	seen := map[string]bool{}
	add := func(flag string) {
		flag = strings.TrimSpace(flag)
		if flag == "" || seen[flag] {
			return
		}
		seen[flag] = true
		flags = append(flags, flag)
	}
	add("injection_suspected")
	for _, finding := range findings {
		add(finding.Kind)
	}
	return flags
}

func stampFlags(list []domain.Article, flags map[string][]string) []domain.Article {
	out := make([]domain.Article, len(list))
	for i, article := range list {
		out[i] = article
		if got := flags[article.ID]; len(got) > 0 {
			out[i].Flags = append([]string(nil), got...)
		}
	}
	return out
}

func (w *work) classify(ctx context.Context) ([]domain.Article, error) {
	var list []domain.Article
	for _, article := range w.inPeriod {
		if missingText(article) {
			w.exclude(article.ID, domain.ReasonInsufficient, journal.Data{}, "")
			continue
		}
		list = append(list, article)
	}
	start := w.begin("topic", len(list))
	defer w.finish("topic", start, len(list))
	if len(list) == 0 {
		return nil, nil
	}
	type slot struct {
		res domain.TopicResult
		err error
	}
	slots := make([]slot, len(list))
	var done atomic.Int32
	err := runParallel(ctx, len(list), w.deps.Config.LLM.MaxConcurrency, func(ctx context.Context, i int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, callErr := topic.Classify(ctx, w.client, list[i])
		slots[i] = slot{res: res, err: callErr}
		n := int(done.Add(1))
		w.emit("topic", n, len(list))
		return nil
	})
	if err != nil {
		return nil, err
	}
	var passed []domain.Article
	for i, article := range list {
		if slots[i].err != nil {
			w.journal.Set(article.ID, domain.StatusExcluded, "", "",
				"Не удалось определить тему материала", slots[i].err.Error())
			continue
		}
		w.putTopic(article.ID, slots[i].res)
		decision := topic.Decide(slots[i].res)
		if !decision.Pass {
			w.journal.Set(article.ID, domain.StatusExcluded, "", decision.Code, decision.Reason, "")
			continue
		}
		passed = append(passed, article)
	}
	return passed, nil
}

func missingText(article domain.Article) bool {
	if strings.TrimSpace(article.Text) == "" {
		return true
	}
	for _, field := range article.MissingFields {
		if field == "text" {
			return true
		}
	}
	return false
}

func (w *work) extract(ctx context.Context, passed []domain.Article) (map[string]string, error) {
	start := w.begin("extract", len(passed))
	defer w.finish("extract", start, len(passed))
	failures := map[string]string{}
	if len(passed) == 0 {
		return failures, nil
	}
	type slot struct {
		mentions []domain.Mention
		err      error
	}
	slots := make([]slot, len(passed))
	var done atomic.Int32
	err := runParallel(ctx, len(passed), w.deps.Config.LLM.MaxConcurrency, func(ctx context.Context, i int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mentions, callErr := extract.Mentions(ctx, w.client, passed[i])
		slots[i] = slot{mentions: mentions, err: callErr}
		n := int(done.Add(1))
		w.emit("extract", n, len(passed))
		return nil
	})
	if err != nil {
		return failures, err
	}
	for i, article := range passed {
		if slots[i].err != nil {
			failures[article.ID] = slots[i].err.Error()
			w.journal.Note(article.ID, "extract", slots[i].err.Error())
			continue
		}
		w.mentions = append(w.mentions, slots[i].mentions...)
	}
	return failures, nil
}

func (w *work) cluster(ctx context.Context, passed []domain.Article, extractErrs map[string]string) error {
	start := w.begin("cluster", len(w.mentions))
	defer w.finish("cluster", start, len(w.mentions))
	if err := ctx.Err(); err != nil {
		return err
	}
	arts := w.articleMap()
	res, err := cluster.BuildResult(ctx, w.client, w.mentions, arts, w.context, w.deps.Config.Cluster)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("кластеризация: %w", err)
	}
	if res.Errors == nil && len(extractErrs) > 0 {
		res.Errors = map[string]string{}
	}
	for id, msg := range extractErrs {
		if strings.TrimSpace(res.Errors[id]) == "" {
			res.Errors[id] = msg
		}
	}
	cluster.ApplyJournal(w.journal, res, w.period, passed, w.context)
	w.events = res.Events
	return nil
}

func (w *work) rank(ctx context.Context) error {
	arts := w.articleMap()
	start := w.begin("rank", len(w.events))
	res, err := rank.ScoreResult(ctx, w.client, w.events, arts, w.deps.Config.Rank)
	w.finish("rank", start, len(w.events))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("оценка событий: %w", err)
	}
	selStart := w.begin("select", len(res.Events))
	selected := rank.Select(res.Events, arts, w.deps.Config.Rank.MaxEvents)
	rank.ApplyJournal(w.journal, selected, res.Errors)
	w.finish("select", selStart, len(res.Events))
	w.events = selected
	return nil
}

func (w *work) compose(ctx context.Context) error {
	selected := selectedEvents(w.events)
	start := w.begin("summarize", len(selected))
	defer w.finish("summarize", start, len(selected))
	if err := ctx.Err(); err != nil {
		return err
	}
	arts := w.articleMap()
	items, err := summarize.Items(ctx, w.client, w.events, arts, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.noteFailure(selected, "summarize", err)
		items = templateItems(selected, arts)
	}
	w.model = digestModel(w.period, w.params.ControlTime, items)
	return nil
}

func (w *work) gate(ctx context.Context) error {
	start := w.begin("verify", len(w.model.Items))
	defer w.finish("verify", start, len(w.model.Items))
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := verify.Gate(ctx, w.client, w.model, w.events, w.articleMap(), verify.Options{
		MaxRetries: w.deps.Config.Verify.MaxRetries,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.renderLocal()
		return fmt.Errorf("проверка выпуска: %w", err)
	}
	w.model = result.Model
	w.digestMD = result.DigestMD
	w.post = result.Post
	w.email = result.EmailText
	w.html = result.EmailHTML
	w.verify = result.Report
	if w.verify.Findings == nil {
		w.verify.Findings = []domain.VerifyFinding{}
	}
	return nil
}

func (w *work) renderLocal() {
	w.digestMD = render.Digest(w.model)
	w.post = render.Post(w.model, nil)
	w.email = render.EmailText(w.model)
	w.html = render.EmailHTML(w.model)
}

func (w *work) noteFailure(events []domain.Event, stage string, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	for _, event := range events {
		id := strings.TrimSpace(event.PrimaryArticleID)
		if id == "" && len(event.ArticleIDs) > 0 {
			id = event.ArticleIDs[0]
		}
		if id == "" {
			continue
		}
		w.journal.Note(id, stage, msg)
		row, ok := w.journal.Get(id)
		if !ok {
			continue
		}
		if strings.TrimSpace(row.Error) == "" {
			row.Error = msg
			w.journal.Add(row)
		}
	}
}

func (w *work) sealUndecided(runErr error) {
	reason := "Статус материала не определён"
	if runErr != nil {
		reason = "Прогон остановлен: " + oneLine(runErr.Error())
	}
	for _, row := range w.journal.Entries() {
		if row.Status != "" {
			continue
		}
		w.journal.Set(row.ID, domain.StatusExcluded, "", "", reason, oneLine(reason))
	}
}

func (w *work) exclude(id string, code domain.ReasonCode, data journal.Data, errText string) {
	w.journal.Set(id, domain.StatusExcluded, "", code, journal.Text(code, data), errText)
}

func (w *work) putMeta(article domain.Article) {
	if article.ID == "" {
		return
	}
	row, ok := w.journal.Get(article.ID)
	if !ok {
		return
	}
	row.Source = article.Source
	row.SourceKind = article.SourceKind
	row.PublishedAtMSK = mskStamp(article.PublishedAt)
	if article.Flags != nil {
		row.Flags = append([]string(nil), article.Flags...)
	}
	w.journal.Add(row)
}

func (w *work) putTopic(id string, res domain.TopicResult) {
	row, ok := w.journal.Get(id)
	if !ok {
		return
	}
	cp := res
	if res.MatchedTerms != nil {
		cp.MatchedTerms = append([]string(nil), res.MatchedTerms...)
	}
	row.Topic = &cp
	w.journal.Add(row)
}

func (w *work) writeBackTime(list []domain.Article) {
	byID := map[string]*domain.Article{}
	for i := range w.articles {
		byID[w.articles[i].ID] = &w.articles[i]
	}
	for _, article := range list {
		dst := byID[article.ID]
		if dst == nil || article.PublishedAt == nil {
			continue
		}
		when := article.PublishedAt.In(timeutil.MSK)
		dst.PublishedAt = &when
	}
}

func (w *work) articleByID(id string) domain.Article {
	for _, article := range w.articles {
		if article.ID == id {
			return article
		}
	}
	return domain.Article{ID: id}
}

func (w *work) articleMap() map[string]domain.Article {
	out := make(map[string]domain.Article, len(w.articles)+len(w.context))
	for _, article := range w.articles {
		if article.ID != "" {
			out[article.ID] = article
		}
	}
	for _, article := range w.context {
		if article.ID != "" {
			out[article.ID] = article
		}
	}
	return out
}

func (w *work) journalIDs() []string {
	rows := w.journal.Entries()
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func (w *work) begin(stage string, total int) time.Time {
	if total < 0 {
		total = 0
	}
	w.emit(stage, 0, total)
	return time.Now()
}

func (w *work) finish(stage string, start time.Time, total int) {
	w.durations[stage] = time.Since(start).Milliseconds()
	if total < 0 {
		total = 0
	}
	w.emit(stage, total, total)
	slog.Info("pipeline", "stage", stage, "total", total, "duration_ms", w.durations[stage])
}

func (w *work) emit(stage string, done, total int) {
	if w.progress == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.progress(stage, done, total)
}

func (w *work) texts() texts {
	if strings.TrimSpace(w.digestMD) == "" {
		model := w.model
		if model.Period.End.IsZero() {
			model.Period = w.period
			model.ControlTime = w.params.ControlTime.In(timeutil.MSK)
			model.Empty = true
			if strings.TrimSpace(model.Shortfall) == "" {
				model.Shortfall = "прогон не дошёл до сборки выпуска"
			}
		}
		w.model = model
		w.renderLocal()
	}
	return texts{digest: w.digestMD, post: w.post, email: w.email, html: w.html}
}

func (w *work) snapshot() *domain.RunState {
	rows := w.journal.Entries()
	stored := append([]domain.JournalEntry(nil), rows...)
	journalMap := make(map[string]*domain.JournalEntry, len(stored))
	for i := range stored {
		journalMap[stored[i].ID] = &stored[i]
	}
	return &domain.RunState{
		Params:   w.params,
		Period:   w.period,
		Articles: append([]domain.Article(nil), w.articles...),
		Context:  append([]domain.Article(nil), w.context...),
		Journal:  journalMap,
		Mentions: append([]domain.Mention(nil), w.mentions...),
		Events:   append([]domain.Event(nil), w.events...),
		Digest:   w.model,
	}
}

func (w *work) report(state *domain.RunState) domain.RunReport {
	finished := w.deps.Now()
	if finished.Before(w.started) {
		finished = w.started
	}
	stats := llmStats(w.meter, w.deps.Config.LLM)
	findings := w.verify.Findings
	if findings == nil {
		findings = []domain.VerifyFinding{}
	}
	return domain.RunReport{
		RunID:            runID(w.started, w.params),
		StartedAt:        w.started.In(timeutil.MSK).Format(time.RFC3339),
		FinishedAt:       finished.In(timeutil.MSK).Format(time.RFC3339),
		DurationMS:       finished.Sub(w.started).Milliseconds(),
		ControlTime:      timeutil.FormatMSK(w.params.ControlTime),
		Period:           domain.ReportPeriod{Start: timeutil.FormatMSK(w.period.Start), End: timeutil.FormatMSK(w.period.End)},
		AppVersion:       w.deps.Version,
		GoVersion:        runtime.Version(),
		LLM:              stats,
		PromptsHash:      prompts.Hash(),
		ConfigHash:       hashConfig(w.deps.Config, w.deps.ConfigRaw),
		DatasetHash:      w.datasetHash,
		Counts:           countOf(state),
		StageDurationsMS: copyDurations(w.durations),
		Verify: domain.VerifyReport{
			Retries:  w.verify.Retries,
			Findings: findings,
			Degraded: w.verify.Degraded,
		},
		ArtifactHashes: map[string]string{},
	}
}

func llmStats(meter *llm.Meter, cfg config.LLM) domain.LLMStats {
	stats := domain.LLMStats{
		Provider:        cfg.Provider,
		Model:           cfg.Model,
		Temperature:     cfg.Temperature,
		ByTask:          map[string]domain.LLMTaskStats{},
		CostUnavailable: true,
	}
	if meter == nil {
		return stats
	}
	snap := meter.Snapshot()
	if snap.ByTask == nil {
		snap.ByTask = map[string]domain.LLMTaskStats{}
	}
	if snap.Provider == "" {
		snap.Provider = stats.Provider
	}
	if snap.Model == "" {
		snap.Model = stats.Model
	}
	return snap
}

func countOf(state *domain.RunState) domain.RunCounts {
	if state == nil {
		return domain.RunCounts{}
	}
	counts := domain.RunCounts{Events: len(state.Events)}
	for _, row := range state.Journal {
		if row == nil {
			continue
		}
		counts.Total++
		switch row.Status {
		case domain.StatusIncluded:
			counts.Included++
		case domain.StatusMerged:
			counts.Merged++
		case domain.StatusExcluded:
			counts.Excluded++
		}
	}
	return counts
}

func copyDurations(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func runID(started time.Time, params domain.RunParams) string {
	stamp := started.In(timeutil.MSK).Format("20060102T150405")
	return stamp + "-" + shortHash(params.DatasetPath+"|"+params.ControlTime.UTC().Format(time.RFC3339Nano))
}

func digestModel(period domain.Period, control time.Time, items []domain.DigestItem) domain.DigestModel {
	model := domain.DigestModel{
		Period:      period,
		ControlTime: control.In(timeutil.MSK),
		Items:       items,
	}
	if len(items) == 0 {
		model.Empty = true
		model.Shortfall = "после отбора не осталось событий"
		return model
	}
	if len(items) < 5 {
		model.Shortfall = "Подходящих событий меньше пяти."
	}
	return model
}

func templateItems(events []domain.Event, arts map[string]domain.Article) []domain.DigestItem {
	items := make([]domain.DigestItem, 0, len(events))
	for _, event := range events {
		title := "Событие"
		if len(event.Mentions) > 0 && strings.TrimSpace(event.Mentions[0].TitleDraft) != "" {
			title = event.Mentions[0].TitleDraft
		}
		rationale := strings.TrimSpace(event.Rationale)
		if rationale == "" {
			rationale = "Обоснование отбора в данных события не приведено."
		}
		items = append(items, domain.DigestItem{
			N:            len(items) + 1,
			EventID:      event.ID,
			Title:        title,
			Essence:      "Источник описывает событие. Подробности приведены в публикации.",
			Consequences: summarize.NoConsequences,
			Sources:      summarize.SourceRefs(event, arts),
			Rationale:    rationale,
		})
	}
	return items
}

func selectedEvents(events []domain.Event) []domain.Event {
	var out []domain.Event
	for _, event := range events {
		if event.Selected {
			out = append(out, event)
		}
	}
	return out
}

func mskStamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return timeutil.FormatMSK(*t)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
