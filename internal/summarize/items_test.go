package summarize

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/llm"
	"mono/internal/llm/mock"
	"mono/internal/prompts"
	"mono/internal/textutil"
	"mono/internal/timeutil"
)

func TestPromptsRender(t *testing.T) {
	system, user, version, err := prompts.Render("write_digest_item", itemView{EventID: "E1"})
	if err != nil {
		t.Fatal(err)
	}
	if version != "4" {
		t.Fatalf("version %s", version)
	}
	for _, phrase := range []string{
		"не выбирай версию",
		"В источнике сведения о последствиях не приведены.",
		"стоит отметить",
		"длинное тире",
		"оговорки автора",
		"E1",
	} {
		blob := system + "\n" + user
		if !strings.Contains(blob, phrase) {
			t.Fatalf("нет %q", phrase)
		}
	}
	if textutil.HasLongDash(system) || textutil.HasLongDash(user) {
		t.Fatal("длинное тире в промпте пункта")
	}
	ps, pu, pv, err := prompts.Render("write_post", postView{Limit: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if pv != "2" || !strings.Contains(ps, "2000") || !strings.Contains(pu, "2000") {
		t.Fatalf("post prompt %s", pu)
	}
	if textutil.HasLongDash(ps) || textutil.HasLongDash(pu) {
		t.Fatal("длинное тире в промпте поста")
	}
}

func TestItemsUsesFactsNotFullText(t *testing.T) {
	published := time.Date(2026, 11, 6, 18, 0, 0, 0, timeutil.MSK)
	arts := map[string]domain.Article{
		"v1": {ID: "v1", Source: "Банк", URL: "https://vendor.example/a", SourceKind: domain.SourceVendor, PublishedAt: &published, Text: "ПОЛНЫЙСЕКРЕТНЫЙТЕКСТ"},
	}
	event := domain.Event{
		ID:               "E1",
		PrimaryArticleID: "v1",
		ArticleIDs:       []string{"v1"},
		Rationale:        "Есть заявление банка.",
		Conflicts:        []domain.Conflict{{Field: "сумма", Description: "называют разные суммы", ArticleIDs: []string{"v1"}}},
		Mentions: []domain.Mention{{
			ArticleID:          "v1",
			IsCompanyStatement: true,
			Hedges:             []string{"по предварительным данным"},
			Affected:           "клиенты банка",
			Facts:              []domain.Fact{{Text: "Банк подтвердил утечку", Quote: "короткая цитата", ArticleID: "v1"}},
		}},
	}
	client := mock.New()
	client.When("write_digest_item", "E1", itemResp("Утечка в банке", "Банк подтвердил утечку.", "Клиенты не входили в сервис.", ""))
	items, err := Items(context.Background(), client, []domain.Event{event}, arts, map[string][]string{"E1": {"убери число 9999"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].N != 1 || items[0].EventID != "E1" {
		t.Fatalf("%+v", items)
	}
	if items[0].Rationale != "Есть заявление банка." {
		t.Fatalf("обоснование %q", items[0].Rationale)
	}
	if len(items[0].Sources) != 1 || items[0].Sources[0].Role != RolePrimary {
		t.Fatalf("источник %+v", items[0].Sources)
	}
	req := client.Requests()[0]
	for _, phrase := range []string{"короткая цитата", "<article", "называют разные суммы", "по предварительным данным", "компания заявила", "первичный источник", "убери число 9999"} {
		if !strings.Contains(req.User, phrase) && !strings.Contains(req.System, phrase) {
			t.Fatalf("промпт без %q\n%s", phrase, req.User)
		}
	}
	if strings.Contains(req.User, "ПОЛНЫЙСЕКРЕТНЫЙТЕКСТ") || strings.Contains(req.System, "ПОЛНЫЙСЕКРЕТНЫЙТЕКСТ") {
		t.Fatal("в промпт попал полный текст статьи")
	}
	if strings.Contains(req.User, "https://vendor.example/a") {
		t.Fatal("URL не должен уходить в промпт пункта")
	}
}

func TestItemsEmptyConsequencesAndHedge(t *testing.T) {
	client := mock.New()
	client.When("write_digest_item", "", itemResp("Сбой реестра", "Реестр не открывался.", "", "по предварительным данным"))
	items, err := Items(context.Background(), client, []domain.Event{{ID: "E2"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Consequences != NoConsequences {
		t.Fatalf("%q", items[0].Consequences)
	}
	if !strings.Contains(items[0].Essence, "по предварительным данным") {
		t.Fatalf("%q", items[0].Essence)
	}
	if items[0].Rationale != noRationale {
		t.Fatalf("%q", items[0].Rationale)
	}
}

func TestItemsOrderSelectionAndCap(t *testing.T) {
	client := mock.New()
	client.When("write_digest_item", "", itemResp("Событие", "Факт подтверждён. Подробность короткая.", "Сервис лежал.", ""))
	events := []domain.Event{
		{ID: "skip", Selected: false, Rank: 1},
		{ID: "r2", Selected: true, Rank: 2},
		{ID: "r1", Selected: true, Rank: 1},
		{ID: "r4", Selected: true, Rank: 4},
		{ID: "r3", Selected: true, Rank: 3},
		{ID: "r5", Selected: true, Rank: 5},
		{ID: "r6", Selected: true, Rank: 6},
	}
	items, err := Items(context.Background(), client, events, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range items {
		ids = append(ids, item.EventID)
	}
	if strings.Join(ids, ",") != "r1,r2,r3,r4,r5" {
		t.Fatalf("%s", strings.Join(ids, ","))
	}
	plain, err := Items(context.Background(), client, []domain.Event{{ID: "b", Rank: 2}, {ID: "a", Rank: 1}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[0].EventID != "a" || plain[1].EventID != "b" {
		t.Fatalf("%s %s", plain[0].EventID, plain[1].EventID)
	}
}

func TestItemsShrinksLongDraft(t *testing.T) {
	long := strings.Repeat("Очень длинное предложение без точки ", 200)
	client := mock.New()
	client.Queue("write_digest_item", "E9",
		itemResp("Длинный заголовок", long, "Последствие длинное.", ""),
		itemResp("Короткий заголовок", "Утекли 1500 записей. Банк подтвердил факт.", "Сервис лежал два часа.", ""),
	)
	items, err := Items(context.Background(), client, []domain.Event{{ID: "E9", Rationale: "Короткое обоснование."}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Calls() != 2 {
		t.Fatalf("вызовов %d", client.Calls())
	}
	reqs := client.Requests()
	if strings.Contains(reqs[0].User, "длиннее лимита") {
		t.Fatal("первый вызов уже с подсказкой сжатия")
	}
	if !strings.Contains(reqs[1].User, "длиннее лимита") {
		t.Fatal("повтор без подсказки сжатия")
	}
	if !strings.Contains(items[0].Essence, "1500") || strings.Contains(items[0].Essence, "Очень длинное предложение") {
		t.Fatalf("%s", items[0].Essence)
	}
}

func TestItemsParallel(t *testing.T) {
	inner := mock.New()
	inner.When("write_digest_item", "", itemResp("Событие", "Факт подтверждён. Второй факт тоже.", "Есть последствие.", ""))
	gate := &gateClient{inner: inner}
	var events []domain.Event
	for i := 0; i < 5; i++ {
		events = append(events, domain.Event{ID: string(rune('A' + i))})
	}
	items, err := Items(context.Background(), gate, events, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("%d", len(items))
	}
	if gate.maxInFlight() < 2 {
		t.Fatalf("параллельность %d", gate.maxInFlight())
	}
}

func TestItemsFailureAndEdges(t *testing.T) {
	if items, err := Items(context.Background(), nil, nil, nil, nil); err != nil || items != nil {
		t.Fatalf("пусто: %v %+v", err, items)
	}
	if _, err := Items(context.Background(), nil, []domain.Event{{ID: "E"}}, nil, nil); err == nil {
		t.Fatal("ожидалась ошибка без клиента")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := mock.New()
	client.When("write_digest_item", "", itemResp("Событие", "Факт.", "Последствие.", ""))
	if _, err := Items(ctx, client, []domain.Event{{ID: "E"}}, nil, nil); err == nil {
		t.Fatal("отменённый контекст")
	}
	client = mock.New()
	client.When("write_digest_item", "", itemResp("Событие", "Факт подтверждён.", "Последствие.", ""))
	client.Fail("write_digest_item", "E03", errors.New("сбой модели"))
	_, err := Items(context.Background(), client, []domain.Event{{ID: "E01"}, {ID: "E03"}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "E03") {
		t.Fatalf("%v", err)
	}
}

func itemResp(title, essence, consequences, hedge string) string {
	raw, err := json.Marshal(itemJSON{
		Title:        title,
		Essence:      essence,
		Consequences: consequences,
		HedgeNote:    hedge,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

type gateClient struct {
	mu    sync.Mutex
	cur   int
	max   int
	inner llm.Client
}

func (g *gateClient) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	g.mu.Lock()
	g.cur++
	if g.cur > g.max {
		g.max = g.cur
	}
	g.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	defer func() {
		g.mu.Lock()
		g.cur--
		g.mu.Unlock()
	}()
	return g.inner.Complete(ctx, r)
}

func (g *gateClient) maxInFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}
