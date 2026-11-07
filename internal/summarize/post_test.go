package summarize

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/llm/mock"
)

func TestShortItemsKeepsModelURLs(t *testing.T) {
	model := domain.DigestModel{Items: []domain.DigestItem{
		{N: 1, Title: "Утечка", Essence: "Утекли 1500 записей.", Consequences: "Сервис лежал.", Sources: []domain.SourceRef{{URL: "https://vendor.example/a"}, {URL: "https://media.example/b"}}},
		{N: 2, Title: "Сбой", Essence: "Реестр закрыт.", Consequences: NoConsequences, Sources: []domain.SourceRef{{URL: "https://media.example/c"}}},
	}}
	client := mock.New()
	client.When("write_post", "", postResp(
		postRow{1, "Утечка", "Кратко: утекли 1500 записей. Смотрите https://evil.example/x"},
		postRow{2, "Сбой", "Кратко: реестр закрыт."},
	))
	shorts, err := ShortItems(context.Background(), client, model, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(shorts) != 2 || shorts[0].N != 1 || shorts[1].N != 2 {
		t.Fatalf("%+v", shorts)
	}
	if strings.Contains(shorts[0].Text, "https://evil.example") {
		t.Fatalf("чужой URL остался: %s", shorts[0].Text)
	}
	if !strings.Contains(shorts[0].Text, "1500") {
		t.Fatalf("%s", shorts[0].Text)
	}
	if strings.Join(shorts[0].URLs, ",") != "https://vendor.example/a,https://media.example/b" {
		t.Fatalf("%v", shorts[0].URLs)
	}
	if client.Calls() != 1 {
		t.Fatalf("вызовов %d", client.Calls())
	}
	req := client.Requests()[0]
	if !strings.Contains(req.User, "https://vendor.example/a") || !strings.Contains(req.User, "2000") {
		t.Fatalf("промпт поста:\n%s", req.User)
	}
}

func TestShortItemsRetriesThenShrinks(t *testing.T) {
	model := domain.DigestModel{Items: []domain.DigestItem{
		{N: 1, Title: "Утечка", Essence: "Утекли 1500 записей.", Sources: []domain.SourceRef{{URL: "https://vendor.example/a"}}},
	}}
	long := "Утекли 1500 записей. " + strings.Repeat("лишняяподробность ", 300) + "."
	client := mock.New()
	client.Queue("write_post", "",
		postResp(postRow{1, "Утечка", long}),
		postResp(postRow{1, "Утечка", long}),
	)
	shorts, err := ShortItems(context.Background(), client, model, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if client.Calls() != 2 {
		t.Fatalf("вызовов %d", client.Calls())
	}
	if !strings.Contains(client.Requests()[1].User, "длиннее лимита") {
		t.Fatal("повтор без пометки лимита")
	}
	if postWeight(shorts) > 2000 {
		t.Fatalf("вес %d", postWeight(shorts))
	}
	if !strings.Contains(shorts[0].Text, "1500") || strings.Contains(shorts[0].Text, "лишняяподробность") {
		t.Fatalf("%s", shorts[0].Text)
	}
	if len(shorts[0].URLs) != 1 || shorts[0].URLs[0] != "https://vendor.example/a" {
		t.Fatalf("%v", shorts[0].URLs)
	}
}

func TestShortItemsEmptyAndFallback(t *testing.T) {
	client := mock.New()
	shorts, err := ShortItems(context.Background(), client, domain.DigestModel{Empty: true, Shortfall: "вне темы"}, 2000)
	if err != nil || shorts != nil || client.Calls() != 0 {
		t.Fatalf("пусто: %v %+v calls %d", err, shorts, client.Calls())
	}
	model := domain.DigestModel{Items: []domain.DigestItem{{N: 1, Title: "Утечка", Essence: "Утекли 1500 записей.", Sources: []domain.SourceRef{{URL: "https://vendor.example/a"}}}}}
	client.When("write_post", "", `{"items":[]}`)
	shorts, err = ShortItems(context.Background(), client, model, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if shorts[0].Text != "Утекли 1500 записей." || shorts[0].URLs[0] != "https://vendor.example/a" {
		t.Fatalf("%+v", shorts[0])
	}
}

type postRow struct {
	N     int
	Title string
	Text  string
}

func postResp(rows ...postRow) string {
	type row struct {
		N     int    `json:"n"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	body := struct {
		Items []row `json:"items"`
	}{}
	for _, item := range rows {
		body.Items = append(body.Items, row{N: item.N, Title: item.Title, Text: item.Text})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
