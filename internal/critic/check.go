package critic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mono/internal/domain"
	"mono/internal/guard"
	"mono/internal/llm"
	"mono/internal/prompts"
	"mono/internal/textutil"
)

const (
	// Task is the llm accounting name for critic_check.
	Task = "critic_check"

	VerdictSupported   = "supported"
	VerdictUnsupported = "unsupported"
	VerdictUncertain   = "uncertain"
)

const schemaJSON = `{"type":"object","additionalProperties":false,"required":["claims"],"properties":{"claims":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["text","verdict","quote","reason"],"properties":{"text":{"type":"string"},"verdict":{"type":"string"},"quote":{"type":"string"},"reason":{"type":"string"}}}}}}`

// Claim is one assertion from a digest retelling.
type Claim struct {
	Text    string `json:"text"`
	Verdict string `json:"verdict"`
	Quote   string `json:"quote"`
	Reason  string `json:"reason"`
}

// Report is the critic answer for one digest item.
type Report struct {
	Claims []Claim `json:"claims"`
}

type promptSource struct {
	ArticleID string
	Name      string
	Body      string
}

type promptData struct {
	EventID      string
	Title        string
	Essence      string
	Consequences string
	Sources      []promptSource
}

type rawAnswer struct {
	Claims []Claim `json:"claims"`
}

// Check asks the model to judge every claim in the digest item against sources.
// The prompt is separate from write_digest_item on purpose.
func Check(ctx context.Context, c llm.Client, item domain.DigestItem, arts map[string]domain.Article) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if c == nil {
		return Report{}, errors.New("critic: клиент LLM не задан")
	}
	if arts == nil {
		arts = map[string]domain.Article{}
	}
	view := buildPrompt(item, arts)
	system, user, _, err := prompts.Render("critic_check", view)
	if err != nil {
		return Report{}, fmt.Errorf("critic: промпт события %s: %w", item.EventID, err)
	}
	var parsed rawAnswer
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        Task,
		System:      system,
		User:        user,
		Schema:      json.RawMessage(schemaJSON),
		Temperature: 0,
		MaxTokens:   2048,
	}, &parsed)
	if err != nil {
		return Report{}, fmt.Errorf("critic: событие %s: %w", item.EventID, err)
	}
	return normalize(parsed), nil
}

func buildPrompt(item domain.DigestItem, arts map[string]domain.Article) promptData {
	seen := map[string]bool{}
	var sources []promptSource
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		a, ok := arts[id]
		if !ok {
			return
		}
		seen[id] = true
		body := strings.TrimSpace(a.Text)
		if body == "" {
			body = strings.TrimSpace(a.RawText)
		}
		sources = append(sources, promptSource{
			ArticleID: id,
			Name:      a.Source,
			Body:      guard.WrapUntrusted(a.ID, a.Title, body),
		})
	}
	for _, src := range item.Sources {
		add(src.ArticleID)
	}
	if len(sources) == 0 {
		sources = append(sources, promptSource{
			ArticleID: "none",
			Name:      "нет источника",
			Body:      guard.WrapUntrusted("none", "нет источника", "Текст источника отсутствует."),
		})
	}
	return promptData{
		EventID:      item.EventID,
		Title:        item.Title,
		Essence:      item.Essence,
		Consequences: item.Consequences,
		Sources:      sources,
	}
}

func normalize(raw rawAnswer) Report {
	out := Report{Claims: make([]Claim, 0, len(raw.Claims))}
	for _, c := range raw.Claims {
		text := strings.TrimSpace(textutil.ReplaceLongDash(c.Text))
		if text == "" {
			continue
		}
		verdict := strings.ToLower(strings.TrimSpace(c.Verdict))
		switch verdict {
		case VerdictSupported, VerdictUnsupported, VerdictUncertain:
		default:
			verdict = VerdictUncertain
		}
		out.Claims = append(out.Claims, Claim{
			Text:    text,
			Verdict: verdict,
			Quote:   strings.TrimSpace(textutil.ReplaceLongDash(c.Quote)),
			Reason:  strings.TrimSpace(textutil.ReplaceLongDash(c.Reason)),
		})
	}
	if out.Claims == nil {
		out.Claims = []Claim{}
	}
	return out
}

// Unsupported returns claims marked unsupported.
func (r Report) Unsupported() []Claim {
	var out []Claim
	for _, c := range r.Claims {
		if c.Verdict == VerdictUnsupported {
			out = append(out, c)
		}
	}
	return out
}
