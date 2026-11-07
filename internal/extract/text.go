package extract

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberdigest/internal/textutil"
)

// minSentences is the smallest number of terminated sentences that can be retold.
// The frozen config has no field for this, so the threshold lives next to min_text_chars.
const minSentences = 2

// EnoughText reports whether text can support a retelling.
// minChars counts runes after URL removal. Fewer than minSentences terminated
// sentences is never enough, even when the model says otherwise.
func EnoughText(text string, minChars int) bool {
	if minChars < 0 {
		minChars = 0
	}
	if textutil.CountChars(text) < minChars {
		return false
	}
	return sentenceCount(text) >= minSentences
}

func sentenceCount(text string) int {
	rs := []rune(strings.TrimSpace(text))
	n := 0
	for i, r := range rs {
		if r != '.' && r != '!' && r != '?' && r != '…' {
			continue
		}
		if r == '.' && i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]) {
			continue
		}
		if i+1 == len(rs) || unicode.IsSpace(rs[i+1]) {
			n++
		}
	}
	return n
}

func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
