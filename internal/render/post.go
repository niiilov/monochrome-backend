package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"mono/internal/domain"
	"mono/internal/summarize"
	"mono/internal/textutil"
)

type postRow struct {
	n     int
	title string
	text  string
	urls  []string
}

// Post renders post.txt.
// Numbering and URLs follow the digest model. Short texts fill the prose.
// The result stays within PostLimit runes, not counting URLs.
func Post(m domain.DigestModel, shorts []summarize.ShortItem) string {
	prepared := prepare(m)
	if len(prepared.Items) == 0 {
		return emptyPost(prepared)
	}
	rows := rowsFrom(prepared, shorts)
	fitPost(prepared, rows)
	return formatPost(prepared, rows)
}

func emptyPost(m domain.DigestModel) string {
	return "Дайджест по кибербезопасности: " + periodPhrase(m.Period) + "\n\n" + emptyMessage(m.Shortfall) + "\n"
}

func rowsFrom(m domain.DigestModel, shorts []summarize.ShortItem) []postRow {
	byN := map[int]summarize.ShortItem{}
	for _, item := range shorts {
		byN[item.N] = item
	}
	rows := make([]postRow, len(m.Items))
	for i, item := range m.Items {
		title := item.Title
		text := item.Essence
		if short, ok := byN[item.N]; ok {
			if got := cleanLine(textutil.StripURLs(short.Title)); got != "" {
				title = got
			}
			if got := cleanLine(textutil.StripURLs(short.Text)); got != "" {
				text = got
			}
		}
		rows[i] = postRow{
			n:     item.N,
			title: title,
			text:  text,
			urls:  itemURLs(item),
		}
	}
	return rows
}

func itemURLs(item domain.DigestItem) []string {
	var out []string
	seen := map[string]bool{}
	for _, source := range item.Sources {
		url := strings.TrimSpace(source.URL)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, url)
	}
	return out
}

func formatPost(m domain.DigestModel, rows []postRow) string {
	var b strings.Builder
	b.WriteString("Дайджест по кибербезопасности: ")
	b.WriteString(periodPhrase(m.Period))
	b.WriteByte('\n')
	for _, row := range rows {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%d. %s\n", row.n, row.title)
		if row.text != "" {
			b.WriteString(row.text)
			b.WriteByte('\n')
		}
		for _, url := range row.urls {
			b.WriteString(url)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func fitPost(m domain.DigestModel, rows []postRow) {
	for step := 0; step < 400 && textutil.CountChars(formatPost(m, rows)) > PostLimit; step++ {
		over := textutil.CountChars(formatPost(m, rows)) - PostLimit
		if !trimPostOnce(rows, over) {
			return
		}
	}
}

func trimPostOnce(rows []postRow, overflow int) bool {
	best, bestScore := -1, 1
	for i, row := range rows {
		n := len(summarize.SplitSentences(row.text))
		if n > bestScore {
			best, bestScore = i, n
		}
	}
	if best >= 0 {
		next, ok := dropLastSentence(rows[best].text, 1)
		if ok {
			rows[best].text = next
			return true
		}
	}
	best, bestLen := -1, 0
	for i, row := range rows {
		n := utf8.RuneCountInString(row.text)
		if n > bestLen {
			best, bestLen = i, n
		}
	}
	if best >= 0 && bestLen > 60 {
		next := cutRunes(rows[best].text, bestLen-overflow)
		if utf8.RuneCountInString(next) < bestLen {
			if utf8.RuneCountInString(next) < 60 {
				next = cutRunes(rows[best].text, 60)
			}
			if next != rows[best].text {
				rows[best].text = next
				return true
			}
		}
	}
	best, bestLen = -1, 0
	for i, row := range rows {
		n := utf8.RuneCountInString(row.title)
		if n > bestLen {
			best, bestLen = i, n
		}
	}
	if best >= 0 && bestLen > 24 {
		next := cutRunes(rows[best].title, bestLen-overflow)
		if utf8.RuneCountInString(next) < 24 {
			next = cutRunes(rows[best].title, 24)
		}
		if next != rows[best].title && utf8.RuneCountInString(next) < bestLen {
			rows[best].title = next
			return true
		}
	}
	return false
}
