package summarize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"mono/internal/textutil"
)

const (
	// RolePrimary is a vendor or regulator source and is rendered first.
	RolePrimary = "первичный источник"
	// RoleMedia is a newsroom, an author, or an unknown source.
	RoleMedia = "СМИ"
	// RoleReprint is a mention marked as a reprint and is not a primary source.
	RoleReprint = "перепечатка"
	// NoConsequences is the fixed line when the sources do not describe an impact.
	NoConsequences = "В источнике сведения о последствиях не приведены."

	maxEvents       = 5
	digestCharLimit = 4000
	postCharLimit   = 2000
	maxParallel     = 8

	shrinkHint  = "Текст длиннее лимита выпуска. Сократи суть до двух коротких предложений, последствия до одного предложения. Сохрани числа, даты, имена и оговорки."
	noRationale = "Обоснование отбора в данных события не приведено."
)

// SplitSentences splits prose on . ! ? and keeps dates and short abbreviations intact.
func SplitSentences(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	rs := []rune(s)
	var out []string
	start := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if r == '.' && dotInsideNumber(rs, i) {
			continue
		}
		if r == '.' && shortAbbrev(rs, i) && i+1 < len(rs) {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue
		}
		if part := strings.TrimSpace(string(rs[start : i+1])); part != "" {
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

// JoinSentences joins sentences with a single space.
func JoinSentences(parts []string) string {
	return strings.Join(parts, " ")
}

// NormalizeConsequences returns the fixed line when the model gave no impact.
func NormalizeConsequences(s string) string {
	s = cleanProse(s)
	if s == "" {
		return NoConsequences
	}
	low := strings.Trim(strings.ToLower(s), ".")
	switch low {
	case "нет данных", "нет сведений", "не указано", "не приведены", "нет":
		return NoConsequences
	}
	if strings.Contains(low, "сведения о последствиях не приведены") ||
		strings.Contains(low, "сведения о последствиях в источнике не приведены") ||
		strings.Contains(low, "нет сведений о последствиях") {
		return NoConsequences
	}
	return s
}

func cleanProse(s string) string {
	s = textutil.ReplaceLongDash(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func runeCount(s string) int {
	return utf8.RuneCountInString(s)
}

func endsSentence(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r == '.' || r == '!' || r == '?'
}

func mergeHedge(essence, hedge string) string {
	hedge = cleanProse(hedge)
	essence = cleanProse(essence)
	if hedge == "" {
		return essence
	}
	if strings.Contains(strings.ToLower(essence), strings.ToLower(hedge)) {
		return essence
	}
	if !endsSentence(hedge) {
		hedge += "."
	}
	if essence == "" {
		return hedge
	}
	if !endsSentence(essence) {
		essence += "."
	}
	return essence + " " + hedge
}

func uniqueLines(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = oneLine(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func dotInsideNumber(rs []rune, i int) bool {
	return i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1])
}

func shortAbbrev(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && unicode.IsLetter(rs[j]) {
		j--
	}
	n := i - 1 - j
	return n > 0 && n <= 3
}
