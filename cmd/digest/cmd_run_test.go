package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cyberdigest/internal/verify"
)

func TestRunHelpAndBadArgs(t *testing.T) {
	if code := runRun(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("help %d", code)
	}
	errOut := captureStderr(t, func() {
		if code := runRun(context.Background(), []string{"--nope"}); code != 2 {
			t.Fatalf("code %d", code)
		}
	})
	if !strings.Contains(errOut, "неизвестный") {
		t.Fatalf("stderr: %s", errOut)
	}
	errOut = captureStderr(t, func() {
		code := runRun(context.Background(), []string{
			"--dataset", "articles.json",
			"--control-time", "не дата",
			"--out", t.TempDir(),
			"--config", filepath.Join(moduleRoot(t), "config.yaml"),
		})
		if code != 2 {
			t.Fatalf("code %d", code)
		}
	})
	if !strings.Contains(errOut, "контрольное время") {
		t.Fatalf("stderr: %s", errOut)
	}
}

func TestRunMockSynthetic(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	code := runRun(context.Background(), []string{
		"--dataset", filepath.Join(root, "testdata", "synthetic", "articles.json"),
		"--control-time", "07.11.2026 12:00",
		"--out", out,
		"--config", filepath.Join(root, "config.yaml"),
		"--mock",
	})
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	digest, err := os.ReadFile(filepath.Join(out, "digest.md"))
	if err != nil {
		t.Fatal(err)
	}
	journalCSV, err := os.ReadFile(filepath.Join(out, "journal.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verify.CheckFormat(string(digest), string(journalCSV)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"post.txt", "email_preview.html", "email.txt", "journal.json", "run_report.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunWithoutKeyWritesAndExitsOne(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	root := moduleRoot(t)
	dir := t.TempDir()
	dataset := filepath.Join(dir, "articles.json")
	body := `[{"id":"a1","title":"Атака","text":"Зафиксирована кибератака на контур организации и утечка служебных данных. Повторение для длины текста. Зафиксирована кибератака на контур организации и утечка служебных данных.","source":"Хабр","url":"https://example.com/a1","published_at":"2026-11-07T10:00:00+03:00"}]`
	if err := os.WriteFile(dataset, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	var code int
	errOut := captureStderr(t, func() {
		code = runRun(context.Background(), []string{
			"--dataset", dataset,
			"--control-time", "2026-11-07T12:00:00+03:00",
			"--out", out,
			"--config", filepath.Join(root, "config.yaml"),
		})
	})
	if code != 1 {
		t.Fatalf("code %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "LLM_API_KEY") {
		t.Fatalf("stderr: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(out, "journal.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "run_report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRunLiveFixtures(t *testing.T) {
	root := moduleRoot(t)
	dir := t.TempDir()
	dataset := filepath.Join(dir, "articles.json")
	body := `[{"id":"base-1","title":"База","text":"Короткая запись набора. Вторая фраза тоже короткая.","source":"Хакер","url":"https://xakep.ru/base","published_at":"2026-11-07T08:00:00+03:00"}]`
	if err := os.WriteFile(dataset, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--dataset", dataset,
		"--control-time", "2026-11-07T12:00:00+03:00",
		"--out", filepath.Join(dir, "off"),
		"--config", filepath.Join(root, "config.yaml"),
		"--mock",
	}
	if code := runRun(context.Background(), args); code != 0 {
		t.Fatalf("прогон без --live: код %d", code)
	}
	off, err := os.ReadFile(filepath.Join(dir, "off", "journal.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "live-") || strings.Contains(string(off), "Источник недоступен") {
		t.Fatalf("прогон без флага затронул живой сбор:\n%s", off)
	}

	args[5] = filepath.Join(dir, "on")
	args = append(args, "--live", "--sources", filepath.Join(root, "testdata", "live", "sources.yaml"))
	if code := runRun(context.Background(), args); code != 0 {
		t.Fatalf("прогон с --live: код %d", code)
	}
	journalRaw, err := os.ReadFile(filepath.Join(dir, "on", "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		ReasonCode string `json:"reason_code"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal(journalRaw, &rows); err != nil {
		t.Fatal(err)
	}
	liveRows := 0
	down := false
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "live-") && row.ID != "live-down" {
			liveRows++
		}
		if row.ReasonCode == "source_unavailable" && row.Reason == "Источник недоступен" {
			down = true
		}
	}
	if !down {
		t.Fatal("нет строки source_unavailable")
	}
	if liveRows != 5 {
		t.Fatalf("живых записей %d: %+v", liveRows, rows)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не найден путь пакета")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
