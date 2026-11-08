package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/ingest"
	"mono/internal/journal"
	"mono/internal/llm"
	"mono/internal/llm/mock"
	"mono/internal/normalize"
	"mono/internal/prompts"
	"mono/internal/verify"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestSyntheticTwiceIsStable(t *testing.T) {
	cfg := config.Default()
	path := filepath.Join(repoRoot(t), "testdata", "synthetic", "articles.json")
	control := time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))

	run := func(t *testing.T) (*domain.RunState, domain.RunReport) {
		t.Helper()
		client := syntheticMock(t, path, cfg)
		meter := llm.NewMeter(cfg.LLM)
		out := t.TempDir()
		state, report, err := New(Deps{
			Config:  cfg,
			Client:  llm.WithMeter(client, meter),
			Meter:   meter,
			Version: "test",
		}).Run(context.Background(), domain.RunParams{
			DatasetPath: path,
			ControlTime: control,
			OutDir:      out,
		}, nil)
		if err != nil {
			t.Fatalf("прогон: %v", err)
		}
		assertOutputs(t, out, state)
		if report.LLM.Cost != nil || !report.LLM.CostUnavailable {
			t.Fatalf("стоимость должна быть недоступна: %+v", report.LLM)
		}
		if report.PromptsHash != prompts.Hash() {
			t.Fatalf("хэш промптов %q", report.PromptsHash)
		}
		if report.DatasetHash == "" || report.ConfigHash == "" {
			t.Fatalf("пустой хэш набора или конфигурации")
		}
		if report.Verify.Findings == nil {
			t.Fatal("findings == nil")
		}
		raw, err := os.ReadFile(filepath.Join(out, "run_report.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"cost": null`) || !strings.Contains(string(raw), `"cost_unavailable": true`) {
			t.Fatalf("отчёт без null-стоимости: %s", raw)
		}
		return state, report
	}

	first, firstReport := run(t)
	second, secondReport := run(t)
	if got, want := journalSig(first), journalSig(second); got != want {
		t.Fatalf("статусы разошлись\n%s\n%s", got, want)
	}
	if got, want := eventSig(first), eventSig(second); got != want {
		t.Fatalf("события разошлись\n%s\n%s", got, want)
	}
	if firstReport.LLM.CallsTotal != secondReport.LLM.CallsTotal || firstReport.LLM.CallsTotal == 0 {
		t.Fatalf("вызовы %d и %d", firstReport.LLM.CallsTotal, secondReport.LLM.CallsTotal)
	}
	assertCode(t, first, "syn-04", domain.ReasonOutOfPeriod)
	assertCode(t, first, "syn-27", domain.ReasonDateUnconfirmed)
	assertCode(t, first, "syn-32", domain.ReasonOutOfPeriod)
	assertCode(t, first, "syn-18", domain.ReasonOffTopic)
	assertCode(t, first, "syn-15", domain.ReasonAdvertising)
	if first.Journal["syn-34"].ReasonCode == domain.ReasonOutOfPeriod || first.Journal["syn-34"].ReasonCode == domain.ReasonDateUnconfirmed {
		t.Fatalf("syn-34 на границе периода: %s", first.Journal["syn-34"].ReasonCode)
	}
	if statusCount(first, domain.StatusIncluded) == 0 {
		t.Fatal("ни один материал не включён")
	}
	if reasonCount(first, domain.ReasonLowerPriority) == 0 {
		t.Fatal("нет причины lower_priority у невыбранных событий")
	}
	if firstReport.StageDurationsMS["load"] < 0 || firstReport.StageDurationsMS["verify"] < 0 {
		t.Fatalf("нет времени этапов: %+v", firstReport.StageDurationsMS)
	}
	if _, ok := firstReport.StageDurationsMS["verify"]; !ok {
		t.Fatalf("нет этапа verify: %+v", firstReport.StageDurationsMS)
	}
}

func reasonCount(state *domain.RunState, code domain.ReasonCode) int {
	n := 0
	for _, row := range state.Journal {
		if row.ReasonCode == code {
			n++
		}
	}
	return n
}

func TestItemFailureDoesNotStopRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "articles.json")
	writeArticles(t, path, articleJSON("item-ok-01", longBody("Утечка базы")), articleJSON("item-fail-01", longBody("Атака шифровальщика")))
	m := baseMock()
	m.When("extract_events", "item-ok-01", mentionJSON(snippet(longBody("Утечка базы"))))
	m.Fail("extract_events", "item-fail-01", errors.New("сбой извлечения"))
	out := filepath.Join(dir, "out")
	state, _, err := runFixture(t, m, path, nil, out)
	if err != nil {
		t.Fatal(err)
	}
	failed := state.Journal["item-fail-01"]
	if failed == nil || failed.Status != domain.StatusExcluded || !strings.Contains(failed.Error, "сбой извлечения") {
		t.Fatalf("сбой не записан: %+v", failed)
	}
	ok := state.Journal["item-ok-01"]
	if ok == nil || ok.Status != domain.StatusIncluded {
		t.Fatalf("успешный материал: %+v", ok)
	}
	assertOutputs(t, out, state)
}

func TestMissingClientStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "articles.json")
	writeArticles(t, path, articleJSON("only-01", longBody("Утечка данных")))
	out := filepath.Join(dir, "out")
	state, report, err := New(Deps{Config: config.Default(), Version: "test"}).Run(context.Background(), domain.RunParams{
		DatasetPath: path,
		ControlTime: controlTime(),
		OutDir:      out,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "клиент LLM") {
		t.Fatalf("ожидалась ошибка клиента, получено %v", err)
	}
	row := state.Journal["only-01"]
	if row == nil || row.Status != domain.StatusExcluded || !strings.Contains(row.Reason, "остановлен") {
		t.Fatalf("строка журнала: %+v", row)
	}
	if report.RunID == "" || report.GoVersion == "" {
		t.Fatalf("пустой отчёт: %+v", report)
	}
	for _, name := range outputNames {
		if _, statErr := os.Stat(filepath.Join(out, name)); statErr != nil {
			t.Fatal(statErr)
		}
	}
	assertOutputs(t, out, state)
}

func TestExtraFileIsIncluded(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.json")
	extraPath := filepath.Join(dir, "extra.json")
	writeArticles(t, mainPath, articleJSON("main-01", longBody("Утечка архива")))
	writeArticles(t, extraPath, articleJSON("extra-01", longBody("Атака на портал")))
	m := baseMock()
	m.When("extract_events", "main-01", mentionJSON(snippet(longBody("Утечка архива"))))
	m.When("extract_events", "extra-01", mentionJSON(snippet(longBody("Атака на портал"))))
	state, _, err := runFixture(t, m, mainPath, []string{extraPath}, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Journal["main-01"] == nil || state.Journal["extra-01"] == nil {
		t.Fatalf("нет обеих записей: %d", len(state.Journal))
	}
	if state.Journal["extra-01"].Status == "" {
		t.Fatal("у дополнительной публикации нет статуса")
	}
}

func syntheticMock(t *testing.T, path string, cfg config.Config) *mock.Client {
	t.Helper()
	articles, _, err := ingest.LoadDataset(context.Background(), []string{path}, cfg.Ingest)
	if err != nil {
		t.Fatal(err)
	}
	normalize.ApplyAll(articles)
	m := baseMock()
	for _, id := range []string{"syn-15", "syn-16", "syn-17"} {
		m.When("classify_topic", id, `{"is_on_topic":true,"is_advertising":true,"confidence":0.95,"reason":"рекламный анонс"}`)
	}
	for _, id := range []string{"syn-18", "syn-19", "syn-20", "syn-21"} {
		m.When("classify_topic", id, `{"is_on_topic":false,"is_advertising":false,"confidence":0.9,"reason":"нет события информационной безопасности"}`)
	}
	for _, article := range articles {
		quote := snippet(article.Text)
		if quote == "" {
			quote = snippet(article.Title)
		}
		if quote == "" {
			continue
		}
		m.When("extract_events", article.ID, mentionJSON(quote))
	}
	return m
}

func baseMock() *mock.Client {
	m := mock.New()
	m.When("classify_topic", "", `{"is_on_topic":true,"is_advertising":false,"confidence":0.9,"reason":"есть признаки инцидента"}`)
	m.When("extract_events", "", `{"mentions":[]}`)
	m.When("cluster_confirm", "", `{"same_event":false,"shared_entities":[],"reason":"разные события","conflicts":[]}`)
	m.When("update_check", "", `{"new_facts":["свежие сведения"]}`)
	m.When("score_event", "", `{"impact":{"value":null,"quote":"","note":"нет данных"},"scale":{"value":null,"quote":"","note":"нет данных"},"urgency":{"value":null,"quote":"","note":"нет данных"},"relevance":{"value":null,"quote":"","note":"нет данных"}}`)
	m.When("rationale", "", `{"rationale":""}`)
	m.When("write_digest_item", "", `{"title":"Событие в источнике","essence":"Источник описывает событие. Подробности приведены в публикации.","consequences":"","hedge_note":""}`)
	m.When("write_post", "", `{"items":[]}`)
	return m
}

func runFixture(t *testing.T, client llm.Client, path string, extra []string, out string) (*domain.RunState, domain.RunReport, error) {
	t.Helper()
	cfg := config.Default()
	meter := llm.NewMeter(cfg.LLM)
	return New(Deps{
		Config:  cfg,
		Client:  llm.WithMeter(client, meter),
		Meter:   meter,
		Version: "test",
	}).Run(context.Background(), domain.RunParams{
		DatasetPath: path,
		ControlTime: controlTime(),
		OutDir:      out,
		ExtraPaths:  extra,
	}, nil)
}

func assertOutputs(t *testing.T, out string, state *domain.RunState) {
	t.Helper()
	for _, name := range outputNames {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("нет файла %s: %v", name, err)
		}
	}
	digest, err := os.ReadFile(filepath.Join(out, "mono.md"))
	if err != nil {
		t.Fatal(err)
	}
	csvRaw, err := os.ReadFile(filepath.Join(out, "journal.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify.CheckFormat(string(digest), string(csvRaw)); err != nil {
		t.Fatalf("формат: %v\n%s", err, digest)
	}
	j := journal.New()
	for _, id := range journalOrder(state) {
		row := state.Journal[id]
		j.Add(*row)
	}
	ids := make([]string, 0, len(state.Journal))
	for _, row := range j.Entries() {
		ids = append(ids, row.ID)
	}
	if err := j.Validate(ids); err != nil {
		t.Fatal(err)
	}
	reportRaw, err := os.ReadFile(filepath.Join(out, "run_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report domain.RunReport
	if err := json.Unmarshal(reportRaw, &report); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(digest)
	if report.ArtifactHashes["mono.md"] != sum {
		t.Fatalf("хэш mono.md %q, в отчёте %q", sum, report.ArtifactHashes["mono.md"])
	}
}

func assertCode(t *testing.T, state *domain.RunState, id string, code domain.ReasonCode) {
	t.Helper()
	row := state.Journal[id]
	if row == nil {
		t.Fatalf("нет %s", id)
	}
	if row.ReasonCode != code || row.Status != domain.StatusExcluded {
		t.Fatalf("%s: статус %s код %s причина %s", id, row.Status, row.ReasonCode, row.Reason)
	}
}

func journalSig(state *domain.RunState) string {
	var b strings.Builder
	for _, id := range journalOrder(state) {
		row := state.Journal[id]
		b.WriteString(row.ID)
		b.WriteByte('\t')
		b.WriteString(string(row.Status))
		b.WriteByte('\t')
		b.WriteString(row.Group)
		b.WriteByte('\t')
		b.WriteString(string(row.ReasonCode))
		b.WriteByte('\t')
		b.WriteString(row.Reason)
		b.WriteByte('\n')
	}
	return b.String()
}

func eventSig(state *domain.RunState) string {
	var b strings.Builder
	for _, event := range state.Events {
		b.WriteString(event.ID)
		b.WriteByte('\t')
		if event.Selected {
			b.WriteString("1")
		} else {
			b.WriteString("0")
		}
		b.WriteByte('\t')
		b.WriteString(strings.Join(event.ArticleIDs, ","))
		b.WriteByte('\n')
	}
	return b.String()
}

func journalOrder(state *domain.RunState) []string {
	seen := map[string]bool{}
	var ids []string
	for _, article := range state.Articles {
		if article.ID == "" || seen[article.ID] {
			continue
		}
		seen[article.ID] = true
		ids = append(ids, article.ID)
	}
	for id := range state.Journal {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func statusCount(state *domain.RunState, status domain.Status) int {
	n := 0
	for _, row := range state.Journal {
		if row.Status == status {
			n++
		}
	}
	return n
}

func controlTime() time.Time {
	return time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
}

func articleJSON(id, text string) string {
	raw, err := json.Marshal(map[string]string{
		"id":           id,
		"title":        "Инцидент " + id,
		"text":         text,
		"source":       "Хабр",
		"url":          "https://example.com/" + id,
		"published_at": "2026-11-07T10:00:00+03:00",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func writeArticles(t *testing.T, path string, rows ...string) {
	t.Helper()
	body := "[\n" + strings.Join(rows, ",\n") + "\n]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func longBody(lead string) string {
	return lead + ". " + strings.Repeat("Зафиксирована кибератака на контур организации и утечка служебных данных. ", 12)
}

func snippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	rs := []rune(text)
	if len(rs) > 160 {
		rs = rs[:160]
	}
	return strings.TrimSpace(string(rs))
}

func mentionJSON(quote string) string {
	raw, err := json.Marshal(map[string]any{
		"mentions": []any{map[string]any{
			"title_draft":          "Событие",
			"facts":                []any{map[string]any{"text": quote, "quote": quote}},
			"affected":             "",
			"consequences":         "",
			"hedges":               []string{},
			"entities":             []string{},
			"is_company_statement": false,
			"is_reprint":           false,
			"reporter":             "",
			"references_earlier":   false,
			"new_facts":            []string{},
			"sufficient_text":      true,
		}},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не найден путь пакета")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

var outputNames = []string{
	"digest.md",
	"post.md",
	"email.md",
	"mono.md",
	"post.txt",
	"email_preview.html",
	"email.txt",
	"journal.csv",
	"journal.json",
	"run_report.json",
}
