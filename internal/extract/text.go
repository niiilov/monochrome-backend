package extract

import (
	"strings"
	"unicode/utf8"

	"mono/internal/textutil"
)

// EnoughText reports whether non-empty text reaches the soft review threshold.
// A false result requires additional assessment, not automatic exclusion.
// minChars counts Unicode code points after URL removal, not sentences.
func EnoughText(text string, minChars int) bool {
	if minChars < 0 {
		minChars = 0
	}
	return strings.TrimSpace(textutil.StripURLs(text)) != "" && textutil.CountChars(text) >= minChars
}

func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
