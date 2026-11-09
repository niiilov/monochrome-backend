package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"mono/internal/llm"
)

// offlineClient is a deterministic stand-in for the model.
// Quotes for extraction are taken from the wrapped article in the prompt,
// so later stages can keep them. It does not open the network.
type offlineClient struct{}

// NewOfflineClient returns the client used by `mono run --mock`.
func NewOfflineClient() llm.Client {
	return offlineClient{}
}

func (offlineClient) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	text, err := offlineText(r.Task, r.System+"\n"+r.User)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		Text:  text,
		Model: "mock",
		Usage: llm.Usage{InputTokens: 12, OutputTokens: 8},
	}, nil
}

func offlineText(task, prompt string) (string, error) {
	switch task {
	case "classify_topic":
		return mustJSON(classifyReply(prompt)), nil
	case "extract_events":
		return mustJSON(extractReply(prompt)), nil
	case "assess_short_text":
		quote := articleSnippet(prompt)
		low := strings.ToLower(quote)
		concrete := false
		for _, marker := range []string{"атак", "утеч", "взлом", "уязвим", "похит", "зашифр", "ddos"} {
			concrete = concrete || strings.Contains(low, marker)
		}
		if !concrete {
			return mustJSON(map[string]any{"sufficient_text": false, "event": "", "quote": ""}), nil
		}
		return mustJSON(map[string]any{"sufficient_text": true, "event": quote, "quote": quote}), nil
	case "cluster_confirm":
		return mustJSON(map[string]any{
			"same_event":      false,
			"shared_entities": []string{},
			"reason":          "разные события",
			"conflicts":       []any{},
		}), nil
	case "update_check":
		return mustJSON(map[string]any{"new_facts": []string{"свежие сведения"}}), nil
	case "score_event":
		return mustJSON(scoreReply()), nil
	case "rationale":
		return mustJSON(map[string]any{"rationale": ""}), nil
	case "write_digest_item":
		return mustJSON(map[string]any{
			"title":        "Событие в источнике",
			"essence":      "Источник описывает событие. Подробности приведены в публикации.",
			"consequences": "",
			"hedge_note":   "",
		}), nil
	case "write_post":
		return mustJSON(map[string]any{"items": []any{}}), nil
	default:
		return "", fmt.Errorf("offline: нет ответа для задачи %s", task)
	}
}

func classifyReply(prompt string) map[string]any {
	low := strings.ToLower(prompt)
	ad := strings.Contains(low, "скидк") ||
		strings.Contains(low, "вебинар") ||
		strings.Contains(low, "промокод") ||
		strings.Contains(low, "специальное предложение")
	reason := "есть признаки инцидента"
	if ad {
		reason = "рекламный анонс"
	}
	return map[string]any{
		"is_on_topic":    true,
		"is_advertising": ad,
		"confidence":     0.9,
		"reason":         reason,
	}
}

func extractReply(prompt string) map[string]any {
	quote := articleSnippet(prompt)
	if quote == "" {
		return map[string]any{"mentions": []any{}}
	}
	return map[string]any{
		"mentions": []any{mentionBody(quote)},
	}
}

func mentionBody(quote string) map[string]any {
	return map[string]any{
		"title_draft":          "Событие",
		"facts":                []any{map[string]any{"text": quote, "quote": quote}},
		"affected":             "",
		"consequences":         "",
		"hedges":               []string{},
		"entities":             []string{},
		"is_company_statement": false,
		"is_reprint":           false,
		"reporter":             "",
		"references_earlier":   false,
		"new_facts":            []string{},
		"sufficient_text":      true,
	}
}

func scoreReply() map[string]any {
	empty := map[string]any{"value": nil, "quote": "", "note": "нет данных"}
	return map[string]any{
		"impact":    empty,
		"scale":     empty,
		"urgency":   empty,
		"relevance": empty,
	}
}

// articleSnippet returns a short slice of the article text inside the prompt.
// The slice stays inside one field (title or body) so the quote checker can find it.
func articleSnippet(prompt string) string {
	const open = "<article id=\""
	i := strings.LastIndex(prompt, open)
	if i < 0 {
		return ""
	}
	rest := prompt[i+len(open):]
	gt := strings.Index(rest, "\">")
	if gt < 0 {
		return ""
	}
	body := rest[gt+2:]
	if end := strings.Index(body, "</article>"); end >= 0 {
		body = body[:end]
	}
	body = html.UnescapeString(body)
	body = strings.TrimSpace(body)
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		tail := strings.TrimSpace(body[nl+1:])
		if tail != "" {
			body = tail
		} else {
			body = strings.TrimSpace(body[:nl])
		}
	}
	body = strings.Join(strings.Fields(body), " ")
	rs := []rune(body)
	if len(rs) < 20 {
		return ""
	}
	if len(rs) > 160 {
		rs = rs[:160]
	}
	return strings.TrimSpace(string(rs))
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
