package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/journal"
	"mono/internal/render"
	"mono/internal/timeutil"
)

func TestCheckHelpAndUnknown(t *testing.T) {
	if code := runCheck(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("help %d", code)
	}
	var code int
	errOut := captureStderr(t, func() {
		code = runCheck(context.Background(), []string{"--nope"})
	})
	if code != 2 || !strings.Contains(errOut, "неизвестный") {
		t.Fatalf("code %d\n%s", code, errOut)
	}
}

func TestCheckAcceptsOutDir(t *testing.T) {
	root := t.TempDir()
	writeOut(t, root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if code := runCheck(context.Background(), []string{"--out", "out"}); code != 0 {
		t.Fatalf("code %d", code)
	}
}

func TestCheckRejectsBadJournal(t *testing.T) {
	root := t.TempDir()
	writeOut(t, root)
	if err := os.WriteFile(filepath.Join(root, "out", "journal.csv"), []byte("id,status,group,reason,error\nx,нет,g,r,\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	var code int
	errOut := captureStderr(t, func() {
		code = runCheck(context.Background(), []string{"--out", "out"})
	})
	if code != 1 || !strings.Contains(errOut, "журнал") {
		t.Fatalf("code %d\n%s", code, errOut)
	}
}

func TestCheckRunsOrganizerScript(t *testing.T) {
	if !pythonWorks() {
		t.Skip("python не установлен")
	}
	root := t.TempDir()
	writeOut(t, root)
	if err := os.Mkdir(filepath.Join(root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "import sys\nsys.stderr.write('organizer-fail')\nsys.exit(1)\n"
	if err := os.WriteFile(filepath.Join(root, "tools", "check_mono.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	var code int
	errOut := captureStderr(t, func() {
		code = runCheck(context.Background(), []string{"--out", "out"})
	})
	if code != 1 || !strings.Contains(errOut, "organizer-fail") {
		t.Fatalf("code %d\n%s", code, errOut)
	}
}

func pythonWorks() bool {
	bin := findPython()
	if bin == "" {
		return false
	}
	out, err := exec.Command(bin, "-c", "print(1)").Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}

func writeOut(t *testing.T, root string) {
	t.Helper()
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, timeutil.MSK)
	end := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	model := domain.DigestModel{
		Period:      domain.Period{Start: start, End: end},
		ControlTime: end,
		Empty:       true,
		Shortfall:   "все материалы вне темы",
	}
	dir := filepath.Join(root, "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mono.md"), []byte(render.Digest(model)), 0o644); err != nil {
		t.Fatal(err)
	}
	j := journal.New()
	j.Add(domain.JournalEntry{ID: "a1", Status: domain.StatusExcluded, Reason: "вне темы"})
	var buf bytes.Buffer
	if err := j.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.csv"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
