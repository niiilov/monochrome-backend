package rank

import (
	"strings"
	"time"

	"mono/internal/domain"
	"mono/internal/topic"
)

// Select orders events and marks at most limit of them as selected.
// Tie-break, after Total: more IndependentSources, then a later publication
// among ArticleIDs, then the event ID ascending.
// Publication time comes from arts. The section 5.7 signature has no article
// map; without it the date tie-break cannot be computed.
// Rank is 1..N for the selected events and 0 for the rest.
// An empty list returns nil. The input slice is not reordered.
func Select(events []domain.Event, arts map[string]domain.Article, limit int) []domain.Event {
	return SelectPreferred(events, arts, nil, limit)
}

// SelectPreferred is Select with user keywords as the first ordering key.
// An event whose own articles contain any keyword stays above events that do not,
// even when its Total is lower. Order inside each group is the usual one.
func SelectPreferred(events []domain.Event, arts map[string]domain.Article, keywords []string, limit int) []domain.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]domain.Event, len(events))
	copy(out, events)
	sortEvents(out, arts, keywords)
	if limit < 0 {
		limit = 0
	}
	for i := range out {
		if i < limit {
			out[i].Selected = true
			out[i].Rank = i + 1
		} else {
			out[i].Selected = false
			out[i].Rank = 0
		}
	}
	return out
}

func sortEvents(events []domain.Event, arts map[string]domain.Article, keywords []string) {
	// Straight insertion keeps the order function obvious and avoids a
	// dependency on sort for a list that is at most a handful of events.
	for i := 1; i < len(events); i++ {
		item := events[i]
		j := i
		for j > 0 && rankBefore(item, events[j-1], arts, keywords) {
			events[j] = events[j-1]
			j--
		}
		events[j] = item
	}
}

// rankBefore reports whether a ranks above b.
func rankBefore(a, b domain.Event, arts map[string]domain.Article, keywords []string) bool {
	if len(keywords) > 0 {
		am := eventMatchesKeywords(a, arts, keywords)
		bm := eventMatchesKeywords(b, arts, keywords)
		if am != bm {
			return am
		}
	}
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	if a.IndependentSources != b.IndependentSources {
		return a.IndependentSources > b.IndependentSources
	}
	ta := latestPub(a, arts)
	tb := latestPub(b, arts)
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID < b.ID
}

func eventMatchesKeywords(ev domain.Event, arts map[string]domain.Article, keywords []string) bool {
	if len(keywords) == 0 || arts == nil {
		return false
	}
	seen := map[string]bool{}
	check := func(id string) bool {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
		article, ok := arts[id]
		if !ok {
			return false
		}
		return topic.MatchesKeywords(article.Title, article.Text, keywords)
	}
	for _, id := range ev.ArticleIDs {
		if check(id) {
			return true
		}
	}
	return check(ev.PrimaryArticleID)
}

func latestPub(ev domain.Event, arts map[string]domain.Article) time.Time {
	var best time.Time
	seen := map[string]bool{}
	consider := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || arts == nil {
			return
		}
		seen[id] = true
		a, ok := arts[id]
		if !ok || a.PublishedAt == nil {
			return
		}
		if a.PublishedAt.After(best) {
			best = *a.PublishedAt
		}
	}
	for _, id := range ev.ArticleIDs {
		consider(id)
	}
	consider(ev.PrimaryArticleID)
	return best
}
