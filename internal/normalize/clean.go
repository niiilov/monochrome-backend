package normalize

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

var (
	scriptRE  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRE   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	commentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	blockRE   = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|blockquote|section|article)\s*>|<br\s*/?>`)
)

// Clean strips HTML, NULs, and control characters, then collapses whitespace.
// Paragraph breaks (a blank line) are kept. A single line wrap becomes a space.
func Clean(s string) string {
	if s == "" {
		return ""
	}
	s = scriptRE.ReplaceAllString(s, " ")
	s = styleRE.ReplaceAllString(s, " ")
	s = commentRE.ReplaceAllString(s, " ")
	s = blockRE.ReplaceAllStringFunc(s, func(tag string) string {
		if strings.Contains(strings.ToLower(tag), "br") {
			return "\n"
		}
		return "\n\n"
	})
	s = stripTags(s)
	s = html.UnescapeString(s)
	s = dropControls(s)
	return foldSpace(s)
}

func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '<' {
			b.WriteRune(rs[i])
			continue
		}
		if !tagAt(rs, i) {
			b.WriteRune('<')
			continue
		}
		k := i + 1
		limit := i + 2000
		if limit > len(rs) {
			limit = len(rs)
		}
		for k < limit && rs[k] != '>' {
			k++
		}
		if k < len(rs) && rs[k] == '>' {
			i = k
			continue
		}
		b.WriteRune('<')
	}
	return b.String()
}

func tagAt(rs []rune, i int) bool {
	if i+1 >= len(rs) {
		return false
	}
	n := rs[i+1]
	if n == '/' || n == '!' {
		return true
	}
	return unicode.IsLetter(n)
}

func dropControls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteRune(r)
		case '\uFEFF':
			continue
		default:
			if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) && !unicode.IsSpace(r) {
				continue
			}
			if unicode.Is(unicode.Cc, r) {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func foldSpace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Zs, r) {
			return ' '
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	var b strings.Builder
	started := false
	blank := 0
	for _, line := range lines {
		line = collapseSpaces(strings.TrimSpace(line))
		if line == "" {
			if started {
				blank++
			}
			continue
		}
		if started {
			if blank > 0 {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(line)
		started = true
		blank = 0
	}
	return b.String()
}

func collapseSpaces(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if r == ' ' {
			if prev {
				continue
			}
			prev = true
			b.WriteByte(' ')
			continue
		}
		prev = false
		b.WriteRune(r)
	}
	return b.String()
}
