package main

import (
	"strings"
	"testing"

	"cyberdigest/internal/config"
)

func TestFakeAndMockTogether(t *testing.T) {
	err := run([]string{"--fake", "--mock"})
	if err == nil || !strings.Contains(err.Error(), "вместе") {
		t.Fatalf("conflict: %v", err)
	}
}

func TestBuildMockRunner(t *testing.T) {
	got, err := buildRunner(config.Default(), []byte("llm: {}\n"), true)
	if err != nil || got == nil {
		t.Fatal(err)
	}
}

func TestBuildRunnerNeedsKey(t *testing.T) {
	_, err := buildRunner(config.Default(), nil, false)
	if err == nil || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("key: %v", err)
	}
}
