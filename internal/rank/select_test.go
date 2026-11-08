package rank

import (
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/journal"
)

func TestSelectPreferredPutsKeywordMatchesFirst(t *testing.T) {
	events := []domain.Event{
		{ID: "E-high", Total: 3, ArticleIDs: []string{"high"}, PrimaryArticleID: "high"},
		{ID: "E-mid", Total: 2, ArticleIDs: []string{"mid"}, PrimaryArticleID: "mid"},
		{ID: "E-hit", Total: 0.5, ArticleIDs: []string{"hit"}, PrimaryArticleID: "hit"},
	}
	arts := map[string]domain.Article{
		"high": {ID: "high", Title: "Крупная кибератака", Text: "Пострадали банки."},
		"mid":  {ID: "mid", Title: "Утечка данных", Text: "Скомпрометированы записи."},
		"hit":  {ID: "hit", Title: "Шифровальщик в сети", Text: "Атака ransomware остановила завод."},
	}
	got := SelectPreferred(events, arts, []string{"ransomware"}, 2)
	if idsOf(got)[0] != "E-hit" || !got[0].Selected || got[0].Rank != 1 {
		t.Fatalf("keyword event %+v", got[0])
	}
	if idsOf(got)[1] != "E-high" || !got[1].Selected || got[1].Rank != 2 {
		t.Fatalf("second %+v", got[1])
	}
	if got[2].Selected || got[2].Rank != 0 {
		t.Fatalf("rest %+v", got[2])
	}
	plain := Select(events, arts, 2)
	if idsOf(plain)[0] != "E-high" {
		t.Fatalf("plain order %v", idsOf(plain))
	}
}

func TestSelectOrdersByTotalSourcesDateAndID(t *testing.T) {
	later := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	earlier := later.Add(-2 * time.Hour)
	events := []domain.Event{
		{ID: "E-low", Total: 1, IndependentSources: 9, ArticleIDs: []string{"low"}, PrimaryArticleID: "low"},
		{ID: "E-many", Total: 2, IndependentSources: 4, ArticleIDs: []string{"many"}, PrimaryArticleID: "many"},
		{ID: "E-late", Total: 2, IndependentSources: 1, ArticleIDs: []string{"late"}, PrimaryArticleID: "late"},
		{ID: "E-b", Total: 2, IndependentSources: 1, ArticleIDs: []string{"b"}, PrimaryArticleID: "b"},
		{ID: "E-a", Total: 2, IndependentSources: 1, ArticleIDs: []string{"a"}, PrimaryArticleID: "a"},
		{ID: "E-none", Total: 2, IndependentSources: 1, ArticleIDs: []string{"none"}, PrimaryArticleID: "none"},
	}
	arts := map[string]domain.Article{
		"low":  {ID: "low", PublishedAt: &later},
		"many": {ID: "many", PublishedAt: &earlier},
		"late": {ID: "late", PublishedAt: &later},
		"b":    {ID: "b", PublishedAt: &earlier},
		"a":    {ID: "a", PublishedAt: &earlier},
		"none": {ID: "none"},
	}
	input := append([]domain.Event(nil), events...)
	got := Select(events, arts, 10)
	if events[0].ID != "E-low" || events[0].Selected {
		t.Fatal("input slice was reordered or marked")
	}
	want := []string{"E-many", "E-late", "E-a", "E-b", "E-none", "E-low"}
	if stringsJoin(idsOf(got)) != stringsJoin(want) {
		t.Fatalf("order %v", idsOf(got))
	}
	again := Select(reverse(input), arts, 10)
	if stringsJoin(idsOf(again)) != stringsJoin(want) {
		t.Fatalf("order depends on input: %v", idsOf(again))
	}
	capped := Select(reverse(input), arts, 5)
	if len(capped) != 6 || !capped[0].Selected || capped[0].Rank != 1 || capped[4].Rank != 5 {
		t.Fatalf("cap ranks: %+v", ranksOf(capped))
	}
	if capped[5].Selected || capped[5].Rank != 0 || capped[5].ID != "E-low" {
		t.Fatalf("sixth %+v", capped[5])
	}
	j := journal.New()
	ApplyJournal(j, capped, nil)
	low, ok := j.Get("low")
	if !ok || low.Reason != "Уступило по приоритету: место 6 из 6, балл 1.00, часть данных неизвестна" {
		t.Fatalf("sixth reason %q", low.Reason)
	}
	none := Select(events, arts, 0)
	for _, ev := range none {
		if ev.Selected || ev.Rank != 0 {
			t.Fatalf("limit 0 selected %+v", ev)
		}
	}
}

func TestSelectFewerThanFiveAndEmpty(t *testing.T) {
	if Select(nil, nil, 5) != nil {
		t.Fatal("nil input")
	}
	if len(Select([]domain.Event{}, nil, 5)) != 0 {
		t.Fatal("empty input")
	}
	events := []domain.Event{
		{ID: "E2", Total: 1, ArticleIDs: []string{"b"}, PrimaryArticleID: "b"},
		{ID: "E3", Total: 3, ArticleIDs: []string{"c"}, PrimaryArticleID: "c"},
		{ID: "E1", Total: 2, ArticleIDs: []string{"a"}, PrimaryArticleID: "a"},
	}
	got := Select(events, nil, 5)
	if stringsJoin(idsOf(got)) != "E3,E1,E2" {
		t.Fatalf("order %v", idsOf(got))
	}
	for i, ev := range got {
		if !ev.Selected || ev.Rank != i+1 {
			t.Fatalf("event %s rank %d selected %v", ev.ID, ev.Rank, ev.Selected)
		}
	}
}

func TestApplyJournalLowerPriority(t *testing.T) {
	score := filledScores(2)
	events := []domain.Event{
		{
			ID: "E01", Total: 2, Selected: true, Rank: 1, Scores: score,
			Rationale:        "Утекли персональные данные тысячи клиентов.",
			ArticleIDs:       []string{"a", "b", "shared"},
			PrimaryArticleID: "a",
		},
		{
			ID: "E02", Total: 1, Scores: score,
			ArticleIDs:       []string{"shared", "c"},
			PrimaryArticleID: "c",
		},
	}
	j := journal.New()
	j.Set("a", domain.StatusIncluded, "E01", domain.ReasonSelected, "старая причина", "сбой чтения")
	j.Set("ctx", domain.StatusExcluded, "", domain.ReasonOutOfPeriod, "Публикация вне периода", "")
	ApplyJournal(nil, events, nil)
	ApplyJournal(j, nil, nil)
	ApplyJournal(j, events, map[string]string{"E02": "оценка не выполнена"})

	a, _ := j.Get("a")
	if a.Status != domain.StatusIncluded || a.ReasonCode != domain.ReasonSelected || a.Group != "E01" || a.Error != "сбой чтения" {
		t.Fatalf("primary %+v", a)
	}
	if a.Reason != "Событие вошло в выпуск: Утекли персональные данные тысячи клиентов." {
		t.Fatalf("reason %q", a.Reason)
	}
	b, _ := j.Get("b")
	if b.Status != domain.StatusMerged || b.Group != "E01" || b.ReasonCode != domain.ReasonMerged {
		t.Fatalf("merged %+v", b)
	}
	shared, _ := j.Get("shared")
	if shared.Status != domain.StatusMerged || shared.ReasonCode != domain.ReasonMerged || shared.Group != "E01" {
		t.Fatalf("shared article must stay with the selected event: %+v", shared)
	}
	if shared.Reason != "Сообщение о том же событии (E01), ссылка сохранена в источниках. Также события: E02." {
		t.Fatalf("shared reason %q", shared.Reason)
	}
	c, _ := j.Get("c")
	if c.Status != domain.StatusExcluded || c.ReasonCode != domain.ReasonLowerPriority || c.Group != "E02" {
		t.Fatalf("lower %+v", c)
	}
	if c.Reason != "Уступило по приоритету: место 2 из 2, балл 1.00" || c.Error != "оценка не выполнена" {
		t.Fatalf("lower reason %q err %q", c.Reason, c.Error)
	}
	ctxRow, _ := j.Get("ctx")
	if ctxRow.ReasonCode != domain.ReasonOutOfPeriod || ctxRow.Status != domain.StatusExcluded {
		t.Fatalf("context changed: %+v", ctxRow)
	}
}

func TestApplyJournalPartialFlag(t *testing.T) {
	events := []domain.Event{{
		ID: "E01", Total: 1.5, Selected: true, Rank: 1,
		Rationale:        "Сведения о масштабе не приведены.",
		ArticleIDs:       []string{"a"},
		PrimaryArticleID: "a",
		Scores:           domain.Scores{Impact: ip(2)},
	}}
	j := journal.New()
	ApplyJournal(j, events, nil)
	row, _ := j.Get("a")
	if row.Status != domain.StatusIncluded || !traceHas(row, UnknownDataNote) {
		t.Fatalf("row %+v", row)
	}
}

func filledScores(n int) domain.Scores {
	return domain.Scores{Impact: ip(n), Scale: ip(n), Urgency: ip(n), Relevance: ip(n)}
}

func idsOf(events []domain.Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.ID
	}
	return out
}

func ranksOf(events []domain.Event) []int {
	out := make([]int, len(events))
	for i, ev := range events {
		out[i] = ev.Rank
	}
	return out
}

func reverse(events []domain.Event) []domain.Event {
	out := append([]domain.Event(nil), events...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func stringsJoin(items []string) string {
	return joinComma(items)
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item
	}
	return out
}
