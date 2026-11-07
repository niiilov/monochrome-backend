package main

import (
	"context"
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

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не найден путь пакета")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
