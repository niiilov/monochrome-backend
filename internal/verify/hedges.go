package verify

import (
	"strings"
	"unicode"

	"cyberdigest/internal/domain"
)

type hedgeMarker struct {
	key  string
	line string
	word bool
}

// hedgeMarkers are the stems the retelling must keep when an event has hedges.
// word means the key is a word prefix, not a free substring.
var hedgeMarkers = []hedgeMarker{
	{key: "предварительн", line: "По предварительным данным."},
	{key: "неподтвержд", line: "По неподтвержденным данным."},
	{key: "заяв", line: "Компания заявила об этом.", word: true},
	{key: "сообщил", line: "Источник сообщил об этом.", word: true},
	{key: "сообща", line: "Источник сообщает об этом.", word: true},
	{key: "по данным", line: "По данным источника."},
	{key: "возможно", line: "Возможно, сведения неполные.", word: true},
	{key: "оценоч", line: "По оценочным данным."},
	{key: "примерно", line: "Оценка приблизительная.", word: true},
	{key: "по мнению", line: "По мнению источника."},
	{key: "как утвержда", line: "Как утверждает источник."},
	{key: "предполага", line: "Источник предполагает это."},
}

func eventHedges(event domain.Event) []string {
	var out []string
	seen := map[string]bool{}
	for _, mention := range event.Mentions {
		for _, hedge := range mention.Hedges {
			hedge = strings.Join(strings.Fields(hedge), " ")
			if hedge == "" {
				continue
			}
			key := strings.ToLower(hedge)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, hedge)
		}
	}
	return out
}

func hedgeGaps(hedges []string, prose string) []string {
	var gaps []string
	seen := map[string]bool{}
	for _, hedge := range hedges {
		for _, gap := range gapsFor(hedge, prose) {
			if seen[gap] {
				continue
			}
			seen[gap] = true
			gaps = append(gaps, gap)
		}
	}
	return gaps
}

func gapsFor(hedge, prose string) []string {
	h := strings.ToLower(strings.Join(strings.Fields(hedge), " "))
	if h == "" {
		return nil
	}
	if hasAttribution(prose) {
		return nil
	}
	matched := false
	for _, marker := range hedgeMarkers {
		if !strings.Contains(h, marker.key) {
			continue
		}
		matched = true
		if markerHits(prose, marker) {
			return nil
		}
		return []string{hedge}
	}
	if matched {
		return nil
	}
	for _, marker := range hedgeMarkers {
		if markerHits(prose, marker) {
			return nil
		}
	}
	if strings.Contains(strings.ToLower(prose), h) {
		return nil
	}
	return []string{hedge}
}

func markerHits(prose string, marker hedgeMarker) bool {
	if marker.word {
		return hasWordPrefix(prose, marker.key)
	}
	return strings.Contains(strings.ToLower(prose), marker.key)
}

func hedgeFallback(hedge string) string {
	h := strings.ToLower(hedge)
	for _, marker := range hedgeMarkers {
		if strings.Contains(h, marker.key) {
			return marker.line
		}
	}
	// Do not paste raw extract fragments into the digest.
	return "По данным источника."
}

func hasWordPrefix(text, prefix string) bool {
	prefix = strings.ToLower(prefix)
	for _, word := range words(text) {
		if strings.HasPrefix(strings.ToLower(word), prefix) {
			return true
		}
	}
	return false
}

func words(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func lastRune(s string) (rune, int) {
	rs := []rune(s)
	if len(rs) == 0 {
		return 0, 0
	}
	return rs[len(rs)-1], len(rs)
}
