package verify

import (
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/summarize"
	"cyberdigest/internal/textutil"
)

const noRationale = "Обоснование отбора в данных события не приведено."

// templateShorts copies the digest fields into the post so a failed model
// call cannot add a number, a link or a quote that the digest does not have.
func templateShorts(m domain.DigestModel) []summarize.ShortItem {
	out := make([]summarize.ShortItem, 0, len(m.Items))
	for i, item := range m.Items {
		parts := []string{strings.TrimSpace(item.Essence)}
		if consequences := strings.TrimSpace(item.Consequences); consequences != "" && consequences != summarize.NoConsequences {
			parts = append(parts, consequences)
		}
		if rationale := strings.TrimSpace(item.Rationale); rationale != "" {
			parts = append(parts, rationale)
		}
		n := item.N
		if n <= 0 {
			n = i + 1
		}
		out = append(out, summarize.ShortItem{
			N:     n,
			Title: item.Title,
			Text:  strings.TrimSpace(strings.Join(parts, " ")),
		})
	}
	return out
}

func degradeEvents(m domain.DigestModel, events []domain.Event, arts map[string]domain.Article, ids []string, useFacts bool) (domain.DigestModel, bool) {
	changed := false
	if len(m.Items) > 5 {
		m.Items = append([]domain.DigestItem(nil), m.Items[:5]...)
		changed = true
	}
	want := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		for _, item := range m.Items {
			want[item.EventID] = true
		}
	}
	byID := eventByID(events)
	for i, item := range m.Items {
		if !want[item.EventID] {
			continue
		}
		event := byID[item.EventID]
		if event.ID == "" {
			event.ID = item.EventID
			for _, source := range item.Sources {
				event.ArticleIDs = append(event.ArticleIDs, source.ArticleID)
			}
		}
		next := degradeItem(item, event, arts, useFacts)
		next.N = item.N
		if next.N <= 0 {
			next.N = i + 1
		}
		m.Items[i] = next
		changed = true
	}
	for i := range m.Items {
		m.Items[i].N = i + 1
	}
	return m, changed
}

func degradeItem(item domain.DigestItem, event domain.Event, arts map[string]domain.Article, useFacts bool) domain.DigestItem {
	articles := articlesFor(item, event, arts)
	body := corpusOf(articles)
	urls := eventURLs(articles)
	next := item
	if refs := summarize.SourceRefs(event, arts); len(refs) > 0 {
		next.Sources = refs
	}
	if useFacts {
		next.Title = pickTitle(event, body, urls)
		next.Essence = pickEssence(event, body, urls)
		next.Consequences = pickConsequences(event, body, urls)
		next.Rationale = pickRationale(event, body, urls)
	} else {
		next.Title = "Событие"
		next.Essence = "Источник описывает событие."
		next.Consequences = summarize.NoConsequences
		next.Rationale = noRationale
	}
	next.Essence = applyHedges(next.Essence, event, body, urls)
	if strings.TrimSpace(next.Title) == "" {
		next.Title = "Событие"
	}
	if strings.TrimSpace(next.Essence) == "" {
		next.Essence = "Источник описывает событие."
	}
	if strings.TrimSpace(next.Consequences) == "" {
		next.Consequences = summarize.NoConsequences
	}
	if strings.TrimSpace(next.Rationale) == "" {
		next.Rationale = noRationale
	}
	return next
}

func pickTitle(event domain.Event, body string, urls map[string]bool) string {
	for _, mention := range event.Mentions {
		if title, ok := cleanCandidate(mention.TitleDraft, body, urls); ok {
			return cutLine(title, 180)
		}
	}
	for _, fact := range factsOf(event) {
		if title, ok := cleanCandidate(fact.Text, body, urls); ok {
			return cutLine(firstSentence(title), 180)
		}
	}
	return "Событие"
}

func pickEssence(event domain.Event, body string, urls map[string]bool) string {
	var parts []string
	for _, fact := range factsOf(event) {
		text, ok := cleanCandidate(fact.Text, body, urls)
		if !ok {
			continue
		}
		parts = append(parts, finishPeriod(text))
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func pickConsequences(event domain.Event, body string, urls map[string]bool) string {
	for _, mention := range event.Mentions {
		text, ok := cleanCandidate(mention.Consequences, body, urls)
		if ok {
			return finishPeriod(text)
		}
	}
	return summarize.NoConsequences
}

func pickRationale(event domain.Event, body string, urls map[string]bool) string {
	if text, ok := cleanCandidate(event.Rationale, body, urls); ok {
		return finishPeriod(text)
	}
	return noRationale
}

func applyHedges(essence string, event domain.Event, body string, urls map[string]bool) string {
	for _, hedge := range hedgeGaps(eventHedges(event), essence) {
		line := hedgeFallback(hedge)
		if cleaned, ok := cleanCandidate(line, body, urls); ok {
			essence = strings.TrimSpace(essence + " " + finishPeriod(cleaned))
			continue
		}
		essence = strings.TrimSpace(essence + " По данным источника.")
	}
	return finishPeriod(essence)
}

func cleanCandidate(text, corpus string, urls map[string]bool) (string, bool) {
	text = foldWS(textutil.ReplaceLongDash(text))
	if text == "" || stopPhrase(text) != "" {
		return "", false
	}
	allowed := numberSet(corpus)
	for _, number := range textutil.ExtractNumbers(text) {
		if !allowed[number] {
			return "", false
		}
	}
	for _, raw := range textutil.ExtractURLs(text) {
		if !urlKnown(raw, urls) {
			return "", false
		}
	}
	for _, quote := range quoteSpans(text) {
		if !quoteInCorpus(quote, corpus) {
			return "", false
		}
	}
	return text, true
}

func factsOf(event domain.Event) []domain.Fact {
	var out []domain.Fact
	for _, mention := range event.Mentions {
		out = append(out, mention.Facts...)
	}
	return out
}

func firstSentence(text string) string {
	parts := splitSentences(text)
	if len(parts) == 0 {
		return strings.TrimSpace(text)
	}
	return parts[0]
}

func cutLine(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	rs = rs[:n]
	for i := len(rs) - 1; i > 0; i-- {
		if rs[i] == ' ' {
			return strings.TrimSpace(string(rs[:i]))
		}
	}
	return strings.TrimSpace(string(rs))
}
