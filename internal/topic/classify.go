package topic

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/guard"
	"cyberdigest/internal/journal"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/normalize"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/textutil"
)

const (
	taskClassify = "classify_topic"
	// promptReserve leaves room for the system prompt and the article wrapper.
	// The HTTP client counts system and user runes together against max_input_chars.
	promptReserve  = 2000
	maxReasonRunes = 500
	maxTokens      = 400
)

// classifySchema is the JSON schema sent to a client that supports response_format.
var classifySchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["is_on_topic", "is_advertising", "confidence", "reason"],
  "properties": {
    "is_on_topic": {"type": "boolean"},
    "is_advertising": {"type": "boolean"},
    "confidence": {"type": "number"},
    "reason": {"type": "string"}
  }
}`)

type modelVerdict struct {
	IsOnTopic     bool    `json:"is_on_topic"`
	IsAdvertising bool    `json:"is_advertising"`
	Confidence    float64 `json:"confidence"`
	Reason        string  `json:"reason"`
}

type promptData struct {
	Matches []Match
	Article string
}

// Decision is the code's include-or-exclude result.
// Pass means the article continues. Otherwise Code is off_topic or advertising.
type Decision struct {
	Pass   bool
	Code   domain.ReasonCode
	Reason string
}

// Decide applies the include rule. Advertising wins over a positive topic flag.
// The model reason text cannot override the booleans.
func Decide(res domain.TopicResult) Decision {
	if res.IsAdvertising {
		return Decision{
			Code:   domain.ReasonAdvertising,
			Reason: journal.Text(domain.ReasonAdvertising, journal.Data{}),
		}
	}
	if !res.IsOnTopic {
		return Decision{
			Code:   domain.ReasonOffTopic,
			Reason: journal.Text(domain.ReasonOffTopic, journal.Data{ModelReason: res.Reason}),
		}
	}
	return Decision{Pass: true}
}

// Classify asks the model unless the dictionary and the title have no topic signs.
// Article text is clipped with normalize.Chunks and wrapped with guard.WrapUntrusted.
// MatchedTerms come from the dictionary, not from the model.
func Classify(ctx context.Context, c llm.Client, a domain.Article) (domain.TopicResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hits := Scan(a.Title, a.Text)
	// Title and text are both scanned. No hit means the title has no topic sign either.
	if len(hits) == 0 {
		return domain.TopicResult{
			IsOnTopic:    false,
			Confidence:   0,
			MatchedTerms: nil,
			Reason:       "В заголовке и тексте нет терминов информационной безопасности",
		}, nil
	}
	if c == nil {
		return domain.TopicResult{}, errors.New("topic: клиент не задан")
	}
	body := clipText(a.Text, textBudget())
	system, user, _, err := prompts.Render("classify_topic", promptData{
		Matches: hits,
		Article: guard.WrapUntrusted(a.ID, a.Title, body),
	})
	if err != nil {
		return domain.TopicResult{}, err
	}
	var verdict modelVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskClassify,
		System:      system,
		User:        user,
		Schema:      classifySchema,
		Temperature: 0,
		MaxTokens:   maxTokens,
	}, &verdict)
	if err != nil {
		return domain.TopicResult{}, err
	}
	terms := make([]string, len(hits))
	for i, hit := range hits {
		terms[i] = hit.Term
	}
	return domain.TopicResult{
		IsOnTopic:     verdict.IsOnTopic,
		IsAdvertising: verdict.IsAdvertising,
		Confidence:    clampConfidence(verdict.Confidence),
		MatchedTerms:  terms,
		Reason:        cleanReason(verdict.Reason),
	}, nil
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

func clipText(text string, budget int) string {
	parts := normalize.Chunks(text, budget)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func clampConfidence(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func cleanReason(s string) string {
	s = textutil.ReplaceLongDash(s)
	if utf8.RuneCountInString(s) <= maxReasonRunes {
		return s
	}
	return string([]rune(s)[:maxReasonRunes])
}
