// Package textutil counts characters and pulls numbers, dates, URLs, and emails out of text.
package textutil

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	urlRE         = regexp.MustCompile(`https?://\S+`)
	emailRE       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	numericDateRE = regexp.MustCompile(`(?:^|[^\d])(\d{1,2})\.(\d{1,2})\.(\d{4})(?:[^\d]|$)`)
	verbalDateRE  = regexp.MustCompile(`(?i)(?:^|[^\p{L}\d])(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)(?:\s+(\d{4}))?(?:[^\p{L}\d]|$)`)
	numberRE      = regexp.MustCompile(`(\d{1,3}(?:[ \x{00a0}\x{202f}]\d{3})+|\d+(?:[.,]\d+)?)(?:\s*(%|тыс\.?|тысяч\p{L}*|млн\.?|миллион\p{L}*|млрд\.?|миллиард\p{L}*))?`)
)

var months = map[string]int{
	"января": 1, "февраля": 2, "марта": 3, "апреля": 4,
	"мая": 5, "июня": 6, "июля": 7, "августа": 8,
	"сентября": 9, "октября": 10, "ноября": 11, "декабря": 12,
}

// StripURLs removes http(s) URLs. Trailing punctuation such as ")" and "," stays in the text.
func StripURLs(s string) string {
	return urlRE.ReplaceAllStringFunc(s, func(m string) string {
		_, tail := splitTrail(m)
		return tail
	})
}

// CountChars counts Unicode code points after StripURLs.
func CountChars(s string) int {
	return utf8.RuneCountInString(StripURLs(s))
}

// HasLongDash reports whether s contains U+2014 or U+2013.
func HasLongDash(s string) bool {
	return strings.ContainsRune(s, '\u2014') || strings.ContainsRune(s, '\u2013')
}

// ReplaceLongDash replaces a long dash between digits with "-" and any other long dash with " - ".
func ReplaceLongDash(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(rs) + 8)
	for i, r := range rs {
		if r != '\u2014' && r != '\u2013' {
			b.WriteRune(r)
			continue
		}
		prevDigit := i > 0 && unicode.IsDigit(rs[i-1])
		nextDigit := i+1 < len(rs) && unicode.IsDigit(rs[i+1])
		if prevDigit && nextDigit {
			b.WriteByte('-')
			continue
		}
		if i == 0 || !unicode.IsSpace(rs[i-1]) {
			b.WriteByte(' ')
		}
		b.WriteByte('-')
		if i+1 == len(rs) || !unicode.IsSpace(rs[i+1]) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// ExtractNumbers returns normalized numbers. "1 000" becomes "1000", "1,5 млн" becomes "1500000", "20%" stays "20%".
// Numbers that belong to a recognized date are skipped.
func ExtractNumbers(s string) []string {
	masked := maskMatches(s, urlRE)
	masked = maskMatches(masked, numericDateRE)
	low := strings.ToLower(masked)
	low = maskMatches(low, verbalDateRE)
	found := numberRE.FindAllStringSubmatch(low, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		norm, ok := canonicalNumber(m[1], m[2])
		if ok {
			out = append(out, norm)
		}
	}
	return out
}

// ExtractDates returns dates as DD.MM or DD.MM.YYYY. A verbal date without a year has no year.
func ExtractDates(s string) []string {
	low := strings.ToLower(s)
	type hit struct {
		at  int
		val string
	}
	var hits []hit
	for _, m := range numericDateRE.FindAllStringSubmatchIndex(low, -1) {
		day := atoi(low[m[2]:m[3]])
		month := atoi(low[m[4]:m[5]])
		year := atoi(low[m[6]:m[7]])
		if !validYMD(year, month, day) {
			continue
		}
		hits = append(hits, hit{at: m[2], val: fmt.Sprintf("%02d.%02d.%04d", day, month, year)})
	}
	for _, m := range verbalDateRE.FindAllStringSubmatchIndex(low, -1) {
		day := atoi(low[m[2]:m[3]])
		month := months[low[m[4]:m[5]]]
		year := 0
		if m[6] >= 0 {
			year = atoi(low[m[6]:m[7]])
		}
		if month == 0 || day < 1 || day > 31 {
			continue
		}
		if year != 0 && !validYMD(year, month, day) {
			continue
		}
		val := fmt.Sprintf("%02d.%02d", day, month)
		if year != 0 {
			val = fmt.Sprintf("%02d.%02d.%04d", day, month, year)
		}
		hits = append(hits, hit{at: m[2], val: val})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.val
	}
	return out
}

// ExtractURLs returns http(s) URLs without trailing punctuation.
func ExtractURLs(s string) []string {
	found := urlRE.FindAllString(s, -1)
	out := make([]string, 0, len(found))
	for _, raw := range found {
		core, _ := splitTrail(raw)
		if core != "" {
			out = append(out, core)
		}
	}
	return out
}

// ExtractEmails returns email addresses without trailing punctuation.
func ExtractEmails(s string) []string {
	found := emailRE.FindAllString(s, -1)
	out := make([]string, 0, len(found))
	for _, raw := range found {
		core, _ := splitTrail(raw)
		if strings.Contains(core, "@") {
			out = append(out, core)
		}
	}
	return out
}

func splitTrail(raw string) (core, tail string) {
	core = strings.TrimRight(raw, ".,;:!?)]}>\"'")
	return core, raw[len(core):]
}

func maskMatches(s string, re *regexp.Regexp) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
}

func canonicalNumber(raw, suffix string) (string, bool) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\u00a0', '\u202f':
			return -1
		case ',':
			return '.'
		default:
			return r
		}
	}, raw)
	value, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return "", false
	}
	suf := strings.TrimSuffix(strings.TrimSpace(suffix), ".")
	switch {
	case suf == "%":
		return formatPlain(value) + "%", true
	case strings.HasPrefix(suf, "тыс"):
		value *= 1000
	case strings.HasPrefix(suf, "млн"), strings.HasPrefix(suf, "миллион"):
		value *= 1000000
	case strings.HasPrefix(suf, "млрд"), strings.HasPrefix(suf, "миллиард"):
		value *= 1000000000
	}
	return formatPlain(value), true
}

func formatPlain(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-6 {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func validYMD(year, month, day int) bool {
	return year >= 1 && month >= 1 && month <= 12 && day >= 1 && day <= 31
}
