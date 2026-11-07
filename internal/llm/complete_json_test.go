package llm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cyberdigest/internal/llm"
	"cyberdigest/internal/llm/mock"
)

func TestCompleteJSONInvalidThenOK(t *testing.T) {
	m := mock.New()
	m.Once("", "", "nope", "```json\n{\"ok\":true}\n```")
	var out struct {
		OK bool `json:"ok"`
	}
	resp, err := llm.CompleteJSON(context.Background(), m, llm.Request{Task: "classify_topic", User: "данные"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || resp.Text == "" {
		t.Fatalf("out %+v resp %+v", out, resp)
	}
	if m.Calls() != 2 {
		t.Fatalf("calls %d", m.Calls())
	}
	reqs := m.Requests()
	if !strings.Contains(reqs[1].User, "не прошёл проверку") {
		t.Fatalf("retry prompt: %s", reqs[1].User)
	}
	if !strings.Contains(reqs[0].System, "JSON") {
		t.Fatalf("system: %s", reqs[0].System)
	}
}

func TestCompleteJSONUnknownField(t *testing.T) {
	m := mock.New()
	m.When("", "", `{"ok":true,"extra":1}`)
	var out struct {
		OK bool `json:"ok"`
	}
	_, err := llm.CompleteJSON(context.Background(), m, llm.Request{User: "данные"}, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error: %v", err)
	}
	if m.Calls() != 3 {
		t.Fatalf("calls %d", m.Calls())
	}
}

func TestCompleteJSONFramedObject(t *testing.T) {
	m := mock.New()
	m.When("", "", "результат {\"ok\":true} спасибо")
	var out struct {
		OK bool `json:"ok"`
	}
	if _, err := llm.CompleteJSON(context.Background(), m, llm.Request{User: "данные"}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal(out)
	}
	if m.Calls() != 1 {
		t.Fatalf("calls %d", m.Calls())
	}
}

func TestCompleteJSONTransportError(t *testing.T) {
	m := mock.New()
	m.Fail("", "", errors.New("временный сбой"))
	var out struct {
		OK bool `json:"ok"`
	}
	_, err := llm.CompleteJSON(context.Background(), m, llm.Request{User: "данные"}, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if m.Calls() != 1 {
		t.Fatalf("calls %d", m.Calls())
	}
}
