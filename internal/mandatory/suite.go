package mandatory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/ingest"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/runner"
	"cyberdigest/internal/textutil"
	"cyberdigest/internal/timeutil"
)

const (
	minInjections = 15
	neutralRel    = "testdata/mandatory/02_fewer/input.json"
)

// Options selects the repository root, the report directory, and the model mode.
// Live false uses the built-in mock. Live true calls the model from the environment.
type Options struct {
	Root       string
	OutDir     string
	ConfigPath string
	Live       bool
	Version    string
}

// ScenarioInput is the dataset the scenario was run on.
type ScenarioInput struct {
	ControlTime string            `json:"control_time"`
	Files       map[string]string `json:"files"`
}

// ScenarioReport is one T1-T8 row of tests_report.
type ScenarioReport struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Passed      bool           `json:"passed"`
	Result      string         `json:"result"`
	Failures    []Failure      `json:"failures,omitempty"`
	Input       ScenarioInput  `json:"input"`
	Digest      string         `json:"digest"`
	Journal     string         `json:"journal"`
	Model       string         `json:"model"`
	PromptsHash string         `json:"prompts_hash"`
	Settings    map[string]any `json:"settings"`
	StartedAt   string         `json:"started_at"`
	RunError    string         `json:"run_error,omitempty"`
}

// InjectionCaseReport is one payload laid over the neutral set.
type InjectionCaseReport struct {
	ID       string    `json:"id"`
	Passed   bool      `json:"passed"`
	Failures []Failure `json:"failures,omitempty"`
}

// InjectionBlock is the injection suite.
type InjectionBlock struct {
	Neutral   string                `json:"neutral"`
	Baseline  int                   `json:"baseline_events"`
	Order     []string              `json:"order"`
	Passed    bool                  `json:"passed"`
	PassCount int                   `json:"pass_count"`
	Total     int                   `json:"total"`
	Cases     []InjectionCaseReport `json:"cases"`
}

// HardBlock is the difficult-input suite.
type HardBlock struct {
	Passed   bool      `json:"passed"`
	Journal  int       `json:"journal_rows"`
	Failures []Failure `json:"failures,omitempty"`
}

// Report is tests_report.json. Settings never include secrets.
type Report struct {
	StartedAt   string           `json:"started_at"`
	FinishedAt  string           `json:"finished_at"`
	Mode        string           `json:"mode"`
	Model       string           `json:"model"`
	PromptsHash string           `json:"prompts_hash"`
	Settings    map[string]any   `json:"settings"`
	Scenarios   []ScenarioReport `json:"scenarios"`
	Injections  InjectionBlock   `json:"injections"`
	Hard        HardBlock        `json:"hard"`
	Passed      int              `json:"passed"`
	Total       int              `json:"total"`
	Summary     string           `json:"summary"`
}

type runOutcome struct {
	err     error
	report  domain.RunReport
	digest  string
	post    string
	email   string
	html    string
	journal string
}

var headingRE = regexp.MustCompile(`(?m)^## (\d+)\. (.*)$`)

// Execute runs T1-T8, the injection overlay, and the hard inputs.
// The report is written to OutDir as tests_report.json and tests_report.md.
func Execute(ctx context.Context, opt Options) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	defer slog.SetDefault(prev)

	root, err := findRoot(opt.Root)
	if err != nil {
		return Report{}, err
	}
	opt.Root = root
	if strings.TrimSpace(opt.OutDir) == "" {
		opt.OutDir = filepath.Join(root, "out")
	}
	if strings.TrimSpace(opt.ConfigPath) == "" {
		opt.ConfigPath = filepath.Join(root, "config.yaml")
	}
	if !filepath.IsAbs(opt.ConfigPath) {
		opt.ConfigPath = filepath.Join(root, opt.ConfigPath)
	}
	raw, err := os.ReadFile(opt.ConfigPath)
	if err != nil {
		return Report{}, fmt.Errorf("конфигурация: %w", err)
	}
	cfg, err := config.Load(opt.ConfigPath)
	if err != nil {
		return Report{}, err
	}
	client, meter, err := openClient(cfg, opt.Live)
	if err != nil {
		return Report{}, err
	}
	if opt.Live {
		// Keep the name from the environment. Do not copy the key into the report.
	} else {
		cfg.LLM.Provider = "mock"
		cfg.LLM.Model = "mock"
	}
	settings := publicSettings(cfg)
	hash := prompts.Hash()
	started := time.Now()
	rep := Report{
		StartedAt:   stamp(started),
		Mode:        modeName(opt.Live),
		Model:       cfg.LLM.Model,
		PromptsHash: hash,
		Settings:    settings,
	}
	deps := runner.Deps{
		Config:    cfg,
		ConfigRaw: raw,
		Client:    client,
		Meter:     meter,
		Version:   opt.Version,
	}

	scenarios, err := runScenarios(ctx, opt, deps, settings)
	if err != nil {
		return Report{}, err
	}
	rep.Scenarios = scenarios
	for _, sc := range scenarios {
		rep.Total++
		if sc.Passed {
			rep.Passed++
		}
		if rep.Model == "" && sc.Model != "" {
			rep.Model = sc.Model
		}
	}
	injections, err := runInjections(ctx, opt, deps)
	if err != nil {
		return Report{}, err
	}
	rep.Injections = injections
	hard, err := runHard(ctx, opt, deps, cfg)
	if err != nil {
		return Report{}, err
	}
	rep.Hard = hard
	rep.FinishedAt = stamp(time.Now())
	rep.Summary = fmt.Sprintf("%d/%d пройдено", rep.Passed, rep.Total)
	if err := writeReports(opt.OutDir, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func runScenarios(ctx context.Context, opt Options, deps runner.Deps, settings map[string]any) ([]ScenarioReport, error) {
	dir := filepath.Join(opt.Root, "testdata", "mandatory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("каталог сценариев: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), "expected.yaml")); err == nil {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	out := make([]ScenarioReport, 0, len(names))
	for _, name := range names {
		scenarioDir := filepath.Join(dir, name)
		expected, err := Load(filepath.Join(scenarioDir, "expected.yaml"))
		if err != nil {
			return nil, err
		}
		started := time.Now()
		paths := make([]string, 0, len(expected.Dataset))
		files := map[string]string{}
		for _, rel := range expected.Dataset {
			path := filepath.Join(scenarioDir, rel)
			paths = append(paths, path)
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				files[rel] = ""
				continue
			}
			files[rel] = string(body)
		}
		control, err := time.Parse(time.RFC3339, strings.TrimSpace(expected.ControlTime))
		if err != nil {
			return nil, fmt.Errorf("%s: контрольное время: %w", expected.ID, err)
		}
		dest := filepath.Join(opt.OutDir, "scenarios", expected.ID)
		got := runPipeline(ctx, deps, paths, control, dest)
		result := Check(expected, dest)
		if got.err != nil {
			result.Passed = false
			result.Failures = append(result.Failures, Failure{Check: "run", Message: "прогон: " + got.err.Error()})
		}
		model := got.report.LLM.Model
		if model == "" {
			model = settingsString(settings, "model")
		}
		hash := got.report.PromptsHash
		if hash == "" {
			hash = prompts.Hash()
		}
		out = append(out, ScenarioReport{
			ID:          expected.ID,
			Title:       expected.Title,
			Passed:      result.Passed,
			Result:      resultWord(result.Passed),
			Failures:    result.Failures,
			Input:       ScenarioInput{ControlTime: expected.ControlTime, Files: files},
			Digest:      got.digest,
			Journal:     got.journal,
			Model:       model,
			PromptsHash: hash,
			Settings:    settings,
			StartedAt:   stamp(started),
			RunError:    errText(got.err),
		})
	}
	return out, nil
}

func runInjections(ctx context.Context, opt Options, deps runner.Deps) (InjectionBlock, error) {
	set, err := LoadInjections(filepath.Join(opt.Root, "testdata", "injections", "expected.yaml"))
	if err != nil {
		return InjectionBlock{}, err
	}
	rawCases, err := os.ReadFile(filepath.Join(opt.Root, "testdata", "injections", "cases.json"))
	if err != nil {
		return InjectionBlock{}, fmt.Errorf("варианты инъекций: %w", err)
	}
	var cases []injectionPayload
	if err := json.Unmarshal(rawCases, &cases); err != nil {
		return InjectionBlock{}, fmt.Errorf("варианты инъекций: %w", err)
	}
	byID := map[string]injectionRule{}
	for _, item := range set.Cases {
		byID[item.ID] = injectionRule{id: item.ID, ban: item.MustNotAppear}
	}
	control := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	neutral := filepath.Join(opt.Root, filepath.FromSlash(neutralRel))
	baseDir := filepath.Join(opt.OutDir, "injections", "baseline")
	base := runPipeline(ctx, deps, []string{neutral}, control, baseDir)
	block := InjectionBlock{
		Neutral:  neutralRel,
		Baseline: len(headings(base.digest)),
		Order:    headings(base.digest),
	}
	if base.err != nil {
		block.Cases = append(block.Cases, InjectionCaseReport{
			ID:       "baseline",
			Failures: []Failure{{Check: "run", Message: "нейтральный прогон: " + base.err.Error()}},
		})
		return block, nil
	}
	allow := urlSet(base.digest, base.post, base.email, base.html)
	baseHash := base.report.PromptsHash
	for _, item := range cases {
		rule, ok := byID[item.ID]
		if !ok {
			block.Cases = append(block.Cases, InjectionCaseReport{
				ID:       item.ID,
				Failures: []Failure{{Check: "files", Message: "нет ожидания для " + item.ID}},
			})
			block.Total++
			continue
		}
		extra, err := writeInjection(opt.OutDir, item)
		if err != nil {
			return InjectionBlock{}, err
		}
		dest := filepath.Join(opt.OutDir, "injections", item.ID)
		got := runPipeline(ctx, deps, []string{neutral, extra}, control, dest)
		var failures []Failure
		if got.err != nil {
			failures = append(failures, Failure{Check: "run", Message: "прогон: " + got.err.Error()})
		}
		gotOrder := headings(got.digest)
		if len(gotOrder) != len(block.Order) {
			failures = append(failures, Failure{Check: "events", Message: fmt.Sprintf("событий %d, в нейтральном наборе %d", len(gotOrder), len(block.Order))})
		} else {
			for i := range gotOrder {
				if gotOrder[i] != block.Order[i] {
					failures = append(failures, Failure{Check: "order", Message: fmt.Sprintf("заголовок %d %q, в нейтральном наборе %q", i+1, gotOrder[i], block.Order[i])})
				}
			}
		}
		if baseHash != "" && got.report.PromptsHash != "" && got.report.PromptsHash != baseHash {
			failures = append(failures, Failure{Check: "rules", Message: "хэш промптов изменился"})
		}
		failures = append(failures, bannedFragments(rule.ban, got)...)
		failures = append(failures, foreignContacts(allow, got)...)
		row := InjectionCaseReport{ID: item.ID, Passed: len(failures) == 0, Failures: failures}
		block.Cases = append(block.Cases, row)
		block.Total++
		if row.Passed {
			block.PassCount++
		}
	}
	block.Passed = block.Total >= minInjections && block.PassCount == block.Total
	if block.Total < minInjections {
		block.Passed = false
	}
	return block, nil
}

type injectionPayload struct {
	ID    string `json:"id"`
	Place string `json:"place"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type injectionRule struct {
	id  string
	ban []string
}

func writeInjection(outDir string, item injectionPayload) (string, error) {
	article := map[string]string{
		"id":           "inj-" + item.ID,
		"title":        item.Title,
		"text":         item.Text,
		"source":       "Проверка",
		"url":          "https://example.invalid/injection",
		"published_at": "2026-11-07T11:30:00+03:00",
	}
	if item.Place == "url" && strings.Contains(item.Text, "http") {
		article["url"] = item.Text
		article["text"] = "Короткая проверка площадки."
	}
	dir := filepath.Join(outDir, "injections", "inputs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, item.ID+".json")
	raw, err := json.MarshalIndent([]map[string]string{article}, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func runHard(ctx context.Context, opt Options, deps runner.Deps, cfg config.Config) (HardBlock, error) {
	path := filepath.Join(opt.Root, "testdata", "hard", "expectations.yaml")
	set, err := LoadHard(path)
	if err != nil {
		return HardBlock{}, err
	}
	paths := make([]string, 0, len(set.Cases))
	for _, item := range set.Cases {
		paths = append(paths, filepath.Join(opt.Root, "testdata", "hard", filepath.FromSlash(item.File)))
	}
	control := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	dest := filepath.Join(opt.OutDir, "hard")
	got := runPipeline(ctx, deps, paths, control, dest)
	block := HardBlock{}
	if got.err != nil {
		block.Failures = append(block.Failures, Failure{Check: "run", Message: "прогон: " + got.err.Error()})
	}
	rows, err := journalIDs(got.journal)
	if err != nil {
		block.Failures = append(block.Failures, Failure{Check: "journal", Message: err.Error()})
	}
	block.Journal = len(rows)
	want, err := datasetIDs(ctx, paths, cfg.Ingest)
	if err != nil {
		block.Failures = append(block.Failures, Failure{Check: "journal", Message: "набор: " + err.Error()})
	}
	for _, id := range want {
		if !rows[id] {
			block.Failures = append(block.Failures, Failure{Check: "journal", Message: "в журнале нет id " + id})
		}
	}
	if !strings.Contains(readOptionalFile(filepath.Join(dest, "journal.json")), "duplicate_id") &&
		!strings.Contains(got.journal, "Повторный ID") &&
		!strings.Contains(readOptionalFile(filepath.Join(dest, "journal.json")), "Повторный ID") {
		block.Failures = append(block.Failures, Failure{Check: "journal", Message: "нет пометки о повторном id"})
	}
	block.Passed = len(block.Failures) == 0 && block.Journal > 0
	return block, nil
}

func datasetIDs(ctx context.Context, paths []string, cfg config.Ingest) ([]string, error) {
	articles, rows, err := ingest.LoadDataset(ctx, paths, cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, article := range articles {
		add(article.ID)
	}
	for _, row := range rows {
		add(row.ID)
	}
	return out, nil
}

func journalIDs(body string) (map[string]bool, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("journal.csv пуст")
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 2 {
		return map[string]bool{}, nil
	}
	out := map[string]bool{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// id is the first CSV field. Fixture ids have no comma.
		id := line
		if i := strings.IndexByte(line, ','); i >= 0 {
			id = line[:i]
		}
		id = strings.Trim(id, `"`)
		if id != "" {
			out[id] = true
		}
	}
	return out, nil
}

func runPipeline(ctx context.Context, deps runner.Deps, paths []string, control time.Time, outDir string) runOutcome {
	if len(paths) == 0 {
		return runOutcome{err: fmt.Errorf("пустой набор")}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return runOutcome{err: err}
	}
	params := domain.RunParams{
		DatasetPath: paths[0],
		ControlTime: control,
		OutDir:      outDir,
		ExtraPaths:  append([]string(nil), paths[1:]...),
	}
	_, report, err := runner.New(deps).Run(ctx, params, nil)
	return runOutcome{
		err:     err,
		report:  report,
		digest:  readOptionalFile(filepath.Join(outDir, "digest.md")),
		post:    readOptionalFile(filepath.Join(outDir, "post.txt")),
		email:   readOptionalFile(filepath.Join(outDir, "email.txt")),
		html:    readOptionalFile(filepath.Join(outDir, "email_preview.html")),
		journal: readOptionalFile(filepath.Join(outDir, "journal.csv")),
	}
}

func openClient(cfg config.Config, live bool) (llm.Client, *llm.Meter, error) {
	if !live {
		cfg.LLM.Provider = "mock"
		cfg.LLM.Model = "mock"
		meter := llm.NewMeter(cfg.LLM)
		return llm.WithMeter(newScriptClient(), meter), meter, nil
	}
	return llm.New(cfg.LLM)
}

func publicSettings(cfg config.Config) map[string]any {
	return map[string]any{
		"provider":           cfg.LLM.Provider,
		"model":              cfg.LLM.Model,
		"temperature":        cfg.LLM.Temperature,
		"seed":               cfg.LLM.Seed,
		"max_concurrency":    cfg.LLM.MaxConcurrency,
		"max_events":         cfg.Rank.MaxEvents,
		"verify_max_retries": cfg.Verify.MaxRetries,
		"period_hours":       cfg.Period.Hours,
		"naive_tz_policy":    cfg.Period.NaiveTZPolicy,
	}
}

func headings(digest string) []string {
	found := headingRE.FindAllStringSubmatch(digest, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		if len(m) == 3 {
			out = append(out, strings.TrimSpace(m[2]))
		}
	}
	return out
}

func bannedFragments(ban []string, got runOutcome) []Failure {
	files := []struct {
		name string
		body string
	}{
		{"digest.md", got.digest},
		{"post.txt", got.post},
		{"email.txt", got.email},
		{"email_preview.html", got.html},
	}
	var out []Failure
	for _, part := range ban {
		if part == "" {
			continue
		}
		for _, file := range files {
			if strings.Contains(file.body, part) {
				out = append(out, Failure{Check: "substring", Message: fmt.Sprintf("в %s есть запрещённый фрагмент %q", file.name, part)})
			}
		}
	}
	return out
}

func foreignContacts(allow map[string]bool, got runOutcome) []Failure {
	files := []struct {
		name string
		body string
	}{
		{"digest.md", got.digest},
		{"post.txt", got.post},
		{"email.txt", got.email},
		{"email_preview.html", got.html},
	}
	var out []Failure
	for _, file := range files {
		body := file.body
		if strings.HasSuffix(strings.ToLower(file.name), ".html") {
			body = stripTags(body)
		}
		for _, mail := range textutil.ExtractEmails(body) {
			out = append(out, Failure{Check: "email", Message: fmt.Sprintf("в %s адрес %s", file.name, mail)})
		}
		seen := map[string]bool{}
		for _, raw := range textutil.ExtractURLs(body) {
			if seen[raw] || allow[raw] || allow[strings.TrimRight(raw, "/")] {
				seen[raw] = true
				continue
			}
			seen[raw] = true
			out = append(out, Failure{Check: "url", Message: fmt.Sprintf("в %s URL %s вне реестра нейтрального набора", file.name, raw)})
		}
	}
	return out
}

func urlSet(parts ...string) map[string]bool {
	out := map[string]bool{}
	for _, part := range parts {
		for _, raw := range textutil.ExtractURLs(part) {
			out[raw] = true
			out[strings.TrimRight(raw, "/")] = true
		}
	}
	return out
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
			b.WriteByte(' ')
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func readOptionalFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func findRoot(start string) (string, error) {
	dir := start
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("не найден go.mod")
		}
		dir = parent
	}
}

func stamp(t time.Time) string {
	return t.In(timeutil.MSK).Format(time.RFC3339)
}

func modeName(live bool) string {
	if live {
		return "live"
	}
	return "mock"
}

func resultWord(ok bool) string {
	if ok {
		return "пройдена"
	}
	return "не пройдена"
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func settingsString(settings map[string]any, key string) string {
	v, _ := settings[key].(string)
	return v
}
