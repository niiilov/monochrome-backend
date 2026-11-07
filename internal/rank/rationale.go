package rank

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberdigest/internal/textutil"
)

// NoDataNote is the mark stored when a criterion has no support in the articles.
const NoDataNote = "нет данных"

// UnknownDataNote is the trace line used when at least one criterion is nil.
const UnknownDataNote = "часть данных неизвестна"

var (
	importanceRE = regexp.MustCompile(`(?i)важн`)
	searchRankRE = regexp.MustCompile(`(?i)(поисков|выдач)`)
)

func fallbackRationale(g grounds) string {
	parts := []string{
		groundClause("Последствия", "сведения о последствиях не приведены", g.Consequences),
		groundClause("масштаб", "сведения о масштабе не приведены", g.Scale),
		groundClause("срочность", "сведения о срочности не приведены", g.Urgency),
	}
	return finishSentence(strings.Join(parts, "; "))
}

func groundClause(label, absent, value string) string {
	value = soften(value)
	if value == "" {
		return absent
	}
	return label + ": " + value
}

// acceptRationale keeps the model sentence only when it is one sentence,
// stays inside the publication facts, and does not argue about importance
// or a search position.
func acceptRationale(raw string, g grounds) (string, bool) {
	s := oneLine(raw)
	s = strings.TrimSpace(strings.TrimPrefix(s, "Обоснование:"))
	s = oneLine(s)
	if s == "" || sentenceCount(s) != 1 || importanceRE.MatchString(s) || searchRankRE.MatchString(s) {
		return "", false
	}
	if !covers(s, g.Consequences, "последств") || !covers(s, g.Scale, "масштаб") || !covers(s, g.Urgency, "срочност") {
		return "", false
	}
	return finishSentence(s), true
}

func covers(sentence, source, topic string) bool {
	if strings.TrimSpace(source) == "" {
		return acknowledgesAbsence(sentence, topic)
	}
	return sharesWord(sentence, source)
}

func acknowledgesAbsence(s, topic string) bool {
	l := strings.ToLower(s)
	if !strings.Contains(l, topic) {
		return false
	}
	for _, mark := range []string{
		"не привед",
		"нет данных",
		"не указан",
		"отсутств",
		"не сообщ",
		"нет сведен",
		"не содержит",
		"не назван",
	} {
		if strings.Contains(l, mark) {
			return true
		}
	}
	return false
}

func sharesWord(sentence, source string) bool {
	words := longWords(source)
	if len(words) == 0 {
		return true
	}
	low := strings.ToLower(sentence)
	for _, w := range words {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

func longWords(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	seen := map[string]bool{}
	for _, w := range fields {
		if utf8.RuneCountInString(w) < 4 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func sentenceCount(s string) int {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 {
		return 0
	}
	n := 0
	for i, r := range rs {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if r == '.' && i > 0 && unicode.IsDigit(rs[i-1]) {
			continue
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

func soften(s string) string {
	s = oneLine(s)
	s = strings.TrimRight(s, ".!? ")
	s = strings.ReplaceAll(s, ". ", ", ")
	s = strings.ReplaceAll(s, "! ", ", ")
	s = strings.ReplaceAll(s, "? ", ", ")
	return strings.TrimSpace(s)
}

func oneLine(s string) string {
	s = textutil.ReplaceLongDash(s)
	s = strings.Join(strings.Fields(s), " ")
	return clipRunes(s, lineLimit)
}

func finishSentence(s string) string {
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

func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
