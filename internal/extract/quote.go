package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// locateQuote returns the exact slice of article that matches quote after
// whitespace is collapsed. The match is case-sensitive.
func locateQuote(article, quote string) (string, bool) {
	qn := foldRunes(quote)
	if len(qn) == 0 {
		return "", false
	}
	an, origin := foldArticle(article)
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
	return locateQuote(title, quote)
}

// foldArticle collapses whitespace and records the original rune index of each kept rune.
func foldArticle(s string) (string, []int) {
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
		norm = append(norm, r)
		origin = append(origin, i)
	}
	if len(norm) > 0 && norm[len(norm)-1] == ' ' {
		norm = norm[:len(norm)-1]
		origin = origin[:len(origin)-1]
	}
	return string(norm), origin
}

func foldRunes(s string) []rune {
	norm, _ := foldArticle(s)
	return []rune(norm)
}
