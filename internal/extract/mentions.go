package extract

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/guard"
	"mono/internal/llm"
	"mono/internal/normalize"
	"mono/internal/prompts"
	"mono/internal/textutil"
)

const (
	taskExtract = "extract_events"
	// promptReserve leaves room for the system prompt and the article wrapper.
	promptReserve  = 2000
	maxReasonRunes = 500
	maxTokens      = 2500
)

// extractSchema is sent when the client supports response_format.
var extractSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["mentions"],
  "properties": {
    "mentions": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["title_draft", "facts", "affected", "consequences", "hedges", "entities", "is_company_statement", "is_reprint", "reporter", "references_earlier", "new_facts", "sufficient_text"],
        "properties": {
          "title_draft": {"type": "string"},
          "facts": {
            "type": "array",
            "items": {
              "type": "object",
              "additionalProperties": false,
              "required": ["text", "quote"],
              "properties": {
                "text": {"type": "string"},
                "quote": {"type": "string"}
              }
            }
          },
          "affected": {"type": "string"},
          "consequences": {"type": "string"},
          "hedges": {"type": "array", "items": {"type": "string"}},
          "entities": {"type": "array", "items": {"type": "string"}},
          "is_company_statement": {"type": "boolean"},
          "is_reprint": {"type": "boolean"},
          "reporter": {"type": "string"},
          "references_earlier": {"type": "boolean"},
          "new_facts": {"type": "array", "items": {"type": "string"}},
          "sufficient_text": {"type": "boolean"}
        }
      }
    }
  }
}`)

type modelFact struct {
	Text  string `json:"text"`
	Quote string `json:"quote"`
}

type modelMention struct {
	TitleDraft         string      `json:"title_draft"`
	Facts              []modelFact `json:"facts"`
	Affected           string      `json:"affected"`
	Consequences       string      `json:"consequences"`
	Hedges             []string    `json:"hedges"`
	Entities           []string    `json:"entities"`
	IsCompanyStatement bool        `json:"is_company_statement"`
	IsReprint          bool        `json:"is_reprint"`
	Reporter           string      `json:"reporter"`
	ReferencesEarlier  bool        `json:"references_earlier"`
	NewFacts           []string    `json:"new_facts"`
	SufficientText     bool        `json:"sufficient_text"`
}

type modelResponse struct {
	Mentions []modelMention `json:"mentions"`
}

type promptData struct {
	Source  string
	Article string
}

// Mentions extracts 1..N event mentions from one article.
// A fact whose quote is not a substring of the title or text (after space
// normalization) is dropped, and a trace note is written when ctx carries a Noter.
// SufficientText is forced off when EnoughText rejects the article.
func Mentions(ctx context.Context, c llm.Client, a domain.Article) ([]domain.Mention, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(a.ID) == "" {
		return nil, errors.New("extract: у статьи нет id")
	}
	if c == nil {
		return nil, errors.New("extract: клиент не задан")
	}
	body := clipArticle(a.Text, textBudget())
	system, user, _, err := prompts.Render("extract_events", promptData{
		Source:  strings.TrimSpace(a.Source),
		Article: guard.WrapUntrusted(a.ID, a.Title, body),
	})
	if err != nil {
		return nil, err
	}
	var parsed modelResponse
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskExtract,
		System:      system,
		User:        user,
		Schema:      extractSchema,
		Temperature: 0,
		MaxTokens:   maxTokens,
	}, &parsed)
	if err != nil {
		return nil, err
	}
	enough := EnoughText(a.Text, config.Default().Ingest.MinTextChars)
	out := make([]domain.Mention, 0, len(parsed.Mentions))
	n := 0
	for _, raw := range parsed.Mentions {
		m, notes := sanitize(a, raw, enough)
		for _, note := range notes {
			Note(ctx, stageExtract, a.ID, note)
		}
		if m == nil {
			continue
		}
		n++
		m.ID = "M-" + a.ID + "-" + itoa(n)
		m.ArticleID = a.ID
		out = append(out, *m)
	}
	return out, nil
}

func sanitize(a domain.Article, raw modelMention, enough bool) (*domain.Mention, []string) {
	var notes []string
	facts := make([]domain.Fact, 0, len(raw.Facts))
	for _, fact := range raw.Facts {
		quote := strings.TrimSpace(fact.Quote)
		if quote == "" {
			notes = append(notes, "Факт без цитаты отброшен.")
			continue
		}
		exact, ok := findQuote(a.Text, a.Title, quote)
		if !ok {
			notes = append(notes, "Цитата не найдена в тексте статьи, факт отброшен: «"+clip(oneLine(quote), 80)+"».")
			continue
		}
		facts = append(facts, domain.Fact{
			Text:      oneLine(fact.Text),
			Quote:     exact,
			ArticleID: a.ID,
		})
	}
	if len(facts) == 0 {
		notes = append(notes, "Упоминание отброшено: не осталось фактов с дословной цитатой.")
		return nil, notes
	}
	sufficient := raw.SufficientText && enough
	if raw.SufficientText && !enough {
		notes = append(notes, "Модель посчитала текст достаточным, но код оставил недостаточно: мало знаков или предложений.")
	}
	hedges, hedgeNotes := verbatimList(a, raw.Hedges, "Оговорка не найдена в тексте, отброшена")
	notes = append(notes, hedgeNotes...)
	m := &domain.Mention{
		TitleDraft:         oneLine(raw.TitleDraft),
		Facts:              facts,
		Affected:           oneLine(raw.Affected),
		Consequences:       oneLine(raw.Consequences),
		Hedges:             hedges,
		Entities:           cleanList(raw.Entities),
		IsCompanyStatement: raw.IsCompanyStatement,
		IsReprint:          raw.IsReprint,
		Reporter:           oneLine(raw.Reporter),
		ReferencesEarlier:  raw.ReferencesEarlier,
		NewFacts:           cleanList(raw.NewFacts),
		SufficientText:     sufficient,
	}
	return m, notes
}

// verbatimList keeps items that are substrings of the article and stores the original span.
func verbatimList(a domain.Article, items []string, note string) ([]string, []string) {
	if len(items) == 0 {
		return nil, nil
	}
	var kept, notes []string
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		exact, ok := findQuote(a.Text, a.Title, item)
		if !ok {
			notes = append(notes, note+": «"+clip(oneLine(item), 80)+"».")
			continue
		}
		if _, dup := seen[exact]; dup {
			continue
		}
		seen[exact] = struct{}{}
		kept = append(kept, exact)
	}
	return kept, notes
}

func cleanList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = oneLine(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func oneLine(s string) string {
	s = textutil.ReplaceLongDash(s)
	s = strings.Join(strings.Fields(s), " ")
	return clipRunes(s, maxReasonRunes)
}

func textBudget() int {
	limit := config.Default().LLM.MaxInputChars
	if limit <= 0 {
		limit = 24000
	}
	if limit <= promptReserve+500 {
		return limit
	}
	return limit - promptReserve
}

func clipArticle(text string, budget int) string {
	parts := normalize.Chunks(text, budget)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
