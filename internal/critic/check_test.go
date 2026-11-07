package critic

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/llm/mock"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/timeutil"
	"cyberdigest/internal/verify"
)

func TestCheckSupportedAndUnsupported(t *testing.T) {
	arts, item := sampleItem()
	client := mock.New()
	client.When(Task, "", mustJSON(Report{Claims: []Claim{
		{Text: "утекли 20 записей", Verdict: VerdictSupported, Quote: "утекли 20 записей", Reason: "есть цитата"},
		{Text: "пострадали 500 человек", Verdict: VerdictUnsupported, Quote: "", Reason: "нет в источнике"},
		{Text: "масштаб неясен", Verdict: VerdictUncertain, Quote: "", Reason: "источник молчит"},
	}}))
	rep, err := Check(context.Background(), client, item, arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Claims) != 3 {
		t.Fatalf("claims=%d", len(rep.Claims))
	}
	if len(rep.Unsupported()) != 1 || rep.Unsupported()[0].Text != "пострадали 500 человек" {
		t.Fatalf("unsupported=%+v", rep.Unsupported())
	}
	if client.CallsFor(Task) != 1 {
		t.Fatalf("calls=%d", client.CallsFor(Task))
	}
	req := client.Requests()[0]
	if !strings.Contains(req.System, "независимый критик") {
		t.Fatal("system prompt not loaded")
	}
	if !strings.Contains(req.User, "<article") {
		t.Fatal("source must be wrapped")
	}
}

func TestGateCriticFindings(t *testing.T) {
	arts, item := sampleItem()
	client := mock.New()
	client.When(Task, "", mustJSON(Report{Claims: []Claim{
		{Text: "выдумка", Verdict: VerdictUnsupported, Quote: "", Reason: "нет опоры"},
		{Text: "неясно", Verdict: VerdictUncertain, Quote: "", Reason: "мало данных"},
	}}))
	fs, err := (GateCritic{Client: client}).CheckItem(context.Background(), item, arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("findings=%d", len(fs))
	}
	if fs[0].Rule != verify.RuleCritic || fs[0].Severity != verify.SevError {
		t.Fatalf("unsupported finding: %+v", fs[0])
	}
	if fs[1].Severity != verify.SevWarn {
		t.Fatalf("uncertain finding: %+v", fs[1])
	}
}

func TestPromptVersions(t *testing.T) {
	_, _, ver, err := prompts.Render("critic_check", promptData{
		EventID: "E01",
		Title:   "t",
		Essence: "e",
		Sources: []promptSource{{ArticleID: "a", Name: "n", Body: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ver != "1" {
		t.Fatalf("version=%q", ver)
	}
}

func TestNoLongDashInPrompts(t *testing.T) {
	system, user, _, err := prompts.Render("critic_check", promptData{
		EventID: "E01", Title: "t", Essence: "e",
		Sources: []promptSource{{ArticleID: "a", Name: "n", Body: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{system, user} {
		if strings.Contains(s, "\u2014") || strings.Contains(s, "\u2013") {
			t.Fatal("long dash in prompt")
		}
	}
}

func sampleItem() (map[string]domain.Article, domain.DigestItem) {
	published := time.Date(2026, 11, 6, 15, 0, 0, 0, timeutil.MSK)
	const id = "a1"
	arts := map[string]domain.Article{
		id: {
			ID: id, Title: "Утечка", Text: "Роскомнадзор сообщил, что утекли 20 записей.",
			Source: "Ведомости", URL: "https://example.com/a1", PublishedAt: &published,
		},
	}
	item := domain.DigestItem{
		N: 1, EventID: "E01", Title: "Утечка 20 записей",
		Essence: "Утекли 20 записей.", Consequences: "В источнике сведения о последствиях не приведены.",
		Sources: []domain.SourceRef{{ArticleID: id, Name: "Ведомости", URL: "https://example.com/a1"}},
	}
	return arts, item
}
