package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/ingest/live"
	"mono/internal/llm"
)

func TestLiveFixturesProduceEvents(t *testing.T) {
	root := repoRoot(t)
	cfg := config.Default()
	cfg.Live.Enabled = true
	cfg.Live.SourcesFile = filepath.Join(root, "testdata", "live", "sources.yaml")
	cfg.Live.RespectRobots = true
	got := live.Collect(context.Background(), cfg.Live)
	if len(got.Articles) != 5 {
		t.Fatalf("фикстуры: %d статей, журнал %+v", len(got.Articles), got.Journal)
	}
	client := baseMock()
	for _, article := range got.Articles {
		quote := snippet(article.Text)
		if quote == "" {
			t.Fatalf("нет цитаты для %s", article.ID)
		}
		client.When("extract_events", article.ID, mentionJSON(quote))
	}
	dir := t.TempDir()
	dataset := filepath.Join(dir, "articles.json")
	body := `[{"id":"base-1","title":"База","text":"Короткая запись набора. Вторая фраза тоже короткая.","source":"Хакер","url":"https://xakep.ru/base","published_at":"2026-11-07T08:00:00+03:00"}]`
	if err := os.WriteFile(dataset, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	meter := llm.NewMeter(cfg.LLM)
	state, report, err := New(Deps{
		Config:  cfg,
		Client:  llm.WithMeter(client, meter),
		Meter:   meter,
		Version: "test",
	}).Run(context.Background(), domain.RunParams{
		DatasetPath: dataset,
		ControlTime: controlTime(),
		OutDir:      out,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Events < 1 || len(state.Events) < 1 {
		t.Fatalf("событий %d / %d", report.Counts.Events, len(state.Events))
	}
	liveIn := 0
	down := false
	for _, row := range state.Journal {
		if row == nil {
			continue
		}
		if strings.HasPrefix(row.ID, "live-") && (row.Status == domain.StatusIncluded || row.Status == domain.StatusMerged) {
			liveIn++
		}
		if row.ReasonCode == domain.ReasonSourceDown {
			down = true
		}
	}
	if !down {
		t.Fatal("нет source_unavailable")
	}
	if liveIn == 0 {
		t.Fatal("ни одна живая запись не вошла в событие")
	}
	digest, err := os.ReadFile(filepath.Join(out, "mono.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(digest), "## 1.") {
		t.Fatalf("выпуск:\n%s", digest)
	}
}

func TestLiveFlagWithoutGlobalSwitch(t *testing.T) {
	root := repoRoot(t)
	cfg := config.Default()
	cfg.Live.Enabled = false
	cfg.Live.SourcesFile = filepath.Join(root, "testdata", "live", "sources.yaml")
	got := live.Collect(context.Background(), cfg.Live)
	client := baseMock()
	for _, article := range got.Articles {
		quote := snippet(article.Text)
		client.When("extract_events", article.ID, mentionJSON(quote))
	}
	dir := t.TempDir()
	meter := llm.NewMeter(cfg.LLM)
	state, _, err := New(Deps{
		Config:  cfg,
		Client:  llm.WithMeter(client, meter),
		Meter:   meter,
		Version: "test",
	}).Run(context.Background(), domain.RunParams{
		Live:        true,
		ControlTime: controlTime(),
		OutDir:      filepath.Join(dir, "out"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var liveRows int
	for _, row := range state.Journal {
		if row != nil && strings.HasPrefix(row.ID, "live-") {
			liveRows++
		}
	}
	if liveRows == 0 {
		t.Fatal("флаг прогона не включил сбор")
	}
}

func TestLiveDisabledDoesNotReadSources(t *testing.T) {
	cfg := config.Default()
	cfg.Live.Enabled = false
	cfg.Live.SourcesFile = filepath.Join(t.TempDir(), "missing-sources.yaml")
	dir := t.TempDir()
	dataset := filepath.Join(dir, "articles.json")
	body := `[{"id":"base-1","title":"Утечка","text":"Зафиксирована утечка персональных данных в контуре организации. Вторая фраза описывает проверку доступа и отключение сегмента.","source":"Хакер","url":"https://xakep.ru/base","published_at":"2026-11-07T10:00:00+03:00"}]`
	if err := os.WriteFile(dataset, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	client := baseMock()
	quote := snippet("Зафиксирована утечка персональных данных в контуре организации. Вторая фраза описывает проверку доступа и отключение сегмента.")
	client.When("extract_events", "base-1", mentionJSON(quote))
	out := filepath.Join(dir, "out")
	meter := llm.NewMeter(cfg.LLM)
	state, _, err := New(Deps{
		Config:  cfg,
		Client:  llm.WithMeter(client, meter),
		Meter:   meter,
		Version: "test",
	}).Run(context.Background(), domain.RunParams{
		DatasetPath: dataset,
		ControlTime: controlTime(),
		OutDir:      out,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.Journal {
		if row != nil && (row.ReasonCode == domain.ReasonSourceDown || strings.HasPrefix(row.ID, "live-")) {
			t.Fatalf("выключенный сбор попал в журнал: %+v", row)
		}
	}
}
