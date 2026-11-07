package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberdigest/internal/config"
)

func TestChatCompletionsURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://example.test/v1/chat/completions", "https://example.test/v1/chat/completions"},
	}
	for _, tc := range cases {
		if got := chatCompletionsURL(tc.in); got != tc.want {
			t.Errorf("chatCompletionsURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestApplyAuthGeminiHeader(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyAuth(req, "https://generativelanguage.googleapis.com/v1beta/openai", "k")
	if req.Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("bearer: %s", req.Header.Get("Authorization"))
	}
	if req.Header.Get("x-goog-api-key") != "k" {
		t.Fatal("gemini header missing")
	}
	req2, _ := http.NewRequest(http.MethodPost, "https://example.test", nil)
	applyAuth(req2, "https://api.openai.com/v1", "k")
	if req2.Header.Get("x-goog-api-key") != "" {
		t.Fatal("openai host must not get the google header")
	}
}

func TestCompleteOK(t *testing.T) {
	var saw atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer unit-secret" {
			t.Errorf("auth header leaked or missing")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "test-model" {
			t.Errorf("model %#v", body["model"])
		}
		if _, ok := body["seed"]; !ok {
			t.Error("seed was not sent")
		}
		writeOK(w, "pong", 5, 1)
	}), config.LLM{APIKey: "unit-secret", Model: "test-model", Seed: 7})
	resp, err := c.Complete(context.Background(), Request{Task: "llm_ping", User: "ping", MaxTokens: 16})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "pong" || resp.Model != "test-model" || resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 1 {
		t.Fatalf("%+v", resp)
	}
	if saw.Load() != 1 {
		t.Fatalf("calls %d", saw.Load())
	}
}

func TestCompleteContentParts(t *testing.T) {
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m2","choices":[{"message":{"content":[{"type":"text","text":"po"},{"type":"text","text":"ng"}]}}],"usage":{"input_tokens":4,"output_tokens":1}}`)
	}), config.LLM{APIKey: "k", Model: "m"})
	resp, err := c.Complete(context.Background(), Request{User: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "pong" || resp.Model != "m2" || resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 1 {
		t.Fatalf("%+v", resp)
	}
}

func TestSchemaFallback(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n.Add(1)
		if bytesContains(raw, "json_schema") {
			http.Error(w, `{"error":{"message":"response_format json_schema is not supported"}}`, http.StatusBadRequest)
			return
		}
		writeOK(w, `{"ok":true}`, 3, 2)
	}), config.LLM{APIKey: "k", Model: "m", Seed: 1})
	resp, err := c.Complete(context.Background(), Request{
		User:   "ping",
		Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"ok":true}` || n.Load() != 2 {
		t.Fatalf("text %q calls %d", resp.Text, n.Load())
	}
}

func TestSeedFallback(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n.Add(1)
		if bytesContains(raw, `"seed"`) {
			http.Error(w, `{"error":{"message":"unknown field seed"}}`, http.StatusBadRequest)
			return
		}
		writeOK(w, "pong", 1, 1)
	}), config.LLM{APIKey: "k", Model: "m", Seed: 7})
	if _, err := c.Complete(context.Background(), Request{User: "ping"}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestRetryOn500(t *testing.T) {
	var n atomic.Int32
	c, srv := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			http.Error(w, `{"error":{"message":"unavailable"}}`, http.StatusInternalServerError)
			return
		}
		writeOK(w, "ok", 2, 2)
	}), config.LLM{APIKey: "k", Model: "m"})
	_ = srv
	wrapped := WithRetry(c, RetryOptions{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	resp, err := wrapped.Complete(context.Background(), Request{User: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || n.Load() != 3 {
		t.Fatalf("text %q calls %d", resp.Text, n.Load())
	}
}

func TestRetryOn429(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":{"message":"slow down"}}`, http.StatusTooManyRequests)
			return
		}
		writeOK(w, "ok", 1, 1)
	}), config.LLM{APIKey: "k", Model: "m"})
	wrapped := WithRetry(c, RetryOptions{MaxRetries: 1, BaseDelay: time.Second, MaxDelay: time.Second})
	start := time.Now()
	if _, err := wrapped.Complete(context.Background(), Request{User: "ping"}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("calls %d", n.Load())
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("Retry-After 0 slept too long: %s", time.Since(start))
	}
}

func TestNoRetryOn400(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Error(w, `{"error":{"message":"bad request model"}}`, http.StatusBadRequest)
	}), config.LLM{APIKey: "k", Model: "m", Seed: 3})
	wrapped := WithRetry(c, RetryOptions{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	_, err := wrapped.Complete(context.Background(), Request{User: "ping"})
	if err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 1 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestRetryOnTimeout(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond)
			return
		}
		writeOK(w, "pong", 1, 1)
	}), config.LLM{APIKey: "k", Model: "m"})
	c.http = &http.Client{Timeout: 40 * time.Millisecond}
	wrapped := WithRetry(c, RetryOptions{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	resp, err := wrapped.Complete(context.Background(), Request{User: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "pong" || n.Load() < 2 {
		t.Fatalf("text %q calls %d", resp.Text, n.Load())
	}
}

func TestRedactKeyInError(t *testing.T) {
	const key = "unit-secret-xyz"
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad `+r.Header.Get("Authorization")+`"}}`, http.StatusBadRequest)
	}), config.LLM{APIKey: key, Model: "m"})
	_, err := c.Complete(context.Background(), Request{User: "ping"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("key leaked: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redaction marker missing: %s", err.Error())
	}
}

func TestEmptyContentReportsFinishReason(t *testing.T) {
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"prompt_tokens":1,"completion_tokens":16}}`)
	}), config.LLM{APIKey: "k", Model: "m"})
	_, err := c.Complete(context.Background(), Request{User: "ping"})
	if err == nil || !strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("error: %v", err)
	}
}

func TestMaxInputChars(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		writeOK(w, "ok", 1, 1)
	}), config.LLM{APIKey: "k", Model: "m", MaxInputChars: 8})
	_, err := c.Complete(context.Background(), Request{User: "123456789"})
	if err == nil {
		t.Fatal("expected length error")
	}
	if n.Load() != 0 {
		t.Fatal("request was sent")
	}
}

func TestMissingKeyDoesNotCall(t *testing.T) {
	var n atomic.Int32
	c, _ := newHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
	}), config.LLM{Model: "m"})
	c.apiKey = ""
	if _, err := c.Complete(context.Background(), Request{User: "ping"}); err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 0 {
		t.Fatal("request was sent")
	}
}

func TestNewRequiresSettings(t *testing.T) {
	if _, _, err := New(config.LLM{}); err == nil {
		t.Fatal("expected error")
	}
}

func newHTTPClient(t *testing.T, h http.Handler, cfg config.LLM) (*OpenAI, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	return NewOpenAI(cfg), srv
}

func writeOK(w http.ResponseWriter, content string, in, out int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model": "test-model",
		"choices": []any{
			map[string]any{"message": map[string]any{"role": "assistant", "content": content}},
		},
		"usage": map[string]any{"prompt_tokens": in, "completion_tokens": out},
	})
}

func bytesContains(b []byte, sub string) bool {
	return strings.Contains(string(b), sub)
}
