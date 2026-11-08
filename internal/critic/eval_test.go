package critic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mono/internal/config"
	"mono/internal/llm"
	"mono/internal/llm/mock"
)

func TestRunEvalMockReducesPlantedDiscrepancies(t *testing.T) {
	client := ScriptEvalMock(mock.New())
	meter := llm.NewMeter(config.LLM{Provider: "mock", Model: "mock", Temperature: 0})
	wrapped := llm.WithMeter(client, meter)
	rep, err := RunEval(context.Background(), wrapped, meter, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Cases) != 2 {
		t.Fatalf("cases=%d", len(rep.Cases))
	}
	var planted CaseResult
	for _, c := range rep.Cases {
		if c.Name == CasePlanted {
			planted = c
		}
		if c.Name == CaseTrain && c.With.Discrepancies != 0 {
			t.Fatalf("train with critic still has discrepancies: %+v", c.With)
		}
	}
	if planted.Name == "" {
		t.Fatal("no planted case")
	}
	if planted.Without.Discrepancies < 1 {
		t.Fatalf("planted without critic must keep lies: %+v", planted.Without)
	}
	if planted.With.Discrepancies >= planted.Without.Discrepancies {
		t.Fatalf("critic did not reduce discrepancies: without=%d with=%d", planted.Without.Discrepancies, planted.With.Discrepancies)
	}
	if planted.With.CriticCalls < 1 {
		t.Fatal("expected critic calls in with-critic mode")
	}
	if planted.Without.CriticCalls != 0 {
		t.Fatal("without-critic mode must not call critic_check")
	}
}

func TestWriteReport(t *testing.T) {
	client := ScriptEvalMock(mock.New())
	meter := llm.NewMeter(config.LLM{Provider: "mock", Model: "mock"})
	rep, err := RunEval(context.Background(), llm.WithMeter(client, meter), meter, true)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "critic_eval.json")
	mdPath := filepath.Join(dir, "critic_eval.md")
	if err := WriteReport(rep, jsonPath, mdPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "Замер эффекта") || !strings.Contains(text, "planted") {
		t.Fatalf("bad markdown:\n%s", text)
	}
	if strings.Contains(text, "\u2014") || strings.Contains(text, "\u2013") {
		t.Fatal("long dash in report")
	}
}
