package summarize_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/llm/mock"
	"mono/internal/render"
	"mono/internal/summarize"
	"mono/internal/textutil"
	"mono/internal/timeutil"
)

func TestFiveEventsFitDigestPostAndEmail(t *testing.T) {
	client := mock.New()
	client.When("write_digest_item", "", mustJSON(map[string]string{
		"title":        "Утечка данных",
		"essence":      "Неизвестные выгрузили 1500 записей. Компания заявила о восстановлении сервиса.",
		"consequences": "Клиенты не входили в сервис два часа.",
		"hedge_note":   "",
	}))
	client.When("write_post", "", fivePostJSON())

	start := time.Date(2026, 11, 6, 12, 0, 0, 0, timeutil.MSK)
	end := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	published := time.Date(2026, 11, 6, 18, 0, 0, 0, timeutil.MSK)
	var events []domain.Event
	arts := map[string]domain.Article{}
	var urls []string
	for i := 1; i <= 5; i++ {
		id := string(rune('a' + i - 1))
		vendor := "v" + id
		media := "m" + id
		vURL := "https://vendor.example/" + id
		mURL := "https://media.example/" + id + "/" + strings.Repeat("u", 180)
		urls = append(urls, vURL, mURL)
		arts[vendor] = domain.Article{ID: vendor, Source: "Банк " + id, URL: vURL, SourceKind: domain.SourceVendor, PublishedAt: &published}
		arts[media] = domain.Article{ID: media, Source: "Газета " + id, URL: mURL, SourceKind: domain.SourceMedia, PublishedAt: &published}
		events = append(events, domain.Event{
			ID:               "E" + id,
			Selected:         true,
			Rank:             i,
			Rationale:        "Есть подтверждение источника " + id + ".",
			ArticleIDs:       []string{media, vendor},
			PrimaryArticleID: media,
		})
	}
	items, err := summarize.Items(context.Background(), client, events, arts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("%d", len(items))
	}
	if items[0].Sources[0].Role != summarize.RolePrimary {
		t.Fatalf("роль %+v", items[0].Sources[0])
	}
	model := domain.DigestModel{Period: domain.Period{Start: start, End: end}, ControlTime: end, Items: items}
	digest := render.Digest(model)
	if err := render.CheckFormat(digest); err != nil {
		t.Fatal(err)
	}
	if n := textutil.CountChars(digest); n > render.DigestLimit {
		t.Fatalf("дайджест %d", n)
	}
	shorts, err := summarize.ShortItems(context.Background(), client, model, render.PostLimit)
	if err != nil {
		t.Fatal(err)
	}
	post := render.Post(model, shorts)
	email := render.EmailText(model)
	html := render.EmailHTML(model)
	if n := textutil.CountChars(post); n > render.PostLimit {
		t.Fatalf("пост %d\n%s", n, post)
	}
	for _, url := range urls {
		if !strings.Contains(digest, url) || !strings.Contains(post, url) || !strings.Contains(email, url) || !strings.Contains(html, url) {
			t.Fatalf("пропал URL %s", url)
		}
	}
	if !strings.Contains(post, "Кратко") || !strings.Contains(digest, "1500") {
		t.Fatal("факты разошлись")
	}
	if textutil.HasLongDash(digest) || textutil.HasLongDash(post) || textutil.HasLongDash(email) {
		t.Fatal("длинное тире")
	}
	if client.CallsFor("write_digest_item") != 5 || client.CallsFor("write_post") != 1 {
		t.Fatalf("вызовы item %d post %d", client.CallsFor("write_digest_item"), client.CallsFor("write_post"))
	}
}

func fivePostJSON() string {
	type row struct {
		N     int    `json:"n"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	body := struct {
		Items []row `json:"items"`
	}{}
	for i := 1; i <= 5; i++ {
		body.Items = append(body.Items, row{N: i, Title: "Утечка данных", Text: "Кратко: выгрузили 1500 записей, сервис не работал два часа."})
	}
	return mustJSON(body)
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
