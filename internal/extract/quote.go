package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// locateQuote returns the exact slice of article that matches quote after
// whitespace is collapsed. A case-sensitive hit wins. Case folding is the fallback.
func locateQuote(article, quote string) (string, bool) {
	if exact, ok := locateFold(article, quote, false); ok {
		return exact, true
	}
	return locateFold(article, quote, true)
}

func locateFold(article, quote string, lower bool) (string, bool) {
	qn := foldRunes(quote, lower)
	if len(qn) == 0 {
		return "", false
	}
	an, origin := foldArticle(article, lower)
	q := string(qn)
	pos := strings.Index(an, q)
	if pos < 0 {
		return "", false
	}
	start := utf8.RuneCountInString(an[:pos])
	end := start + len(qn) - 1
	if start < 0 || end >= len(origin) || end < start {
		return "", false
	}
	rs := []rune(article)
	from := origin[start]
	to := origin[end]
	if from < 0 || to >= len(rs) || to < from {
		return "", false
	}
	return string(rs[from : to+1]), true
}

func findQuote(text, title, quote string) (string, bool) {
	if exact, ok := locateQuote(text, quote); ok {
		return exact, true
	}
	if exact, ok := locateQuote(title, quote); ok {
		return exact, true
	}
	// Models sometimes paste the title and the body into one quote.
	// Keep the body span, which is a real substring of the article.
	return quoteAfterTitle(title, text, quote)
}

func quoteAfterTitle(title, text, quote string) (string, bool) {
	title = strings.TrimSpace(title)
	if title == "" || strings.TrimSpace(text) == "" {
		return "", false
	}
	tn := string(foldRunes(title, true))
	qn := string(foldRunes(quote, true))
	if tn == "" || !strings.HasPrefix(qn, tn) {
		return "", false
	}
	rest := strings.TrimSpace(qn[len(tn):])
	if utf8.RuneCountInString(rest) < 12 {
		return "", false
	}
	return locateQuote(text, rest)
}

// foldArticle collapses whitespace and records the original rune index of each kept rune.
func foldArticle(s string, lower bool) (string, []int) {
	rs := []rune(s)
	norm := make([]rune, 0, len(rs))
	origin := make([]int, 0, len(rs))
	prevSpace := false
	started := false
	for i, r := range rs {
		if unicode.IsSpace(r) {
			if started && !prevSpace {
				norm = append(norm, ' ')
				origin = append(origin, i)
				prevSpace = true
			}
			continue
		}
		started = true
		prevSpace = false
		if lower {
			r = unicode.ToLower(r)
		}
		norm = append(norm, r)
		origin = append(origin, i)
	}
	if len(norm) > 0 && norm[len(norm)-1] == ' ' {
		norm = norm[:len(norm)-1]
		origin = origin[:len(origin)-1]
	}
	return string(norm), origin
}

func foldRunes(s string, lower bool) []rune {
	norm, _ := foldArticle(s, lower)
	return []rune(norm)
}
