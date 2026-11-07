package rank

import (
	"encoding/json"
	"strings"

	"cyberdigest/internal/domain"
)

const (
	impactQuote    = "Утекли персональные данные тысячи клиентов"
	scaleQuote     = "Затронуты три банка"
	urgencyQuote   = "Патч не выпущен"
	relevanceQuote = "Организации финансового сектора в России"
)

func ip(n int) *int {
	v := n
	return &v
}

func testEvent(id, articleID, _ string) domain.Event {
	return domain.Event{
		ID:               id,
		PrimaryArticleID: articleID,
		ArticleIDs:       []string{articleID},
		Mentions: []domain.Mention{{
			ID:           "M-" + articleID + "-1",
			ArticleID:    articleID,
			Consequences: impactQuote,
			Affected:     "три банка",
			Facts: []domain.Fact{
				{Text: impactQuote, Quote: impactQuote, ArticleID: articleID},
				{Text: urgencyQuote, Quote: urgencyQuote, ArticleID: articleID},
			},
		}},
	}
}

func artsOf(events []domain.Event, text string) map[string]domain.Article {
	out := map[string]domain.Article{}
	for _, ev := range events {
		for _, id := range ev.ArticleIDs {
			out[id] = domain.Article{ID: id, Title: "Новость", Text: text}
		}
	}
	return out
}

func scorePayload(impact, scale, urgency, relevance *int, iq, sq, uq, rq string) string {
	body := map[string]any{
		"impact":    crit(impact, iq),
		"scale":     crit(scale, sq),
		"urgency":   crit(urgency, uq),
		"relevance": crit(relevance, rq),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func crit(v *int, quote string) map[string]any {
	note := ""
	if v == nil {
		note = NoDataNote
		quote = ""
	}
	return map[string]any{"value": v, "quote": quote, "note": note}
}

func fullScore() string {
	return scorePayload(ip(3), ip(2), ip(3), ip(2), impactQuote, scaleQuote, urgencyQuote, relevanceQuote)
}

func ratSentence() string {
	return rat("Последствия: утекли персональные данные тысячи клиентов; масштаб: три банка; срочность: патч не выпущен.")
}

func rat(s string) string {
	raw, err := json.Marshal(map[string]string{"rationale": s})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func traceHas(row domain.JournalEntry, part string) bool {
	for _, note := range row.Trace {
		if note.Stage == stageRank && strings.Contains(note.Note, part) {
			return true
		}
	}
	return false
}
