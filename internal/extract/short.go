package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mono/internal/domain"
	"mono/internal/guard"
	"mono/internal/llm"
	"mono/internal/prompts"
)

const taskShortText = "assess_short_text"

var shortTextSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["sufficient_text", "event", "quote"],
  "properties": {
    "sufficient_text": {"type": "boolean"},
    "event": {"type": "string"},
    "quote": {"type": "string"}
  }
}`)

type shortTextVerdict struct {
	SufficientText bool   `json:"sufficient_text"`
	Event          string `json:"event"`
	Quote          string `json:"quote"`
}

type shortTextPrompt struct {
	Candidate string
	Article   string
}

func reviewShortText(ctx context.Context, c llm.Client, a domain.Article, mention domain.Mention) (bool, error) {
	candidate, err := json.Marshal(struct {
		Event string        `json:"event"`
		Facts []domain.Fact `json:"facts"`
	}{mention.TitleDraft, mention.Facts})
	if err != nil {
		return false, err
	}
	system, user, _, err := prompts.Render(taskShortText, shortTextPrompt{
		Candidate: guard.WrapUntrusted("candidate", "", string(candidate)),
		Article:   guard.WrapUntrusted(a.ID, a.Title, a.Text),
	})
	if err != nil {
		return false, err
	}
	var verdict shortTextVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task: taskShortText, System: system, User: user, Schema: shortTextSchema,
		Temperature: 0, MaxTokens: 500,
	}, &verdict)
	if err != nil {
		return false, fmt.Errorf("проверка короткого текста: %w", err)
	}
	if !verdict.SufficientText || strings.TrimSpace(verdict.Event) == "" || strings.TrimSpace(verdict.Quote) == "" {
		return false, nil
	}
	// Evidence must come from the body and support an already verified fact.
	if _, ok := findQuote(a.Text, "", verdict.Quote); !ok {
		return false, nil
	}
	for _, fact := range mention.Facts {
		if _, ok := findQuote(fact.Quote, "", verdict.Quote); ok {
			return true, nil
		}
	}
	return false, nil
}
