package verify

import (
	"fmt"
	"regexp"
	"strings"

	"mono/internal/domain"
	"mono/internal/guard"
	"mono/internal/render"
	"mono/internal/textutil"
)

var (
	headingRE = regexp.MustCompile(`^##\s+\d+\.\s*`)
	postNumRE = regexp.MustCompile(`^\d+\.\s+`)
	idRE      = regexp.MustCompile(`ID:\s*([^\s,;]+)`)
)

func checkItems(m domain.DigestModel, reg Registry, arts map[string]domain.Article, events []domain.Event) []Finding {
	if m.Empty || len(m.Items) == 0 {
		return nil
	}
	byID := eventByID(events)
	var out []Finding
	for _, item := range m.Items {
		out = append(out, checkItem(item, byID[item.EventID], reg, arts)...)
	}
	return out
}

func checkItem(item domain.DigestItem, event domain.Event, reg Registry, arts map[string]domain.Article) []Finding {
	articles := articlesFor(item, event, arts)
	body := corpusOf(articles)
	text := prose(item)
	where := func(part string) string { return eventWhere(item.EventID, part) }
	var out []Finding
	out = append(out, checkNumbers(text, body, where("numbers"))...)
	out = append(out, checkDates(item, body, articles, where("dates"))...)
	out = append(out, checkLinks(item, reg, eventURLs(articles), where("links"))...)
	out = append(out, checkQuotes(text, body, where("quote"))...)
	out = append(out, checkNames(text, body, where("name"))...)
	out = append(out, checkHedges(event, text, where("hedge"))...)
	out = append(out, checkFields(item, where("format"))...)
	return out
}

func checkNumbers(text, corpus, where string) []Finding {
	allowed := numberSet(corpus)
	var out []Finding
	seen := map[string]bool{}
	for _, number := range textutil.ExtractNumbers(text) {
		if allowed[number] || seen[number] {
			continue
		}
		seen[number] = true
		out = append(out, finding(RuleNumbers, SevError, where,
			fmt.Sprintf("число %s не найдено в источниках события", number)))
	}
	return out
}

func checkDates(item domain.DigestItem, corpus string, articles []domain.Article, where string) []Finding {
	allowed := allowedDates(corpus, articles)
	var b strings.Builder
	b.WriteString(prose(item))
	for _, source := range item.Sources {
		b.WriteByte('\n')
		b.WriteString(source.Date)
	}
	var out []Finding
	seen := map[string]bool{}
	for _, date := range textutil.ExtractDates(b.String()) {
		if dateAllowed(date, allowed) || seen[date] {
			continue
		}
		seen[date] = true
		out = append(out, finding(RuleDates, SevError, where,
			fmt.Sprintf("дата %s не найдена в источниках события", date)))
	}
	return out
}

func checkLinks(item domain.DigestItem, reg Registry, eventURLs map[string]bool, where string) []Finding {
	var out []Finding
	seenURL := map[string]bool{}
	for _, raw := range textutil.ExtractURLs(prose(item)) {
		if seenURL[raw] {
			continue
		}
		seenURL[raw] = true
		if !urlKnown(raw, eventURLs) {
			out = append(out, finding(RuleURL, SevError, where,
				fmt.Sprintf("URL %s нет среди источников события", raw)))
		}
	}
	for _, source := range item.Sources {
		rec, ok := reg.Get(source.ArticleID)
		if strings.TrimSpace(source.ArticleID) == "" || !ok {
			out = append(out, finding(RuleID, SevError, where,
				fmt.Sprintf("ID %s нет в наборе", emptyAs(source.ArticleID, "пустой"))))
			continue
		}
		if strings.TrimSpace(source.URL) == "" || !sameURL(source.URL, rec.URL) {
			out = append(out, finding(RuleID, SevError, where,
				fmt.Sprintf("ID %s не соответствует URL рядом", source.ArticleID)))
		}
		if !urlKnown(source.URL, eventURLs) && strings.TrimSpace(source.URL) != "" {
			out = append(out, finding(RuleURL, SevError, where,
				fmt.Sprintf("URL %s нет среди источников события", source.URL)))
		}
	}
	for _, id := range idList(prose(item)) {
		if _, ok := reg.Get(id); !ok {
			out = append(out, finding(RuleID, SevError, where,
				fmt.Sprintf("ID %s нет в наборе", id)))
		}
	}
	return out
}

func checkQuotes(text, corpus, where string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, quote := range quoteSpans(text) {
		key := foldWS(quote)
		if seen[key] {
			continue
		}
		seen[key] = true
		if quoteInCorpus(quote, corpus) {
			continue
		}
		out = append(out, finding(RuleQuote, SevError, where,
			fmt.Sprintf("цитата не найдена в источнике: %s", clipRunes(key, 80))))
	}
	return out
}

func checkNames(text, corpus, where string) []Finding {
	var out []Finding
	for _, hit := range namesIn(text) {
		if nameInCorpus(hit, corpus) {
			continue
		}
		severity := SevWarn
		if hit.abbrev {
			severity = SevError
		}
		out = append(out, finding(RuleName, severity, where,
			fmt.Sprintf("имя «%s» не найдено в источниках", hit.text)))
	}
	return out
}

func checkHedges(event domain.Event, text, where string) []Finding {
	gaps := hedgeGaps(eventHedges(event), text)
	var out []Finding
	for _, hedge := range gaps {
		out = append(out, finding(RuleHedge, SevError, where,
			fmt.Sprintf("в пересказе нет оговорки «%s»", hedge)))
	}
	return out
}

func checkFields(item domain.DigestItem, where string) []Finding {
	var out []Finding
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Essence) == "" ||
		strings.TrimSpace(item.Consequences) == "" || strings.TrimSpace(item.Rationale) == "" {
		out = append(out, finding(RuleFormat, SevError, where, "в пункте пустое поле"))
	}
	if len(item.Sources) == 0 {
		out = append(out, finding(RuleFormat, SevError, where, "в пункте нет источников"))
	}
	return out
}

func checkLimits(digest, post string) []Finding {
	var out []Finding
	if n := textutil.CountChars(digest); n > render.DigestLimit {
		out = append(out, finding(RuleLimit, SevError, "digest",
			fmt.Sprintf("дайджест длиннее %d знаков: %d", render.DigestLimit, n)))
	}
	if n := textutil.CountChars(post); n > render.PostLimit {
		out = append(out, finding(RuleLimit, SevError, "post",
			fmt.Sprintf("пост длиннее %d знаков: %d", render.PostLimit, n)))
	}
	return out
}

func checkModelFormat(m domain.DigestModel, digest string) []Finding {
	var out []Finding
	if len(m.Items) > 5 {
		out = append(out, finding(RuleFormat, SevError, "format",
			fmt.Sprintf("событий больше пяти: %d", len(m.Items))))
	}
	if err := render.CheckFormat(digest); err != nil {
		out = append(out, finding(RuleFormat, SevError, "format", err.Error()))
	}
	return out
}

func checkRank(m domain.DigestModel, events []domain.Event) []Finding {
	if len(m.Items) < 2 || len(events) == 0 {
		return nil
	}
	rank := map[string]int{}
	for _, event := range events {
		rank[event.ID] = event.Rank
	}
	prev := 0
	for i, item := range m.Items {
		got := rank[item.EventID]
		if got <= 0 {
			return nil
		}
		if i > 0 && got < prev {
			return []Finding{finding(RuleFormat, SevError, "format", "порядок пунктов не совпадает с Rank")}
		}
		prev = got
	}
	return nil
}

func checkStyle(m domain.DigestModel, post, email string) []Finding {
	var out []Finding
	for _, item := range m.Items {
		out = append(out, styleOf(prose(item), eventWhere(item.EventID, "style"))...)
	}
	out = append(out, styleOf(post, "post")...)
	out = append(out, styleOf(email, "email")...)
	return out
}

func styleOf(text, where string) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []Finding
	if textutil.HasLongDash(text) {
		out = append(out, finding(RuleStyle, SevError, where, "в тексте есть длинное тире"))
	}
	if phrase := stopPhrase(text); phrase != "" {
		out = append(out, finding(RuleStyle, SevError, where,
			fmt.Sprintf("стоп-фраза «%s»", phrase)))
	}
	return out
}

func checkConsistency(digest, post, email string) []Finding {
	var out []Finding
	out = append(out, compareSets(RuleConsistency, "чисел", numberSet(stripChrome(digest)), numberSet(stripChrome(post)), numberSet(stripChrome(email)))...)
	out = append(out, compareSets(RuleConsistency, "дат", dateSet(stripChrome(digest)), dateSet(stripChrome(post)), dateSet(stripChrome(email)))...)
	out = append(out, compareSets(RuleConsistency, "URL", setOf(textutil.ExtractURLs(digest)), setOf(textutil.ExtractURLs(post)), setOf(textutil.ExtractURLs(email)))...)
	digestIDs := idSet(digest)
	emailIDs := idSet(email)
	postIDs := idSet(post)
	if !sameSet(digestIDs, emailIDs) {
		out = append(out, finding(RuleConsistency, SevError, "email",
			diffMessage("ID", "дайджесте", "письме", digestIDs, emailIDs)))
	}
	if len(postIDs) > 0 && !sameSet(digestIDs, postIDs) {
		out = append(out, finding(RuleConsistency, SevError, "post",
			diffMessage("ID", "дайджесте", "посте", digestIDs, postIDs)))
	}
	return out
}

func compareSets(rule, kind string, digest, post, email map[string]bool) []Finding {
	var out []Finding
	if !sameSet(digest, post) {
		out = append(out, finding(rule, SevError, "post", diffMessage(kind, "дайджесте", "посте", digest, post)))
	}
	if !sameSet(digest, email) {
		out = append(out, finding(rule, SevError, "email", diffMessage(kind, "дайджесте", "письме", digest, email)))
	}
	return out
}

func diffMessage(kind, leftName, rightName string, left, right map[string]bool) string {
	var parts []string
	if extra := onlyIn(left, right); len(extra) > 0 {
		parts = append(parts, fmt.Sprintf("только в %s: %s", leftName, strings.Join(extra, ", ")))
	}
	if extra := onlyIn(right, left); len(extra) > 0 {
		parts = append(parts, fmt.Sprintf("только в %s: %s", rightName, strings.Join(extra, ", ")))
	}
	if len(parts) == 0 {
		return "множества " + kind + " не совпадают"
	}
	return "множества " + kind + " не совпадают (" + strings.Join(parts, "; ") + ")"
}

func dateSet(text string) map[string]bool {
	return setOf(textutil.ExtractDates(text))
}

func idSet(text string) map[string]bool {
	return setOf(idList(text))
}

func idList(text string) []string {
	found := idRE.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(found))
	for _, match := range found {
		if len(match) > 1 {
			out = append(out, match[1])
		}
	}
	return out
}

func stripChrome(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "# "):
			continue
		case strings.HasPrefix(trimmed, "Тема:"):
			continue
		case strings.HasPrefix(trimmed, "Дайджест по кибербезопасности:"):
			continue
		case strings.HasPrefix(trimmed, "Событий в выпуске:"):
			continue
		case strings.HasPrefix(trimmed, "Контрольное время:"):
			continue
		case strings.HasPrefix(trimmed, "Период:"):
			continue
		case strings.HasPrefix(trimmed, "Источники:"):
			continue
		case strings.HasPrefix(trimmed, "Обоснование:"):
			// Ranking rationale is digest-only; post keeps essence and consequences.
			continue
		}
		trimmed = headingRE.ReplaceAllString(trimmed, "")
		trimmed = postNumRE.ReplaceAllString(trimmed, "")
		b.WriteString(trimmed)
		b.WriteByte('\n')
	}
	return b.String()
}

func checkEmpty(m domain.DigestModel, digest, post, email string) []Finding {
	empty := m.Empty || len(m.Items) == 0
	if !empty {
		return nil
	}
	const phrase = "Подходящих событий за период нет"
	var out []Finding
	if !strings.Contains(digest, phrase) {
		out = append(out, finding(RuleEmpty, SevError, "digest", "в дайджесте нет сообщения о пустом выпуске"))
	}
	if !strings.Contains(post, phrase) {
		out = append(out, finding(RuleEmpty, SevError, "post", "в посте нет сообщения о пустом выпуске"))
	}
	if !strings.Contains(email, phrase) {
		out = append(out, finding(RuleEmpty, SevError, "email", "в письме нет сообщения о пустом выпуске"))
	}
	if strings.Contains(digest, "\n## ") || strings.HasPrefix(digest, "## ") {
		out = append(out, finding(RuleEmpty, SevError, "digest", "при нуле событий есть блок пункта"))
	}
	if !strings.Contains(digest, "Событий в выпуске: 0") {
		out = append(out, finding(RuleEmpty, SevError, "digest", "строка счёта не равна нулю"))
	}
	return out
}

func checkInjection(m domain.DigestModel, digest, post, email string, reg Registry) []Finding {
	allowed := reg.AllowedURLs()
	var out []Finding
	seen := map[string]bool{}
	add := func(where string, violations []guard.Violation) {
		for _, violation := range violations {
			key := violation.Kind + "\x00" + violation.Excerpt
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, finding(RuleInjection, SevError, where, injectionMessage(violation)))
		}
	}
	for _, item := range m.Items {
		where := eventWhere(item.EventID, "injection")
		add(where, guard.CheckOutput(prose(item), allowed, nil))
	}
	add("post", guard.CheckOutput(post, allowed, nil))
	add("email", guard.CheckOutput(email, allowed, nil))
	add("digest", guard.CheckOutput(digest, allowed, nil))
	return out
}

func injectionMessage(v guard.Violation) string {
	switch v.Kind {
	case guard.ViolEmail:
		return "в тексте посторонний адрес: " + v.Excerpt
	case guard.ViolURL:
		return "в тексте URL вне реестра: " + v.Excerpt
	case guard.ViolPhrase:
		return "в тексте служебная фраза: " + v.Excerpt
	default:
		return "выход не прошёл проверку: " + v.Excerpt
	}
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
