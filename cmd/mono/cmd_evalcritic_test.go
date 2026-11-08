package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvalCriticMockWritesReport(t *testing.T) {
	dir := t.TempDir()
	code := runEvalCritic(context.Background(), []string{
		"--mock", "--out", dir, "--config", filepath.Join(moduleRoot(t), "config.yaml"),
	})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	for _, name := range []string{"critic_eval.json", "critic_eval.md"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 {
			t.Fatalf("empty %s", name)
		}
		if strings.Contains(string(raw), "\u2014") || strings.Contains(string(raw), "\u2013") {
			t.Fatalf("long dash in %s", name)
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, "critic_eval.md"))
	if !strings.Contains(string(md), "planted") {
		t.Fatalf("markdown missing planted case:\n%s", md)
	}
}

func TestEvalCriticHelp(t *testing.T) {
	if code := runEvalCritic(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}
