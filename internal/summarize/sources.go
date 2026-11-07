package summarize

import (
	"sort"
	"strings"
	"time"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/timeutil"
)

const dateUnknown = "дата не указана"

// SourceRefs lists publications of one event.
// A vendor or a regulator is first and gets the primary role.
// ContextArticleIDs are not included: those publications stay out of the digest text.
func SourceRefs(event domain.Event, arts map[string]domain.Article) []domain.SourceRef {
	ids := articleIDs(event)
	if len(ids) == 0 {
		return nil
	}
	mentions := mentionsByArticle(event)
	type row struct {
		id      string
		ref     domain.SourceRef
		tier    int
		primary bool
	}
	rows := make([]row, 0, len(ids))
	for _, id := range ids {
		article := lookupArticle(arts, id)
		reprint := isReprint(mentions[id])
		role, tier := roleOf(article, reprint)
		rows = append(rows, row{
			id: id,
			ref: domain.SourceRef{
				ArticleID: article.ID,
				Name:      sourceName(article),
				Date:      formatDate(article.PublishedAt),
				URL:       strings.TrimSpace(article.URL),
				Role:      role,
			},
			tier:    tier,
			primary: id == strings.TrimSpace(event.PrimaryArticleID),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].tier != rows[j].tier {
			return rows[i].tier < rows[j].tier
		}
		if rows[i].primary != rows[j].primary {
			return rows[i].primary
		}
		return rows[i].id < rows[j].id
	})
	out := make([]domain.SourceRef, len(rows))
	for i, row := range rows {
		out[i] = row.ref
	}
	return out
}

func articleIDs(event domain.Event) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range event.ArticleIDs {
		add(id)
	}
	for _, mention := range event.Mentions {
		add(mention.ArticleID)
	}
	if len(ids) == 0 {
		add(event.PrimaryArticleID)
	}
	return ids
}

func mentionsByArticle(event domain.Event) map[string][]domain.Mention {
	out := map[string][]domain.Mention{}
	for _, mention := range event.Mentions {
		id := strings.TrimSpace(mention.ArticleID)
		out[id] = append(out[id], mention)
	}
	return out
}

func isReprint(mentions []domain.Mention) bool {
	for _, mention := range mentions {
		if mention.IsReprint {
			return true
		}
	}
	return false
}

func roleOf(article domain.Article, reprint bool) (string, int) {
	switch article.SourceKind {
	case domain.SourceVendor, domain.SourceRegulator:
		return RolePrimary, 0
	}
	if reprint {
		return RoleReprint, 2
	}
	return RoleMedia, 1
}

func lookupArticle(arts map[string]domain.Article, id string) domain.Article {
	if arts != nil {
		if article, ok := arts[id]; ok {
			if strings.TrimSpace(article.ID) == "" {
				article.ID = id
			}
			return article
		}
	}
	return domain.Article{ID: id}
}

func sourceName(article domain.Article) string {
	name := oneLine(article.Source)
	if name == "" {
		return "источник не указан"
	}
	return name
}

func formatDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return dateUnknown
	}
	formatted := timeutil.FormatMSK(*t)
	if len(formatted) >= 10 {
		return formatted[:10]
	}
	return formatted
}
