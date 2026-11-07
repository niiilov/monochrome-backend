package mandatory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberdigest/internal/llm"
)

func TestScriptAnswersStayOnSchema(t *testing.T) {
	client := newScriptClient()
	ctx := context.Background()
	cases := []llm.Request{
		{Task: "classify_topic", User: `<article id="t1-sl">текст</article>`},
		{Task: "classify_topic", User: `<article id="t3-sport">спорт</article>`},
		{Task: "extract_events", User: `<article id="t8-inject">текст</article>`},
		{Task: "cluster_confirm", User: "ID статьи: t6-co\nID статьи: t6-news"},
		{Task: "cluster_confirm", User: "ID статьи: t2-phish\nID статьи: t2-ddos"},
		{Task: "update_check", User: "Свежая статья: t4-stale"},
		{Task: "score_event", User: `<article id="t8-base">текст</article>`},
		{Task: "write_digest_item", User: `<article id="t6-co"></article><article id="t6-news"></article>`},
		{Task: "write_post", User: "1. Заголовок\nСуть: Факт.\nПоследствия: нет\nОбоснование: Причина.\nСсылки, не копируй их в text: нет\n"},
		{Task: "rationale", User: "досье"},
	}
	for _, req := range cases {
		resp, err := client.Complete(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", req.Task, err)
		}
		var raw any
		if err := json.Unmarshal([]byte(resp.Text), &raw); err != nil {
			t.Fatalf("%s: %s", req.Task, resp.Text)
		}
		if resp.Model != "mock" {
			t.Fatalf("model %s", resp.Model)
		}
	}
	resp, err := client.Complete(ctx, llm.Request{Task: "classify_topic", User: `<article id="t3-phone">анонс</article>`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, `"is_on_topic":false`) {
		t.Fatalf("off topic: %s", resp.Text)
	}
	resp, err = client.Complete(ctx, llm.Request{Task: "write_digest_item", User: `<article id="t6-co"></article><article id="t6-news"></article>`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "расхожд") {
		t.Fatalf("conflict: %s", resp.Text)
	}
	resp, err = client.Complete(ctx, llm.Request{Task: "extract_events", User: `<article id="t8-inject">текст</article>`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp.Text, "leak@") || strings.Contains(resp.Text, "Игнорируй") {
		t.Fatalf("injection leaked into extract: %s", resp.Text)
	}
}

func TestRedactSecretsSkipsEmptyAndRemovesKey(t *testing.T) {
	t.Setenv("LLM_API_KEY", "super-secret-test-value")
	t.Setenv("SMTP_PASSWORD", "")
	got := redactSecrets("key=super-secret-test-value tail")
	if strings.Contains(got, "super-secret-test-value") {
		t.Fatalf("secret stayed: %s", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("marker missing: %s", got)
	}
	if redactSecrets("обычный текст") != "обычный текст" {
		t.Fatal("plain text changed")
	}
}

func TestExecuteMockPassesMandatorySuite(t *testing.T) {
	ctx := context.Background()
	out := t.TempDir()
	rep, err := Execute(ctx, Options{OutDir: out, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed != rep.Total || rep.Total != 8 {
		for _, sc := range rep.Scenarios {
			if sc.Passed {
				continue
			}
			t.Log(sc.ID, sc.RunError)
			for _, fail := range sc.Failures {
				t.Log(" ", fail.Check, fail.Message)
			}
		}
		t.Fatalf("scenarios %d/%d", rep.Passed, rep.Total)
	}
	if !rep.Injections.Passed {
		t.Fatalf("injections %d/%d", rep.Injections.PassCount, rep.Injections.Total)
	}
	if rep.Injections.Total < minInjections {
		t.Fatalf("injections %d", rep.Injections.Total)
	}
	if !rep.Hard.Passed {
		t.Fatalf("hard %+v", rep.Hard.Failures)
	}
	raw, err := os.ReadFile(filepath.Join(out, "tests_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"result": "пройдена"`) {
		t.Fatal("json report has no passed scenario")
	}
	md, err := os.ReadFile(filepath.Join(out, "tests_report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "8/8 пройдено") {
		t.Fatalf("markdown summary missing: %s", firstLines(string(md), 12))
	}
	if strings.Contains(string(raw), "LLM_API_KEY") {
		t.Fatal("report names the key variable in a secret field")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
