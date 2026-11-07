package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/summarize"
	"cyberdigest/internal/textutil"
)

// Digest renders digest.md.
// Text over DigestLimit is shortened: extra rationale and essence sentences go first.
// Source lines and the facts at the start of a field stay.
func Digest(m domain.DigestModel) string {
	return formatDigest(prepare(m))
}

func formatDigest(m domain.DigestModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Дайджест по кибербезопасности: %s, контрольное время %s\n\n", periodPhrase(m.Period), controlPhrase(m))
	b.WriteString(countLine(m))
	b.WriteByte('\n')
	if len(m.Items) == 0 {
		return b.String()
	}
	for _, item := range m.Items {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "## %d. %s\n", item.N, item.Title)
		fmt.Fprintf(&b, "Суть: %s\n", item.Essence)
		fmt.Fprintf(&b, "Последствия: %s\n", item.Consequences)
		fmt.Fprintf(&b, "Источники: %s\n", formatSources(item.Sources))
		fmt.Fprintf(&b, "Обоснование: %s\n", item.Rationale)
	}
	return b.String()
}

func countLine(m domain.DigestModel) string {
	n := len(m.Items)
	if n == 0 {
		return "Событий в выпуске: 0. " + emptyMessage(m.Shortfall)
	}
	if n < 5 {
		reason := strings.TrimSpace(m.Shortfall)
		if reason == "" {
			reason = fewerThanFive
		}
		return fmt.Sprintf("Событий в выпуске: %d. %s", n, finishSentence(reason))
	}
	return fmt.Sprintf("Событий в выпуске: %d", n)
}

func formatSources(sources []domain.SourceRef) string {
	if len(sources) == 0 {
		return noSources
	}
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, formatSource(source))
	}
	return strings.Join(parts, "; ")
}

func formatSource(source domain.SourceRef) string {
	name := source.Name
	if name == "" {
		name = unknownName
	}
	if role := strings.TrimSpace(source.Role); role != "" {
		name += " (" + role + ")"
	}
	date := source.Date
	if date == "" {
		date = unknownDate
	}
	id := source.ArticleID
	if id == "" {
		id = unknownID
	}
	return fmt.Sprintf("%s, %s, %s, ID: %s", name, date, source.URL, id)
}

func fitDigest(m *domain.DigestModel) {
	for step := 0; step < 400 && textutil.CountChars(formatDigest(*m)) > DigestLimit; step++ {
		if !trimDigestOnce(m) {
			return
		}
	}
}

func trimDigestOnce(m *domain.DigestModel) bool {
	if editItem(m.Items, func(item domain.DigestItem) int {
		n := len(summarize.SplitSentences(item.Rationale))
		if n > 1 {
			return n
		}
		return 0
	}, func(item *domain.DigestItem) bool {
		next, ok := firstSentence(item.Rationale)
		if !ok {
			return false
		}
		item.Rationale = next
		return true
	}) {
		return true
	}
	if editItem(m.Items, func(item domain.DigestItem) int {
		n := len(summarize.SplitSentences(item.Essence))
		if n > 2 {
			return n
		}
		return 0
	}, func(item *domain.DigestItem) bool {
		next, ok := dropLastSentence(item.Essence, 2)
		if !ok {
			return false
		}
		item.Essence = next
		return true
	}) {
		return true
	}
	if editItem(m.Items, func(item domain.DigestItem) int {
		if item.Consequences == summarize.NoConsequences {
			return 0
		}
		n := len(summarize.SplitSentences(item.Consequences))
		if n > 1 {
			return n
		}
		return 0
	}, func(item *domain.DigestItem) bool {
		next, ok := dropLastSentence(item.Consequences, 1)
		if !ok {
			return false
		}
		item.Consequences = next
		return true
	}) {
		return true
	}
	if editItem(m.Items, func(item domain.DigestItem) int {
		n := len(summarize.SplitSentences(item.Essence))
		if n > 1 {
			return n
		}
		return 0
	}, func(item *domain.DigestItem) bool {
		next, ok := dropLastSentence(item.Essence, 1)
		if !ok {
			return false
		}
		item.Essence = next
		return true
	}) {
		return true
	}
	over := textutil.CountChars(formatDigest(*m)) - DigestLimit
	return cutProse(m, over)
}

func editItem(items []domain.DigestItem, score func(domain.DigestItem) int, apply func(*domain.DigestItem) bool) bool {
	best, bestScore := -1, 0
	for i, item := range items {
		sc := score(item)
		if sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		return false
	}
	return apply(&items[best])
}

func firstSentence(s string) (string, bool) {
	parts := summarize.SplitSentences(s)
	if len(parts) <= 1 {
		return s, false
	}
	if parts[0] == s {
		return s, false
	}
	return parts[0], true
}

func dropLastSentence(s string, minKeep int) (string, bool) {
	parts := summarize.SplitSentences(s)
	if len(parts) <= minKeep {
		return s, false
	}
	next := summarize.JoinSentences(parts[:len(parts)-1])
	if next == s {
		return s, false
	}
	return next, true
}

func cutProse(m *domain.DigestModel, overflow int) bool {
	if overflow <= 0 {
		return false
	}
	type field struct {
		get   func(*domain.DigestItem) string
		set   func(*domain.DigestItem, string)
		floor int
		skip  func(*domain.DigestItem) bool
	}
	fields := []field{
		{func(it *domain.DigestItem) string { return it.Essence }, func(it *domain.DigestItem, s string) { it.Essence = s }, 80, nil},
		{func(it *domain.DigestItem) string { return it.Rationale }, func(it *domain.DigestItem, s string) { it.Rationale = s }, 40, nil},
		{func(it *domain.DigestItem) string { return it.Consequences }, func(it *domain.DigestItem, s string) { it.Consequences = s }, 40, func(it *domain.DigestItem) bool {
			return it.Consequences == summarize.NoConsequences
		}},
		{func(it *domain.DigestItem) string { return it.Title }, func(it *domain.DigestItem, s string) { it.Title = s }, 24, nil},
	}
	bestItem, bestField := -1, -1
	bestRoom := 0
	for i := range m.Items {
		item := &m.Items[i]
		for f, spec := range fields {
			if spec.skip != nil && spec.skip(item) {
				continue
			}
			room := utf8.RuneCountInString(spec.get(item)) - spec.floor
			if room > bestRoom {
				bestRoom = room
				bestItem = i
				bestField = f
			}
		}
	}
	if bestItem < 0 {
		return false
	}
	item := &m.Items[bestItem]
	spec := fields[bestField]
	cur := spec.get(item)
	n := utf8.RuneCountInString(cur)
	keep := n - overflow
	if keep < spec.floor {
		keep = spec.floor
	}
	next := cutRunes(cur, keep)
	if next == cur || utf8.RuneCountInString(next) >= n {
		return false
	}
	spec.set(item, next)
	return true
}

func cutRunes(s string, keep int) string {
	rs := []rune(s)
	if keep >= len(rs) {
		return s
	}
	if keep < 1 {
		keep = 1
	}
	cut := rs[:keep]
	for i := len(cut) - 1; i > len(cut)/2; i-- {
		if cut[i] == ' ' {
			cut = cut[:i]
			break
		}
	}
	return strings.TrimSpace(string(cut))
}
