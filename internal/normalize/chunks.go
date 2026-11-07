package normalize

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// protRE marks numbers and dates that a chunk boundary must not cut.
// Spaced groups ("1 000"), decimals, percents, and Russian month names are included.
var protRE = regexp.MustCompile(`(?i)\d{1,2}\.\d{1,2}\.\d{2,4}(?:[, ]+\d{1,2}:\d{2}(?::\d{2})?)?|\d{1,2}\s+(?:января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)(?:\s+\d{4})?(?:,?\s+\d{1,2}:\d{2})?|\d{1,3}(?:[ \x{00a0}\x{202f}]\d{3})+(?:[.,]\d+)?(?:\s*(?:%|тыс\.?|млн\.?|млрд\.?))?|\d+(?:[.,]\d+)?(?:\s*(?:%|тыс\.?|млн\.?|млрд\.?))?`)

// Chunks splits text into pieces of about maxChars runes.
// Cuts prefer a paragraph, then a sentence, then whitespace.
// Consecutive pieces overlap by about one fifth of maxChars, and never by a whole piece.
// A number or a date stays inside one piece even when that piece runs past maxChars.
// maxChars <= 0 or a short text returns the trimmed text as a single piece.
func Chunks(text string, maxChars int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	rs := []rune(text)
	if maxChars <= 0 || len(rs) <= maxChars {
		return []string{text}
	}
	mark := markProtected(text)
	overlap := maxChars / 5
	if overlap > 400 {
		overlap = 400
	}
	var out []string
	start := 0
	for start < len(rs) {
		if len(rs)-start <= maxChars {
			if piece := strings.TrimSpace(string(rs[start:])); piece != "" {
				out = append(out, piece)
			}
			break
		}
		end := snapEnd(rs, mark, start, start+maxChars)
		if end <= start {
			end = extendCut(mark, start+maxChars)
		}
		if end <= start {
			end = start + 1
		}
		if end > len(rs) {
			end = len(rs)
		}
		if piece := strings.TrimSpace(string(rs[start:end])); piece != "" {
			out = append(out, piece)
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		next = snapStart(rs, mark, next, end)
		if next <= start {
			next = end
		}
		start = next
	}
	return out
}

func markProtected(s string) []bool {
	rs := []rune(s)
	mark := make([]bool, len(rs))
	if len(s) == 0 {
		return mark
	}
	b2r := make([]int, len(s)+1)
	bi := 0
	for ri, r := range rs {
		size := utf8.RuneLen(r)
		if size < 1 {
			size = 1
		}
		for k := 0; k < size && bi+k < len(b2r); k++ {
			b2r[bi+k] = ri
		}
		bi += size
	}
	b2r[len(s)] = len(rs)
	for _, loc := range protRE.FindAllStringIndex(s, -1) {
		if loc[0] < 0 || loc[1] > len(s) {
			continue
		}
		r0 := b2r[loc[0]]
		r1 := b2r[loc[1]]
		for i := r0; i < r1 && i < len(mark); i++ {
			mark[i] = true
		}
	}
	return mark
}

func cutInside(mark []bool, cut int) bool {
	return cut > 0 && cut < len(mark) && mark[cut-1] && mark[cut]
}

func extendCut(mark []bool, cut int) int {
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && cut < len(mark) && mark[cut-1] && mark[cut] {
		cut++
	}
	if cut > len(mark) {
		return len(mark)
	}
	return cut
}

func snapEnd(rs []rune, mark []bool, start, limit int) int {
	if limit > len(rs) {
		limit = len(rs)
	}
	if limit <= start {
		return start
	}
	minKeep := start + (limit-start)/3
	para, sent, space := -1, -1, -1
	paraLow, sentLow, spaceLow := -1, -1, -1
	for cut := limit; cut > start; cut-- {
		if cutInside(mark, cut) {
			continue
		}
		high := cut >= minKeep
		if isPara(rs, cut) {
			if high && para < 0 {
				para = cut
			} else if paraLow < 0 {
				paraLow = cut
			}
		}
		if isSentence(rs, cut) {
			if high && sent < 0 {
				sent = cut
			} else if sentLow < 0 {
				sentLow = cut
			}
		}
		if isSpaceCut(rs, cut) {
			if high && space < 0 {
				space = cut
			} else if spaceLow < 0 {
				spaceLow = cut
			}
		}
		if para > 0 {
			break
		}
	}
	for _, c := range []int{para, sent, space, paraLow, sentLow, spaceLow} {
		if c > start {
			return c
		}
	}
	if !cutInside(mark, limit) {
		return limit
	}
	return extendCut(mark, limit)
}

func snapStart(rs []rune, mark []bool, next, end int) int {
	if next < 0 {
		next = 0
	}
	if next >= end {
		return end
	}
	floor := next - 40
	if floor < 0 {
		floor = 0
	}
	for next > floor && cutInside(mark, next) {
		next--
	}
	for next > floor && next < len(rs) && isWordRune(rs[next]) && isWordRune(rs[next-1]) {
		next--
	}
	if cutInside(mark, next) || (next > 0 && next < len(rs) && isWordRune(rs[next]) && isWordRune(rs[next-1])) {
		return end
	}
	if next > end {
		return end
	}
	return next
}

func isPara(rs []rune, cut int) bool {
	return cut >= 2 && rs[cut-1] == '\n' && rs[cut-2] == '\n'
}

func isSentence(rs []rune, cut int) bool {
	if cut <= 0 || cut > len(rs) {
		return false
	}
	prev := rs[cut-1]
	if prev != '.' && prev != '!' && prev != '?' {
		return false
	}
	if cut < len(rs) && !unicode.IsSpace(rs[cut]) {
		return false
	}
	return true
}

func isSpaceCut(rs []rune, cut int) bool {
	if cut <= 0 || cut > len(rs) {
		return false
	}
	if !unicode.IsSpace(rs[cut-1]) {
		return false
	}
	if cut < len(rs) && unicode.IsSpace(rs[cut]) {
		return false
	}
	return true
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
