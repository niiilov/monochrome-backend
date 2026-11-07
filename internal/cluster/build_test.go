package cluster

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/extract"
	"cyberdigest/internal/journal"
	"cyberdigest/internal/llm/mock"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/textutil"
	"cyberdigest/internal/timeutil"
)

func TestRepeatBecomesOneEvent(t *testing.T) {
	arts := map[string]domain.Article{
		"a": article("a", "SecurityLab", domain.SourceMedia, at(2026, 11, 6, 18, 40), "Атака на Горсвет-Энерго оставила без света 12000 абонентов."),
		"b": article("b", "Хакер", domain.SourceMedia, at(2026, 11, 6, 21, 5), "Горсвет-Энерго подтвердил атаку, без света 12000 абонентов."),
		"c": article("c", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 12), "Горсвет-Энерго все еще восстанавливает сеть после атаки."),
	}
	mentions := []domain.Mention{
		mention("M-a-1", "a", "Горсвет-Энерго", "Атака на Горсвет-Энерго оставила без света 12000 абонентов."),
		mention("M-b-1", "b", "Горсвет-Энерго", "Горсвет-Энерго подтвердил атаку, без света 12000 абонентов."),
		mention("M-c-1", "c", "Горсвет-Энерго", "Горсвет-Энерго все еще восстанавливает сеть после атаки."),
	}
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Один инцидент в Горсвет-Энерго", nil))
	res, err := BuildResult(context.Background(), m, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events %d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.ID != "E01" || ev.PrimaryArticleID != "a" || len(ev.ArticleIDs) != 3 {
		t.Fatalf("%+v", ev)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !contains(ev.ArticleIDs, id) {
			t.Fatalf("missing %s in %v", id, ev.ArticleIDs)
		}
	}
	assertQuotes(t, res.Events, arts)

	j := journal.New()
	ApplyJournal(j, res, testPeriod(), []domain.Article{arts["a"], arts["b"], arts["c"]}, nil)
	if err := j.Validate([]string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, j, "a", domain.StatusIncluded, "E01", "вошло в выпуск")
	assertStatus(t, j, "b", domain.StatusMerged, "E01", "Сообщение о том же событии (E01)")
	assertStatus(t, j, "c", domain.StatusMerged, "E01", "Сообщение о том же событии (E01)")
}

func TestStableEventIDs(t *testing.T) {
	early := article("early", "Хакер", domain.SourceMedia, at(2026, 11, 6, 13, 0), "Утечка в контуре Альфа-Север затронула клиентов.")
	late := article("late", "CNews", domain.SourceMedia, at(2026, 11, 7, 11, 0), "DDoS на портал Вектор-Софт продолжается второй час.")
	arts := map[string]domain.Article{"early": early, "late": late}
	forward := []domain.Mention{
		mention("M-early-1", "early", "Альфа-Север", "Утечка в контуре Альфа-Север затронула клиентов."),
		mention("M-late-1", "late", "Вектор-Софт", "DDoS на портал Вектор-Софт продолжается второй час."),
	}
	backward := []domain.Mention{forward[1], forward[0]}
	first, err := Build(context.Background(), nil, forward, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(context.Background(), nil, backward, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	third, err := Build(context.Background(), nil, forward, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != "E01" || first[0].PrimaryArticleID != "early" || first[1].ID != "E02" || first[1].PrimaryArticleID != "late" {
		t.Fatalf("%+v", first)
	}
	if second[0].ID != first[0].ID || second[0].PrimaryArticleID != first[0].PrimaryArticleID || second[1].PrimaryArticleID != first[1].PrimaryArticleID {
		t.Fatalf("order changed: %+v vs %+v", first, second)
	}
	if third[0].ID != first[0].ID || third[1].ID != first[1].ID {
		t.Fatalf("ids changed between runs: %+v %+v", first, third)
	}
}

func TestUpdateOldAndFresh(t *testing.T) {
	old := article("old-1", "SecurityLab", domain.SourceMedia, at(2026, 11, 1, 9, 0), "1 ноября компания Северный Мост сообщила о сбое портала. Утечки данных не было.")
	freshNew := article("fresh-new", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Северный Мост выпустил патч 1.2 после сбоя портала.")
	freshOld := article("fresh-old", "Хакер", domain.SourceMedia, at(2026, 11, 7, 10, 0), "Северный Мост по-прежнему сообщает только о сбое портала.")
	arts := map[string]domain.Article{"fresh-new": freshNew, "fresh-old": freshOld, "old-1": old}
	mentions := []domain.Mention{
		mention("M-fresh-new-1", "fresh-new", "Северный Мост", "Северный Мост выпустил патч 1.2 после сбоя портала."),
		mention("M-fresh-old-1", "fresh-old", "Северный Мост", "Северный Мост по-прежнему сообщает только о сбое портала."),
	}
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Тот же сбой", nil))
	m.When("update_check", "fresh-new", `{"new_facts":["Выпущен патч 1.2"]}`)
	m.When("update_check", "fresh-old", `{"new_facts":[]}`)
	res, err := BuildResult(context.Background(), m, mentions, arts, []domain.Article{old}, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events %+v stale %v", res.Events, res.Stale)
	}
	ev := res.Events[0]
	if ev.ID != "E01" || len(ev.ArticleIDs) != 1 || ev.ArticleIDs[0] != "fresh-new" {
		t.Fatalf("articles %+v", ev.ArticleIDs)
	}
	if len(ev.ContextArticleIDs) != 1 || ev.ContextArticleIDs[0] != "old-1" {
		t.Fatalf("context %+v", ev.ContextArticleIDs)
	}
	if len(ev.Mentions) != 1 || !contains(ev.Mentions[0].NewFacts, "Выпущен патч 1.2") {
		t.Fatalf("new facts %+v", ev.Mentions)
	}
	if !contains(res.Stale, "fresh-old") {
		t.Fatalf("stale %v", res.Stale)
	}
	for _, c := range ev.Conflicts {
		if contains(c.ArticleIDs, "fresh-old") {
			t.Fatalf("stale article stayed in a conflict: %+v", c)
		}
	}
	j := journal.New()
	in := []domain.Article{freshNew, freshOld}
	ApplyJournal(j, res, testPeriod(), in, []domain.Article{old})
	if err := j.Validate([]string{"fresh-new", "fresh-old", "old-1"}); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, j, "old-1", domain.StatusExcluded, "", "вне периода")
	assertStatus(t, j, "fresh-new", domain.StatusIncluded, "E01", "вошло в выпуск")
	assertStatus(t, j, "fresh-old", domain.StatusExcluded, "", "без новых фактов")
	oldRow, _ := j.Get("old-1")
	if oldRow.Group != "" {
		t.Fatalf("context group %q", oldRow.Group)
	}
	found := false
	for _, note := range oldRow.Trace {
		if strings.Contains(note.Note, "E01") {
			found = true
		}
	}
	if !found {
		t.Fatalf("context trace %+v", oldRow.Trace)
	}
}

func TestReferencesEarlierWithoutContext(t *testing.T) {
	arts := map[string]domain.Article{
		"oldish": article("oldish", "Хакер", domain.SourceMedia, at(2026, 11, 7, 8, 0), "Повторяем вчерашнее сообщение о Северном Мосте."),
		"newer":  article("newer", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Северный Мост назвал новое число пострадавших систем: 40."),
	}
	stale := mention("M-oldish-1", "oldish", "Северный Мост", "Повторяем вчерашнее сообщение о Северном Мосте.")
	stale.ReferencesEarlier = true
	fresh := mention("M-newer-1", "newer", "Другой-Контур", "Северный Мост назвал новое число пострадавших систем: 40.")
	fresh.ReferencesEarlier = true
	fresh.NewFacts = []string{"пострадали 40 систем"}
	res, err := BuildResult(context.Background(), nil, []domain.Mention{stale, fresh}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || res.Events[0].PrimaryArticleID != "newer" {
		t.Fatalf("%+v", res.Events)
	}
	if !contains(res.Stale, "oldish") {
		t.Fatalf("stale %v", res.Stale)
	}
	if !contains(res.Events[0].Mentions[0].NewFacts, "пострадали 40 систем") {
		t.Fatalf("facts %+v", res.Events[0].Mentions[0].NewFacts)
	}
}

func TestReprintStaysBesideOriginal(t *testing.T) {
	fact := "Positive Technologies описала CVE-2026-22110 в программе Склад-Учёт, оценка CVSS 9.1."
	arts := map[string]domain.Article{
		"a": article("a", "Positive Technologies", domain.SourceVendor, at(2026, 11, 7, 8, 0), fact),
		"b": article("b", "Лаборатория Касперского", domain.SourceVendor, at(2026, 11, 7, 11, 0), "Перепечатка: "+fact),
	}
	orig := mention("M-a-1", "a", "Склад-Учёт", fact)
	reprint := mention("M-b-1", "b", "Склад-Учёт", "Перепечатка: "+fact)
	reprint.ReferencesEarlier = true
	reprint.IsReprint = true
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "перепечатка того же бюллетеня", nil))
	res, err := BuildResult(context.Background(), m, []domain.Mention{orig, reprint}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events %+v stale %v", res.Events, res.Stale)
	}
	ids := res.Events[0].ArticleIDs
	if !contains(ids, "a") || !contains(ids, "b") {
		t.Fatalf("reprint dropped: %v stale %v", ids, res.Stale)
	}
	if contains(res.Stale, "b") {
		t.Fatalf("reprint marked stale: %v", res.Stale)
	}
}

func TestLaterContextIsNotEarlier(t *testing.T) {
	body := "Шлюз Линия-VPN версий ниже 5.4.1 получил патч против обхода второго фактора."
	fresh := article("fresh", "Инфобезопасность", domain.SourceMedia, at(2026, 11, 7, 12, 0), body)
	later := article("later", "Overclockers", domain.SourceMedia, at(2026, 11, 7, 12, 1), "Комментарий повторяет: "+body+" Новых жертв нет.")
	evMention := mention("M-fresh-1", "fresh", "Линия-VPN", body)
	raw := matchContext(domain.Event{Mentions: []domain.Mention{evMention}}, []domain.Article{later}, config.Default().Cluster)
	if len(raw) == 0 {
		t.Fatal("fixture does not match the later note")
	}
	m := mock.New()
	m.When("update_check", "fresh", `{"new_facts":[]}`)
	res, err := BuildResult(context.Background(), m, []domain.Mention{evMention}, map[string]domain.Article{"fresh": fresh}, []domain.Article{later}, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || contains(res.Stale, "fresh") {
		t.Fatalf("later note staled the event: %+v stale %v", res.Events, res.Stale)
	}
	if len(res.Events[0].ContextArticleIDs) != 0 {
		t.Fatalf("later note stored as context: %v", res.Events[0].ContextArticleIDs)
	}
	if m.CallsFor("update_check") != 0 {
		t.Fatalf("update_check calls %d", m.CallsFor("update_check"))
	}
}

func TestNumberAndDateConflict(t *testing.T) {
	qa := "Медкарта-Онлайн назвала 840000 записей, инцидент 05.11.2026."
	qb := "Медкарта-Онлайн назвала 1200000 записей, инцидент 06.11.2026."
	arts := map[string]domain.Article{
		"a": article("a", "SecurityLab", domain.SourceMedia, at(2026, 11, 6, 15, 0), qa),
		"b": article("b", "Хакер", domain.SourceMedia, at(2026, 11, 6, 16, 0), qb),
	}
	mentions := []domain.Mention{
		mention("M-a-1", "a", "Медкарта-Онлайн", qa),
		mention("M-b-1", "b", "Медкарта-Онлайн", qb),
	}
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Одна утечка", []domain.Conflict{{
		Field: "число", Description: "a сообщает 840000, b сообщает 1200000", ArticleIDs: []string{"a", "b"},
	}}))
	res, err := BuildResult(context.Background(), m, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("%d events", len(res.Events))
	}
	ev := res.Events[0]
	if len(ev.ArticleIDs) != 2 || len(ev.Mentions) != 2 {
		t.Fatalf("links dropped %+v", ev)
	}
	var numbers, dates int
	for _, c := range ev.Conflicts {
		if !contains(c.ArticleIDs, "a") || !contains(c.ArticleIDs, "b") {
			t.Fatalf("conflict ids %+v", c)
		}
		if c.Field == "число" {
			numbers++
		}
		if c.Field == "дата" {
			dates++
		}
	}
	if numbers == 0 || dates == 0 {
		t.Fatalf("conflicts %+v", ev.Conflicts)
	}
	left, right := ev.Mentions[0].Facts[0].Text, ev.Mentions[1].Facts[0].Text
	if !strings.Contains(left+right, "840000") || !strings.Contains(left+right, "1200000") {
		t.Fatal("a version of the number was discarded")
	}
	assertQuotes(t, res.Events, arts)
}

func TestSubsetOfNumbersIsNotConflict(t *testing.T) {
	qa := "Пострадало 840000 записей компании Лента-Софт."
	qb := "Пострадало 840000 записей компании Лента-Софт, затем еще 50 серверов."
	arts := map[string]domain.Article{
		"a": article("a", "Хакер", domain.SourceMedia, at(2026, 11, 6, 15, 0), qa),
		"b": article("b", "CNews", domain.SourceMedia, at(2026, 11, 6, 16, 0), qb),
	}
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Одно событие", nil))
	res, err := BuildResult(context.Background(), m, []domain.Mention{
		mention("M-a-1", "a", "Лента-Софт", qa),
		mention("M-b-1", "b", "Лента-Софт", qb),
	}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || len(res.Events[0].Conflicts) != 0 {
		t.Fatalf("conflicts %+v", res.Events[0].Conflicts)
	}
}

func TestTwoEventsInOneArticle(t *testing.T) {
	text := "Уязвимость CVE-2026-19002 в шлюзе. Отдельно утекли 2200 учетных записей."
	arts := map[string]domain.Article{
		"a": article("a", "Хабр", domain.SourceAuthor, at(2026, 11, 7, 7, 30), text),
	}
	mentions := []domain.Mention{
		mention("M-a-1", "a", "CVE-2026-19002", "Уязвимость CVE-2026-19002 в шлюзе."),
		mention("M-a-2", "a", "учетные записи", "Отдельно утекли 2200 учетных записей."),
	}
	res, err := BuildResult(context.Background(), nil, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 || res.Events[0].ID != "E01" || res.Events[1].ID != "E02" {
		t.Fatalf("%+v", res.Events)
	}
	j := journal.New()
	ApplyJournal(j, res, testPeriod(), []domain.Article{arts["a"]}, nil)
	row, ok := j.Get("a")
	if !ok {
		t.Fatal("missing row")
	}
	if row.Status != domain.StatusIncluded || row.Group != "E01" || !strings.Contains(row.Reason, "E02") {
		t.Fatalf("%+v", row)
	}
	assertQuotes(t, res.Events, arts)
}

func TestReprintDoesNotIncreaseIndependentSources(t *testing.T) {
	arts := map[string]domain.Article{
		"pt":   article("pt", "Positive Technologies", domain.SourceVendor, at(2026, 11, 7, 8, 5), "Positive Technologies раскрыла CVE-2026-18421 в Архив-Про."),
		"news": article("news", "CNews", domain.SourceMedia, at(2026, 11, 7, 10, 20), "CNews описывает CVE-2026-18421 в Архив-Про своими словами."),
		"kas":  article("kas", "Лаборатория Касперского", domain.SourceVendor, at(2026, 11, 7, 11, 5), "Лаборатория Касперского перепечатала бюллетень про CVE-2026-18421."),
	}
	pt := mention("M-pt-1", "pt", "CVE-2026-18421", "Positive Technologies раскрыла CVE-2026-18421 в Архив-Про.")
	pt.IsCompanyStatement = true
	news := mention("M-news-1", "news", "CVE-2026-18421", "CNews описывает CVE-2026-18421 в Архив-Про своими словами.")
	kas := mention("M-kas-1", "kas", "CVE-2026-18421", "Лаборатория Касперского перепечатала бюллетень про CVE-2026-18421.")
	kas.IsCompanyStatement = true
	kas.IsReprint = true
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Один бюллетень", nil))
	res, err := BuildResult(context.Background(), m, []domain.Mention{kas, news, pt}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("%d", len(res.Events))
	}
	ev := res.Events[0]
	if ev.PrimaryArticleID != "pt" {
		t.Fatalf("primary %s, want the earlier vendor", ev.PrimaryArticleID)
	}
	if ev.IndependentSources != 2 || len(ev.ArticleIDs) != 3 {
		t.Fatalf("sources %d ids %v", ev.IndependentSources, ev.ArticleIDs)
	}
	if ev.ArticleIDs[0] != "pt" {
		t.Fatalf("primary is not first: %v", ev.ArticleIDs)
	}
}

func TestVendorBeatsEarlierMedia(t *testing.T) {
	arts := map[string]domain.Article{
		"news":   article("news", "Хакер", domain.SourceMedia, at(2026, 11, 6, 18, 0), "СМИ раньше написало про сбой Линия-Связь."),
		"vendor": article("vendor", "ИнфоТеКС", domain.SourceVendor, at(2026, 11, 7, 10, 0), "ИнфоТеКС подтвердил сбой Линия-Связь."),
	}
	m := mock.New()
	m.When("cluster_confirm", "", confirmJSON(t, true, "Один сбой", nil))
	events, err := Build(context.Background(), m, []domain.Mention{
		mention("M-news-1", "news", "Линия-Связь", "СМИ раньше написало про сбой Линия-Связь."),
		mention("M-vendor-1", "vendor", "Линия-Связь", "ИнфоТеКС подтвердил сбой Линия-Связь."),
	}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].PrimaryArticleID != "vendor" || events[0].ArticleIDs[0] != "vendor" {
		t.Fatalf("%+v", events[0])
	}
}

func TestThresholdAndPairLimit(t *testing.T) {
	arts := map[string]domain.Article{
		"a": article("a", "Хакер", domain.SourceMedia, at(2026, 11, 7, 8, 0), "Текст про Альфа-Север."),
		"b": article("b", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Текст про Альфа-Север иначе."),
		"c": article("c", "SecurityLab", domain.SourceMedia, at(2026, 11, 6, 13, 0), "Самая ранняя заметка про Альфа-Север."),
	}
	mentions := []domain.Mention{
		mention("M-a-1", "a", "Альфа-Север", "Текст про Альфа-Север."),
		mention("M-b-1", "b", "Альфа-Север", "Текст про Альфа-Север иначе."),
		mention("M-c-1", "c", "Альфа-Север", "Самая ранняя заметка про Альфа-Север."),
	}
	blocked := mock.New()
	cfg := config.Default().Cluster
	cfg.SimilarityThreshold = 1
	events, err := Build(context.Background(), blocked, mentions, arts, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Calls() != 0 || len(events) != 3 {
		t.Fatalf("calls %d events %d", blocked.Calls(), len(events))
	}

	limited := mock.New()
	limited.When("cluster_confirm", "", confirmJSON(t, true, "пара", nil))
	cfg = config.Default().Cluster
	cfg.MaxPairsPerArticle = 1
	res, err := BuildResult(context.Background(), limited, mentions, arts, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if limited.CallsFor("cluster_confirm") != 1 {
		t.Fatalf("calls %d", limited.CallsFor("cluster_confirm"))
	}
	if len(res.Events) != 2 || res.Events[0].PrimaryArticleID != "c" || res.Events[0].ID != "E01" {
		t.Fatalf("%+v", idsOf(res.Events))
	}
}

func TestRejectPairAndConfirmError(t *testing.T) {
	arts := map[string]domain.Article{
		"a": article("a", "Хакер", domain.SourceMedia, at(2026, 11, 7, 8, 0), "Сбой Альфа-Север."),
		"b": article("b", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Другой инцидент Альфа-Север."),
	}
	mentions := []domain.Mention{
		mention("M-a-1", "a", "Альфа-Север", "Сбой Альфа-Север."),
		mention("M-b-1", "b", "Альфа-Север", "Другой инцидент Альфа-Север."),
	}
	no := mock.New()
	no.When("cluster_confirm", "", confirmJSON(t, false, "Разные инциденты", nil))
	events, err := Build(context.Background(), no, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("merged despite rejection: %d", len(events))
	}

	bad := mock.New()
	bad.Fail("cluster_confirm", "", context.DeadlineExceeded)
	res, err := BuildResult(context.Background(), bad, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 || res.Errors["a"] == "" {
		t.Fatalf("%d events, errors %v", len(res.Events), res.Errors)
	}
}

func TestUnrelatedMentionsDoNotCallModel(t *testing.T) {
	arts := map[string]domain.Article{
		"a": article("a", "Хакер", domain.SourceMedia, at(2026, 11, 7, 8, 0), "Утечка записей клиентов банка Альфа-Север."),
		"b": article("b", "CNews", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Уязвимость прошивки маршрутизатора Вектор-Софт."),
	}
	m := mock.New()
	events, err := Build(context.Background(), m, []domain.Mention{
		mention("M-a-1", "a", "Альфа-Север", "Утечка записей клиентов банка Альфа-Север."),
		mention("M-b-1", "b", "Вектор-Софт", "Уязвимость прошивки маршрутизатора Вектор-Софт."),
	}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if m.Calls() != 0 || len(events) != 2 {
		t.Fatalf("calls %d events %d", m.Calls(), len(events))
	}
}

func TestInsufficientTextDoesNotFormEvent(t *testing.T) {
	arts := map[string]domain.Article{
		"short": article("short", "Хакер", domain.SourceMedia, at(2026, 11, 7, 9, 0), "Мало."),
		"full":  article("full", "CNews", domain.SourceMedia, at(2026, 11, 7, 10, 0), "Подробная атака на контур Гамма-Сеть."),
	}
	short := mention("M-short-1", "short", "Гамма-Сеть", "Мало.")
	short.SufficientText = false
	res, err := BuildResult(context.Background(), nil, []domain.Mention{
		short,
		mention("M-full-1", "full", "Гамма-Сеть", "Подробная атака на контур Гамма-Сеть."),
	}, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || res.Events[0].PrimaryArticleID != "full" || !contains(res.Insufficient, "short") {
		t.Fatalf("%+v insufficient %v", res.Events, res.Insufficient)
	}
	j := journal.New()
	ApplyJournal(j, res, testPeriod(), []domain.Article{arts["short"], arts["full"]}, nil)
	assertStatus(t, j, "short", domain.StatusExcluded, "", "недостаточно")
	assertStatus(t, j, "full", domain.StatusIncluded, "E01", "вошло в выпуск")
}

func TestPipelineQuotesAreSubstrings(t *testing.T) {
	aText := pad("Атака шифровальщика остановила Горсвет-Энерго. Без света остались 12000 абонентов в 14 городах.")
	bText := pad("Второй источник подтвердил атаку на Горсвет-Энерго. Без света остались 12000 абонентов.")
	cText := pad("Третий источник повторяет сообщение об атаке на Горсвет-Энерго и сохраняет ссылку.")
	articles := []domain.Article{
		{ID: "a", Title: "Атака", Text: aText, Source: "SecurityLab", SourceKind: domain.SourceMedia, PublishedAt: at(2026, 11, 6, 18, 40)},
		{ID: "b", Title: "Подтверждение", Text: bText, Source: "Хакер", SourceKind: domain.SourceMedia, PublishedAt: at(2026, 11, 6, 21, 5)},
		{ID: "c", Title: "Повтор", Text: cText, Source: "CNews", SourceKind: domain.SourceMedia, PublishedAt: at(2026, 11, 7, 9, 12)},
	}
	m := mock.New()
	m.When("extract_events", `id="a"`, mentionJSON(t, "Атака шифровальщика остановила Горсвет-Энерго.", "Горсвет-Энерго"))
	m.When("extract_events", `id="b"`, mentionJSON(t, "Второй источник подтвердил атаку на Горсвет-Энерго.", "Горсвет-Энерго"))
	m.When("extract_events", `id="c"`, mentionJSON(t, "Третий источник повторяет сообщение об атаке на Горсвет-Энерго и сохраняет ссылку.", "Горсвет-Энерго"))
	m.When("cluster_confirm", "", confirmJSON(t, true, "Одна атака", nil))
	var mentions []domain.Mention
	arts := map[string]domain.Article{}
	for _, article := range articles {
		got, err := extract.Mentions(context.Background(), m, article)
		if err != nil {
			t.Fatal(err)
		}
		mentions = append(mentions, got...)
		arts[article.ID] = article
	}
	events, err := Build(context.Background(), m, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "E01" || len(events[0].ArticleIDs) != 3 {
		t.Fatalf("%+v", events)
	}
	assertQuotes(t, events, arts)
	again, err := Build(context.Background(), m, mentions, arts, nil, config.Default().Cluster)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].ID != events[0].ID || again[0].PrimaryArticleID != events[0].PrimaryArticleID {
		t.Fatalf("unstable %+v vs %+v", events[0].ID, again[0])
	}
}

func TestPromptsHaveNoLongDash(t *testing.T) {
	for _, name := range []string{
		"cluster_confirm.system.tmpl", "cluster_confirm.user.tmpl",
		"update_check.system.tmpl", "update_check.user.tmpl",
	} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "prompts", name))
		if err != nil {
			t.Fatal(err)
		}
		if textutil.HasLongDash(string(raw)) {
			t.Fatalf("%s has a long dash", name)
		}
	}
	system, user, version, err := prompts.Render("cluster_confirm", pairPrompt{
		Left:  side{MentionID: "M-a-1", ArticleID: "a", Source: "Хакер", Entities: "Альфа", Facts: "нет", Article: "x"},
		Right: side{MentionID: "M-b-1", ArticleID: "b", Source: "CNews", Entities: "Альфа", Facts: "нет", Article: "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if version != "2" || textutil.HasLongDash(system) || textutil.HasLongDash(user) {
		t.Fatal("long dash or version")
	}
	if strings.Contains(system, "получател") || strings.Contains(user, "similarity_threshold") {
		t.Fatal("settings leaked into cluster_confirm")
	}
	system, user, version, err = prompts.Render("update_check", updatePrompt{FreshID: "f", Earlier: "e", Fresh: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if version != "2" || textutil.HasLongDash(system) || textutil.HasLongDash(user) {
		t.Fatal("long dash in update_check")
	}
}

func mention(id, articleID, entity, fact string) domain.Mention {
	return domain.Mention{
		ID:             id,
		ArticleID:      articleID,
		SufficientText: true,
		Entities:       []string{entity},
		Facts:          []domain.Fact{{Text: fact, Quote: fact, ArticleID: articleID}},
	}
}

func article(id, source string, kind domain.SourceKind, published *time.Time, text string) domain.Article {
	return domain.Article{
		ID: id, Source: source, SourceKind: kind, PublishedAt: published,
		Title: id, Text: text,
	}
}

func at(y int, mon time.Month, d, hh, mm int) *time.Time {
	t := time.Date(y, mon, d, hh, mm, 0, 0, timeutil.MSK)
	return &t
}

func testPeriod() domain.Period {
	end := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	return domain.Period{Start: end.Add(-24 * time.Hour), End: end}
}

func confirmJSON(t *testing.T, same bool, reason string, conflicts []domain.Conflict) string {
	t.Helper()
	if conflicts == nil {
		conflicts = []domain.Conflict{}
	}
	raw, err := json.Marshal(confirmVerdict{
		SameEvent: same, SharedEntities: []string{"сущность"}, Reason: reason, Conflicts: conflicts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type pipeFact struct {
	Text  string `json:"text"`
	Quote string `json:"quote"`
}

type pipeMention struct {
	TitleDraft     string     `json:"title_draft"`
	Facts          []pipeFact `json:"facts"`
	Affected       string     `json:"affected"`
	Consequences   string     `json:"consequences"`
	Hedges         []string   `json:"hedges"`
	Entities       []string   `json:"entities"`
	Reporter       string     `json:"reporter"`
	NewFacts       []string   `json:"new_facts"`
	SufficientText bool       `json:"sufficient_text"`
}

func mentionJSON(t *testing.T, quote, entity string) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		Mentions []pipeMention `json:"mentions"`
	}{Mentions: []pipeMention{{
		TitleDraft:     entity,
		Facts:          []pipeFact{{Text: quote, Quote: quote}},
		Hedges:         []string{},
		Entities:       []string{entity},
		NewFacts:       []string{},
		SufficientText: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func pad(s string) string {
	filler := " Дополнительных подробностей источник не приводит."
	minChars := config.Default().Ingest.MinTextChars
	for !extract.EnoughText(s, minChars) {
		s += filler
	}
	return s
}

func assertQuotes(t *testing.T, events []domain.Event, arts map[string]domain.Article) {
	t.Helper()
	for _, ev := range events {
		for _, m := range ev.Mentions {
			a := arts[m.ArticleID]
			for _, fact := range m.Facts {
				if fact.Quote == "" || (!strings.Contains(a.Text, fact.Quote) && !strings.Contains(a.Title, fact.Quote)) {
					t.Fatalf("quote %q is not a substring of %s", fact.Quote, m.ArticleID)
				}
			}
		}
	}
}

func assertStatus(t *testing.T, j *journal.Journal, id string, status domain.Status, group, part string) {
	t.Helper()
	row, ok := j.Get(id)
	if !ok {
		t.Fatalf("no row %s", id)
	}
	if row.Status != status || row.Group != group || !strings.Contains(row.Reason, part) {
		t.Fatalf("row %+v", row)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func idsOf(events []domain.Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.ID + ":" + ev.PrimaryArticleID
	}
	return out
}
