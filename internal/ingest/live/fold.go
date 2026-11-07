package live

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberdigest/internal/normalize"
)

// FoldParts splits a long article with normalize.Chunks and keeps a short extract of every part.
// A text that already fits, or a single chunk, is returned unchanged.
// The joined extract stays within maxChars runes so later stages still see the tail of a long page.
func FoldParts(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxChars <= 0 {
		return text
	}
	if utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	parts := pickParts(normalize.Chunks(text, maxChars), 8)
	if len(parts) <= 1 {
		return clipRunes(text, maxChars)
	}
	gaps := 2 * (len(parts) - 1)
	share := 1
	if maxChars > gaps {
		share = (maxChars - gaps) / len(parts)
	}
	if share < 1 {
		share = 1
	}
	pieces := make([]string, 0, len(parts))
	for i, part := range parts {
		var piece string
		if i == len(parts)-1 {
			piece = tailSentences(part, share)
		} else {
			piece = headSentences(part, share)
		}
		if piece == "" {
			continue
		}
		pieces = append(pieces, piece)
	}
	return joinWithin(pieces, maxChars)
}

// pickParts keeps a spread of chunks and always includes the last one.
func pickParts(parts []string, max int) []string {
	if max < 2 || len(parts) <= max {
		return parts
	}
	out := make([]string, 0, max)
	span := len(parts) - 1
	slots := max - 1
	for i := 0; i < slots; i++ {
		out = append(out, parts[i*span/slots])
	}
	out = append(out, parts[len(parts)-1])
	return out
}

func joinWithin(pieces []string, maxChars int) string {
	if len(pieces) == 0 {
		return ""
	}
	for len(pieces) > 2 {
		joined := strings.Join(pieces, "\n\n")
		if utf8.RuneCountInString(joined) <= maxChars {
			return strings.TrimSpace(joined)
		}
		pieces = append(append([]string{}, pieces[0]), pieces[2:]...)
	}
	joined := strings.Join(pieces, "\n\n")
	if utf8.RuneCountInString(joined) <= maxChars {
		return strings.TrimSpace(joined)
	}
	tail := pieces[len(pieces)-1]
	if len(pieces) == 1 || utf8.RuneCountInString(tail) >= maxChars {
		return clipRunes(tail, maxChars)
	}
	headBudget := maxChars - utf8.RuneCountInString(tail) - 2
	if headBudget < 1 {
		return clipRunes(tail, maxChars)
	}
	return strings.TrimSpace(clipRunes(pieces[0], headBudget) + "\n\n" + tail)
}

func headSentences(part string, budget int) string {
	part = strings.TrimSpace(part)
	if part == "" || budget <= 0 {
		return ""
	}
	rs := []rune(part)
	if len(rs) <= budget {
		return part
	}
	cut := budget
	for i := 0; i < budget && i < len(rs); i++ {
		r := rs[i]
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(rs) || unicode.IsSpace(rs[i+1])) {
			cut = i + 1
		}
	}
	return strings.TrimSpace(string(rs[:cut]))
}

func tailSentences(part string, budget int) string {
	part = strings.TrimSpace(part)
	if part == "" || budget <= 0 {
		return ""
	}
	rs := []rune(part)
	if len(rs) <= budget {
		return part
	}
	start := len(rs) - budget
	cut := start
	for i := start; i > 0 && start-i < 60; i-- {
		r := rs[i-1]
		if (r == '.' || r == '!' || r == '?') && unicode.IsSpace(rs[i]) {
			cut = i
			for cut < len(rs) && unicode.IsSpace(rs[cut]) {
				cut++
			}
			break
		}
	}
	if cut >= len(rs) {
		cut = start
	}
	return strings.TrimSpace(string(rs[cut:]))
}

func clipRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return strings.TrimSpace(string(rs[:n]))
}
