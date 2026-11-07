package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	// maxJSONAttempts is the first call plus two repeats after a bad JSON payload.
	maxJSONAttempts = 3
	jsonInstruction = "Верни один JSON-объект и ничего больше. Без markdown и без пояснений."
	jsonFixPrefix   = "Предыдущий ответ не прошёл проверку: "
)

// Request is one chat completion.
// Temperature 0 is a real value and is sent as 0.
// Schema nil means a free-text answer. When Schema is set, the HTTP client
// asks for a JSON schema response if the server accepts it.
type Request struct {
	Task        string
	System      string
	User        string
	Schema      json.RawMessage
	Temperature float64
	MaxTokens   int
}

// Usage is token accounting for one call. Prices are applied by Meter.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is the model answer. Text is the raw message content.
type Response struct {
	Text  string
	Usage Usage
	Model string
}

// Client performs a single completion. Wrappers decorate this interface.
type Client interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// CompleteJSON calls the model and decodes one JSON value into out.
// Unknown fields are rejected. Framing around the JSON is stripped.
// A payload that does not decode is sent back up to two times with the
// validation error appended to the user message. Transport errors are not
// repeated here; wrap the client with WithRetry for that.
func CompleteJSON[T any](ctx context.Context, c Client, r Request, out *T) (Response, error) {
	if c == nil {
		return Response{}, errors.New("llm: клиент не задан")
	}
	if out == nil {
		return Response{}, errors.New("llm: некуда записать JSON")
	}
	baseUser := r.User
	r.System = appendLine(r.System, jsonInstruction)
	var last Response
	var lastErr error
	for attempt := 1; attempt <= maxJSONAttempts; attempt++ {
		req := r
		if attempt > 1 && lastErr != nil {
			req.User = baseUser + "\n\n" + jsonFixPrefix + clipRunes(lastErr.Error(), 500) + "\nВерни только исправленный JSON-объект, без лишних полей."
		}
		resp, err := c.Complete(ctx, req)
		if err != nil {
			return resp, err
		}
		last = resp
		value, err := decodeStrict[T](resp.Text)
		if err != nil {
			lastErr = err
			continue
		}
		*out = value
		return resp, nil
	}
	return last, fmt.Errorf("llm: JSON не прошёл проверку после %d попыток: %w", maxJSONAttempts, lastErr)
}

func decodeStrict[T any](raw string) (T, error) {
	var zero T
	payload := extractJSON(raw)
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	var value T
	if err := dec.Decode(&value); err != nil {
		return zero, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return zero, errors.New("после JSON есть лишние данные")
		}
		return zero, err
	}
	return value, nil
}

// extractJSON returns the first JSON object or array, without a markdown fence.
func extractJSON(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\uFEFF"))
	s = stripFence(s)
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return strings.TrimSpace(s)
	}
	return sliceBalanced(s[start:])
}

func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	rest := t[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, "json"), "JSON")
	}
	rest = strings.TrimSpace(rest)
	if j := strings.LastIndex(rest, "```"); j >= 0 {
		rest = strings.TrimSpace(rest[:j])
	}
	return rest
}

func sliceBalanced(s string) string {
	if s == "" {
		return s
	}
	open := s[0]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return s
}

func appendLine(base, line string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return line
	}
	if strings.Contains(base, line) {
		return base
	}
	return base + "\n\n" + line
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n])
}

func hasSchema(s json.RawMessage) bool {
	s = bytes.TrimSpace(s)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}
