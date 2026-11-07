package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"cyberdigest/internal/config"
)

// OpenAI is an HTTP client for POST {base}/v1/chat/completions
// and for OpenAI-compatible prefixes such as Gemini's /openai path.
// A base URL that already has a path gets /chat/completions appended.
// A host without a path gets /v1/chat/completions.
type OpenAI struct {
	baseURL       string
	apiKey        string
	model         string
	maxInputRunes int
	seed          int
	http          *http.Client
	schemaOK      atomic.Bool
	seedOK        atomic.Bool
	proxySecrets  []string
	proxyErr      error
}

// NewOpenAI builds an HTTP client from llm settings.
// The key may be empty here; Complete refuses to call the network without it.
func NewOpenAI(cfg config.LLM) *OpenAI {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	httpClient, secrets, proxyErr := newLLMHTTPClient(timeout)
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	c := &OpenAI{
		baseURL:       strings.TrimSpace(cfg.BaseURL),
		apiKey:        cfg.APIKey,
		model:         strings.TrimSpace(cfg.Model),
		maxInputRunes: cfg.MaxInputChars,
		seed:          cfg.Seed,
		http:          httpClient,
		proxySecrets:  secrets,
		proxyErr:      proxyErr,
	}
	c.schemaOK.Store(true)
	c.seedOK.Store(true)
	return c
}

// Complete posts one chat completion.
// When Schema is set, the first attempt uses response_format json_schema.
// A 400 that rejects the schema or the seed is retried once without that field.
func (c *OpenAI) Complete(ctx context.Context, r Request) (Response, error) {
	if c == nil {
		return Response{}, errors.New("llm: клиент не задан")
	}
	if c.proxyErr != nil {
		return Response{}, c.proxyErr
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return Response{}, errors.New("llm: не задан LLM_API_KEY")
	}
	if c.model == "" {
		return Response{}, errors.New("llm: не задана модель (LLM_MODEL)")
	}
	if c.baseURL == "" {
		return Response{}, errors.New("llm: не задан базовый URL (LLM_BASE_URL)")
	}
	if c.maxInputRunes > 0 {
		n := utf8.RuneCountInString(r.System) + utf8.RuneCountInString(r.User)
		if n > c.maxInputRunes {
			return Response{}, fmt.Errorf("llm: промпт длиннее llm.max_input_chars (%d > %d)", n, c.maxInputRunes)
		}
	}
	useSchema := hasSchema(r.Schema) && c.schemaOK.Load()
	useSeed := c.seedOK.Load()
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		status, body, retryAfter, hasAfter, err := c.post(ctx, r, useSchema, useSeed)
		if err != nil {
			return Response{}, err
		}
		if status >= 200 && status < 300 {
			return decodeChat(body, c.model, c.apiKey)
		}
		msg := clipRunes(redactSecret(squash(string(body)), c.apiKey), 400)
		last = &StatusError{Status: status, Body: msg, RetryAfter: retryAfter, HasRetryAfter: hasAfter}
		if status == http.StatusBadRequest && useSchema && schemaRejected(msg) {
			c.schemaOK.Store(false)
			useSchema = false
			continue
		}
		if status == http.StatusBadRequest && useSeed && seedRejected(msg) {
			c.seedOK.Store(false)
			useSeed = false
			continue
		}
		return Response{}, last
	}
	if last == nil {
		last = errors.New("llm: не удалось согласовать формат запроса")
	}
	return Response{}, last
}

func (c *OpenAI) post(ctx context.Context, r Request, useSchema, useSeed bool) (int, []byte, time.Duration, bool, error) {
	payload, err := c.payload(r, useSchema, useSeed)
	if err != nil {
		return 0, nil, 0, false, err
	}
	endpoint := chatCompletionsURL(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cyberdigest/0.1")
	applyAuth(req, c.baseURL, c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, 0, false, c.redactErr(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, 0, false, c.redactErr(err)
	}
	after, hasAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	return resp.StatusCode, body, after, hasAfter, nil
}

func (c *OpenAI) payload(r Request, useSchema, useSeed bool) ([]byte, error) {
	system := r.System
	if hasSchema(r.Schema) && !useSchema {
		system = appendLine(system, "Верни один JSON-объект по схеме. Без markdown и без пояснений.")
	}
	var messages []chatMessage
	if strings.TrimSpace(system) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, chatMessage{Role: "user", Content: r.User})
	body := chatRequest{
		Model:       c.model,
		Temperature: r.Temperature,
		Messages:    messages,
	}
	if useSeed {
		seed := c.seed
		body.Seed = &seed
	}
	if r.MaxTokens > 0 {
		body.MaxTokens = r.MaxTokens
	}
	if useSchema {
		body.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchemaBody{
				Name:   "result",
				Strict: true,
				Schema: r.Schema,
			},
		}
	}
	return json.Marshal(body)
}

func applyAuth(req *http.Request, baseURL, key string) {
	req.Header.Set("Authorization", "Bearer "+key)
	if strings.Contains(strings.ToLower(baseURL), "googleapis.com") {
		req.Header.Set("x-goog-api-key", key)
	}
}

func chatCompletionsURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.ToLower(base), "/chat/completions") {
		return base
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		if strings.HasSuffix(base, "/v1") {
			return base + "/chat/completions"
		}
		return base + "/v1/chat/completions"
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/v1/chat/completions"
	} else {
		u.Path = path + "/chat/completions"
	}
	return u.String()
}

func decodeChat(body []byte, fallbackModel, secret string) (Response, error) {
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Response{}, fmt.Errorf("llm: ответ сервера не JSON: %w", err)
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return Response{}, errors.New("llm: " + clipRunes(redactSecret(squash(parsed.Error.Message), secret), 400))
	}
	if len(parsed.Choices) == 0 {
		return Response{}, errors.New("llm: в ответе нет choices")
	}
	text, err := decodeContent(parsed.Choices[0].Message.Content)
	if err != nil {
		return Response{}, fmt.Errorf("llm: не разобран content: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		reason := parsed.Choices[0].FinishReason
		if reason == "" {
			reason = "unknown"
		}
		return Response{}, fmt.Errorf("llm: пустой ответ модели (finish_reason=%s)", reason)
	}
	model := parsed.Model
	if model == "" {
		model = fallbackModel
	}
	in := parsed.Usage.PromptTokens
	if in == 0 {
		in = parsed.Usage.InputTokens
	}
	out := parsed.Usage.CompletionTokens
	if out == 0 {
		out = parsed.Usage.OutputTokens
	}
	return Response{Text: text, Model: model, Usage: Usage{InputTokens: in, OutputTokens: out}}, nil
}

func decodeContent(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

func schemaRejected(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "json_schema") || strings.Contains(low, "response_format") || strings.Contains(low, "response format")
}

func seedRejected(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "seed")
}

func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if sec, err := strconv.Atoi(v); err == nil {
		if sec < 0 {
			sec = 0
		}
		return time.Duration(sec) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func redactSecret(s, secret string) string {
	if secret == "" || s == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

func (c *OpenAI) redactErr(err error) error {
	if err == nil || c == nil {
		return err
	}
	msg := redactSecret(err.Error(), c.apiKey)
	for _, secret := range c.proxySecrets {
		msg = redactSecret(msg, secret)
	}
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func squash(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Temperature    float64         `json:"temperature"`
	Messages       []chatMessage   `json:"messages"`
	Seed           *int            `json:"seed,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaBody `json:"json_schema,omitempty"`
}

type jsonSchemaBody struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}
