package verify

import (
	"sort"
	"strings"
	"time"

	"mono/internal/domain"
	"mono/internal/textutil"
	"mono/internal/timeutil"
)

// Record is one dataset publication the digest is allowed to cite.
type Record struct {
	ID     string
	URL    string
	Title  string
	Source string
	Date   string
}

// Registry maps an article id to its url, title, source and publication date.
type Registry struct {
	byID map[string]Record
}

// NewRegistry builds a registry from the dataset. The first record wins for an id.
func NewRegistry(arts map[string]domain.Article) Registry {
	reg := Registry{byID: map[string]Record{}}
	if len(arts) == 0 {
		return reg
	}
	ids := make([]string, 0, len(arts))
	for id := range arts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		article := arts[id]
		key := strings.TrimSpace(article.ID)
		if key == "" {
			key = strings.TrimSpace(id)
		}
		if key == "" {
			continue
		}
		if _, exists := reg.byID[key]; exists {
			continue
		}
		reg.byID[key] = Record{
			ID:     key,
			URL:    strings.TrimSpace(article.URL),
			Title:  strings.TrimSpace(article.Title),
			Source: strings.TrimSpace(article.Source),
			Date:   publicationDate(article),
		}
	}
	return reg
}

// Get returns the record for id.
func (r Registry) Get(id string) (Record, bool) {
	if r.byID == nil {
		return Record{}, false
	}
	rec, ok := r.byID[strings.TrimSpace(id)]
	return rec, ok
}

// Len is the number of records.
func (r Registry) Len() int {
	return len(r.byID)
}

// AllowedURLs is the set passed to guard.CheckOutput.
// A trailing slash is accepted either way.
func (r Registry) AllowedURLs() map[string]bool {
	out := map[string]bool{}
	for _, rec := range r.byID {
		addURL(out, rec.URL)
	}
	return out
}

func publicationDate(article domain.Article) string {
	if article.PublishedAt != nil && !article.PublishedAt.IsZero() {
		return formatDay(*article.PublishedAt)
	}
	for _, date := range textutil.ExtractDates(article.PublishedRaw) {
		if len(date) == 10 {
			return date
		}
	}
	dates := textutil.ExtractDates(article.PublishedRaw)
	if len(dates) > 0 {
		return dates[0]
	}
	return ""
}

func formatDay(t time.Time) string {
	formatted := timeutil.FormatMSK(t)
	if len(formatted) >= 10 {
		return formatted[:10]
	}
	return formatted
}

func addURL(dst map[string]bool, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	dst[raw] = true
	trim := strings.TrimRight(raw, "/")
	if trim != "" {
		dst[trim] = true
		dst[trim+"/"] = true
	}
}

func sameURL(a, b string) bool {
	a = strings.TrimRight(strings.TrimSpace(a), "/")
	b = strings.TrimRight(strings.TrimSpace(b), "/")
	return a != "" && a == b
}

func urlKnown(raw string, allowed map[string]bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(allowed) == 0 {
		return false
	}
	if allowed[raw] {
		return true
	}
	trim := strings.TrimRight(raw, "/")
	return allowed[trim] || allowed[trim+"/"]
}
