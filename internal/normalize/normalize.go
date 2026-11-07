package normalize

import (
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/ingest"
)

// Apply cleans Title and Text, keeps RawText, and fills SourceKind from the source registry.
// When RawText is empty, the current Text is stored as RawText before cleaning.
func Apply(a *domain.Article) {
	if a == nil {
		return
	}
	if a.RawText == "" {
		a.RawText = a.Text
	}
	a.Text = Clean(a.RawText)
	a.Title = oneLine(Clean(a.Title))
	a.SourceKind = ingest.SourceKindOf(a.Source, a.URL)
}

// ApplyAll runs Apply on every article.
func ApplyAll(articles []domain.Article) {
	for i := range articles {
		Apply(&articles[i])
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
