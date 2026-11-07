package verify

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/textutil"
)

func foldWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func corpusOf(articles []domain.Article) string {
	var b strings.Builder
	for _, article := range articles {
		b.WriteString(article.Title)
		b.WriteByte('\n')
		b.WriteString(article.Text)
		b.WriteByte('\n')
		b.WriteString(article.RawText)
		b.WriteByte('\n')
		b.WriteString(article.Source)
		b.WriteByte('\n')
	}
	return b.String()
}

func articlesFor(item domain.DigestItem, event domain.Event, arts map[string]domain.Article) []domain.Article {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range event.ArticleIDs {
		add(id)
	}
	for _, mention := range event.Mentions {
		add(mention.ArticleID)
		for _, fact := range mention.Facts {
			add(fact.ArticleID)
		}
	}
	add(event.PrimaryArticleID)
	for _, source := range item.Sources {
		add(source.ArticleID)
	}
	var out []domain.Article
	for _, id := range ids {
		article, ok := arts[id]
		if !ok {
			continue
		}
		if strings.TrimSpace(article.ID) == "" {
			article.ID = id
		}
		out = append(out, article)
	}
	return out
}

func eventByID(events []domain.Event) map[string]domain.Event {
	out := make(map[string]domain.Event, len(events))
	for _, event := range events {
		out[event.ID] = event
	}
	return out
}

func prose(item domain.DigestItem) string {
	return strings.Join([]string{item.Title, item.Essence, item.Consequences, item.Rationale}, "\n")
}

func numberSet(text string) map[string]bool {
	return setOf(textutil.ExtractNumbers(text))
}

func setOf(values []string) map[string]bool {
	if len(values) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func onlyIn(a, b map[string]bool) []string {
	var out []string
	for key := range a {
		if !b[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func quoteSpans(text string) []string {
	var out []string
	rs := []rune(text)
	pairs := [][2]rune{
		{'«', '»'},
		{'"', '"'},
		{'„', '“'},
	}
	for i := 0; i < len(rs); i++ {
		for _, pair := range pairs {
			if rs[i] != pair[0] {
				continue
			}
			for j := i + 1; j < len(rs) && j-i <= 400; j++ {
				if rs[j] != pair[1] {
					continue
				}
				inner := strings.TrimSpace(string(rs[i+1 : j]))
				if utf8.RuneCountInString(inner) >= 2 {
					out = append(out, inner)
				}
				i = j
				break
			}
		}
	}
	return out
}

func quoteInCorpus(quote, corpus string) bool {
	quote = foldWS(quote)
	if quote == "" {
		return true
	}
	return strings.Contains(foldWS(corpus), quote)
}

func clipRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "..."
}

type nameHit struct {
	text   string
	abbrev bool
}

func namesIn(text string) []nameHit {
	text = textutil.StripURLs(text)
	var out []nameHit
	seen := map[string]bool{}
	for _, sentence := range splitSentences(text) {
		tokens := wordTokens(sentence)
		for i, token := range tokens {
			if isAbbrev(token) {
				if skippedAbbrev(token) {
					continue
				}
				addName(&out, seen, token, true)
				continue
			}
			if i == 0 || !isProper(token) || skippedProper(token) {
				continue
			}
			addName(&out, seen, token, false)
		}
	}
	return out
}

func addName(out *[]nameHit, seen map[string]bool, token string, abbrev bool) {
	key := strings.ToLower(token)
	if abbrev {
		key = "A:" + key
	} else {
		key = "P:" + key
	}
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, nameHit{text: token, abbrev: abbrev})
}

func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	rs := []rune(text)
	var out []string
	start := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if r == '.' && i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]) {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue
		}
		part := strings.TrimSpace(string(rs[start : i+1]))
		if part != "" {
			out = append(out, part)
		}
		j := i + 1
		for j < len(rs) && unicode.IsSpace(rs[j]) {
			j++
		}
		start = j
		i = j - 1
	}
	if start < len(rs) {
		if part := strings.TrimSpace(string(rs[start:])); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func wordTokens(sentence string) []string {
	return strings.FieldsFunc(sentence, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func isAbbrev(token string) bool {
	rs := []rune(token)
	if len(rs) < 2 || len(rs) > 12 {
		return false
	}
	for _, r := range rs {
		if !unicode.IsLetter(r) || !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func isProper(token string) bool {
	rs := []rune(token)
	if len(rs) < 3 || !unicode.IsUpper(rs[0]) {
		return false
	}
	for _, r := range rs[1:] {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}

func skippedAbbrev(token string) bool {
	switch strings.ToUpper(token) {
	case "ID", "URL", "HTTP", "HTTPS", "МСК", "СМИ":
		return true
	default:
		return false
	}
}

func skippedProper(token string) bool {
	switch strings.ToLower(token) {
	case "это", "также", "однако", "поэтому", "кроме", "после", "затем", "далее",
		"здесь", "сейчас", "сегодня", "вчера", "завтра", "источник", "источники",
		"компания", "данные", "новость", "дайджест", "событие", "последствия",
		"обоснование", "суть":
		return true
	default:
		return false
	}
}

func nameInCorpus(hit nameHit, corpus string) bool {
	if hit.abbrev {
		return hasToken(corpus, hit.text)
	}
	if hasToken(corpus, hit.text) {
		return true
	}
	stem := stemName(hit.text)
	if len([]rune(stem)) < 4 {
		return false
	}
	return strings.Contains(strings.ToLower(corpus), stem)
}

func hasToken(corpus, token string) bool {
	want := strings.ToLower(token)
	for _, got := range wordTokens(corpus) {
		if strings.ToLower(got) == want {
			return true
		}
	}
	return false
}

func stemName(word string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	suffixes := []string{
		"ами", "ями", "ого", "ему", "ому", "ыми", "ими",
		"ах", "ях", "ов", "ев", "ей", "ий", "ый", "ой",
		"ая", "яя", "ое", "ее", "ом", "ем", "ых", "их",
		"а", "я", "у", "ю", "е", "ы", "и", "о",
	}
	rs := []rune(w)
	for _, suffix := range suffixes {
		suf := []rune(suffix)
		if len(rs) <= len(suf)+3 {
			continue
		}
		if strings.HasSuffix(w, suffix) {
			return string(rs[:len(rs)-len(suf)])
		}
	}
	return w
}

func finishPeriod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	if r == '.' || r == '!' || r == '?' {
		return s
	}
	return s + "."
}

func allowedDates(corpus string, articles []domain.Article) map[string]bool {
	out := map[string]bool{}
	for _, date := range textutil.ExtractDates(corpus) {
		out[date] = true
	}
	for _, article := range articles {
		if date := publicationDate(article); date != "" {
			out[date] = true
		}
		for _, date := range textutil.ExtractDates(article.PublishedRaw) {
			out[date] = true
		}
	}
	return out
}

func dateAllowed(got string, allowed map[string]bool) bool {
	if allowed[got] {
		return true
	}
	if len(got) == 5 {
		for have := range allowed {
			if have == got || strings.HasPrefix(have, got+".") {
				return true
			}
		}
	}
	return false
}

func eventURLs(articles []domain.Article) map[string]bool {
	out := map[string]bool{}
	for _, article := range articles {
		addURL(out, article.URL)
	}
	return out
}
