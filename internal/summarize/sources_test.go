package summarize

import (
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/timeutil"
)

func TestSourceRefsOrderAndRoles(t *testing.T) {
	published := time.Date(2026, 11, 6, 15, 0, 0, 0, timeutil.MSK)
	arts := map[string]domain.Article{
		"v1":  {ID: "v1", Source: "Банк", URL: "https://vendor.example/a", SourceKind: domain.SourceVendor, PublishedAt: &published},
		"m1":  {ID: "m1", Source: "Газета", URL: "https://media.example/b", SourceKind: domain.SourceMedia, PublishedAt: &published},
		"r1":  {ID: "r1", Source: "Лента", URL: "https://media.example/c", SourceKind: domain.SourceMedia, PublishedAt: &published},
		"ctx": {ID: "ctx", Source: "Архив", URL: "https://old.example/z", SourceKind: domain.SourceMedia},
	}
	event := domain.Event{
		ID:                "E1",
		PrimaryArticleID:  "m1",
		ArticleIDs:        []string{"m1", "r1", "v1"},
		ContextArticleIDs: []string{"ctx"},
		Mentions: []domain.Mention{
			{ArticleID: "r1", IsReprint: true},
		},
	}
	refs := SourceRefs(event, arts)
	if len(refs) != 3 {
		t.Fatalf("источников %d: %+v", len(refs), refs)
	}
	if refs[0].ArticleID != "v1" || refs[0].Role != RolePrimary {
		t.Fatalf("первичный: %+v", refs[0])
	}
	if refs[1].ArticleID != "m1" || refs[1].Role != RoleMedia {
		t.Fatalf("сми: %+v", refs[1])
	}
	if refs[2].ArticleID != "r1" || refs[2].Role != RoleReprint {
		t.Fatalf("перепечатка: %+v", refs[2])
	}
	if refs[0].Date != "06.11.2026" {
		t.Fatalf("дата %q", refs[0].Date)
	}
	for _, ref := range refs {
		if ref.ArticleID == "ctx" {
			t.Fatal("контекст попал в источники")
		}
	}
}

func TestSourceRefsMissingDate(t *testing.T) {
	refs := SourceRefs(domain.Event{ArticleIDs: []string{"x"}}, map[string]domain.Article{
		"x": {ID: "x", Source: "Неизвестный сайт", URL: "https://x.example/1"},
	})
	if len(refs) != 1 || refs[0].Date != "дата не указана" || refs[0].Role != RoleMedia {
		t.Fatalf("%+v", refs)
	}
}
