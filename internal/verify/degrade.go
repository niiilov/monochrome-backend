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
		// Ranking rationale stays in digest.md; the post only needs facts.
		essence := strings.TrimSpace(item.Essence)
		parts := []string{essence}
		if consequences := strings.TrimSpace(item.Consequences); consequences != "" && consequences != summarize.NoConsequences {
			if !strings.Contains(strings.ToLower(essence), strings.ToLower(consequences)) {
				parts = append(parts, consequences)
			}
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
		next.Title = firstGrounded(
			pickTitle(event, body, urls),
			groundLine(item.Title, body, urls, 180),
			"Событие",
		)
		next.Essence = firstGrounded(
			pickEssence(event, body, urls),
			groundProse(item.Essence, body, urls),
			"Источник описывает событие.",
		)
		next.Consequences = firstGrounded(
			pickConsequences(event, body, urls),
			groundProse(item.Consequences, body, urls),
			summarize.NoConsequences,
		)
		next.Rationale = firstGrounded(
			pickRationale(event, body, urls),
			groundProse(item.Rationale, body, urls),
			noRationale,
		)
	} else {
		// Hard path still prefers grounded facts and ranking rationale over blank stubs.
		next.Title = firstGrounded(pickTitle(event, body, urls), "Событие")
		next.Essence = firstGrounded(pickEssence(event, body, urls), "Источник описывает событие.")
		next.Consequences = firstGrounded(pickConsequences(event, body, urls), summarize.NoConsequences)
		next.Rationale = firstGrounded(pickRationale(event, body, urls), noRationale)
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

func firstGrounded(parts ...string) string {
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func pickTitle(event domain.Event, body string, urls map[string]bool) string {
	for _, mention := range event.Mentions {
		if title := groundLine(mention.TitleDraft, body, urls, 180); title != "" {
			return title
		}
	}
	for _, fact := range factsOf(event) {
		if title := groundLine(fact.Text, body, urls, 180); title != "" {
			return cutLine(firstSentence(title), 180)
		}
		if title := groundLine(fact.Quote, body, urls, 180); title != "" {
			return cutLine(firstSentence(title), 180)
		}
	}
	return ""
}

func pickEssence(event domain.Event, body string, urls map[string]bool) string {
	var parts []string
	for _, fact := range factsOf(event) {
		text := groundProse(fact.Text, body, urls)
		if text == "" {
			text = groundProse(fact.Quote, body, urls)
		}
		if text == "" {
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
		if text := groundProse(mention.Consequences, body, urls); text != "" {
			return finishPeriod(text)
		}
	}
	return ""
}

func pickRationale(event domain.Event, body string, urls map[string]bool) string {
	if text := groundProse(event.Rationale, body, urls); text != "" {
		return finishPeriod(text)
	}
	return ""
}

func applyHedges(essence string, event domain.Event, body string, urls map[string]bool) string {
	for _, hedge := range hedgeGaps(eventHedges(event), essence) {
		if hasAttribution(essence) {
			break
		}
		line := hedgeFallback(hedge)
		if cleaned := groundProse(line, body, urls); cleaned != "" {
			essence = strings.TrimSpace(essence + " " + finishPeriod(cleaned))
			continue
		}
		// Fixed marker lines have no invented numbers or quotes.
		essence = strings.TrimSpace(essence + " " + finishPeriod(line))
	}
	return finishPeriod(dedupeSentences(essence))
}

func hasAttribution(prose string) bool {
	for _, marker := range hedgeMarkers {
		if markerHits(prose, marker) {
			return true
		}
	}
	low := strings.ToLower(foldWS(prose))
	for _, phrase := range []string{
		"опирается", "по ее заявлен", "по её заявлен",
		"компания заявила", "компания сообщила",
	} {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return false
}

func dedupeSentences(text string) string {
	var out []string
	seen := map[string]bool{}
	for _, sentence := range splitSentences(text) {
		key := strings.ToLower(foldWS(sentence))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, sentence)
	}
	return strings.Join(out, " ")
}

// cleanCandidate keeps the strict all-or-nothing check used by older call sites.
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

func groundLine(text, corpus string, urls map[string]bool, limit int) string {
	text = groundProse(text, corpus, urls)
	if text == "" {
		return ""
	}
	return cutLine(text, limit)
}

// groundProse drops invented numbers, unknown URLs and unverified quotes
// sentence by sentence instead of discarding the whole retelling.
func groundProse(text, corpus string, urls map[string]bool) string {
	text = foldWS(textutil.ReplaceLongDash(text))
	if text == "" || stopPhrase(text) != "" {
		return ""
	}
	allowed := numberSet(corpus)
	var kept []string
	for _, sentence := range splitSentences(text) {
		sentence = foldWS(sentence)
		if sentence == "" || stopPhrase(sentence) != "" {
			continue
		}
		badNumber := false
		for _, number := range textutil.ExtractNumbers(sentence) {
			if !allowed[number] {
				badNumber = true
				break
			}
		}
		if badNumber {
			continue
		}
		sentence = stripUnknownURLs(sentence, urls)
		sentence = stripUnknownQuotes(sentence, corpus)
		sentence = foldWS(sentence)
		if sentence == "" {
			continue
		}
		kept = append(kept, sentence)
	}
	return strings.Join(kept, " ")
}

func stripUnknownURLs(text string, urls map[string]bool) string {
	for _, raw := range textutil.ExtractURLs(text) {
		if urlKnown(raw, urls) {
			continue
		}
		text = strings.ReplaceAll(text, raw, " ")
	}
	return foldWS(text)
}

func stripUnknownQuotes(text, corpus string) string {
	rs := []rune(text)
	pairs := [][2]rune{
		{'«', '»'},
		{'"', '"'},
		{'„', '“'},
	}
	var b strings.Builder
	for i := 0; i < len(rs); {
		matched := false
		for _, pair := range pairs {
			if rs[i] != pair[0] {
				continue
			}
			for j := i + 1; j < len(rs) && j-i <= 400; j++ {
				if rs[j] != pair[1] {
					continue
				}
				inner := strings.TrimSpace(string(rs[i+1 : j]))
				if quoteInCorpus(inner, corpus) {
					b.WriteString(string(rs[i : j+1]))
				} else {
					b.WriteByte(' ')
				}
				i = j + 1
				matched = true
				break
			}
			if matched {
				break
			}
		}
		if matched {
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return foldWS(b.String())
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
