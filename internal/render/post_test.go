package render

import (
	"strings"
	"testing"

	"mono/internal/summarize"
	"mono/internal/textutil"
)

func TestPostGoldenOrderAndURLs(t *testing.T) {
	model := goldenModel()
	shorts := []summarize.ShortItem{
		{N: 2, Title: "Сбой", Text: "Кратко: реестр не открывался. https://evil.example/no"},
		{N: 1, Title: "Утечка", Text: "Кратко: выгрузили базу клиентов."},
	}
	got := Post(model, shorts)
	want := strings.Join([]string{
		"Дайджест по кибербезопасности: 06.11.2026 12:00 - 07.11.2026 12:00 МСК",
		"",
		"1. Утечка",
		"Кратко: выгрузили базу клиентов.",
		"https://vendor.example/a",
		"https://media.example/b",
		"",
		"2. Сбой",
		"Кратко: реестр не открывался.",
		"https://media.example/c",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "https://evil.example") {
		t.Fatal("чужой URL попал в пост")
	}
	if textutil.CountChars(got) > PostLimit {
		t.Fatalf("%d", textutil.CountChars(got))
	}
	for _, url := range textutil.ExtractURLs(Digest(model)) {
		if !strings.Contains(got, url) {
			t.Fatalf("в посте нет %s", url)
		}
	}
}

func TestPostShrinksButKeepsFactAndURL(t *testing.T) {
	model := fiveModel(false)
	long := "Утекли 1500 записей. " + strings.Repeat("хвостпоста ", 400) + "."
	var shorts []summarize.ShortItem
	for i, item := range model.Items {
		if i == 5 {
			break
		}
		shorts = append(shorts, summarize.ShortItem{N: i + 1, Title: item.Title, Text: long})
	}
	got := Post(model, shorts)
	if textutil.CountChars(got) > PostLimit {
		t.Fatalf("пост %d", textutil.CountChars(got))
	}
	if !strings.Contains(got, "1500") {
		t.Fatal("факт пропал")
	}
	if strings.Contains(got, "хвостпоста") {
		t.Fatal("хвост поста остался")
	}
	for _, item := range model.Items[:5] {
		for _, source := range item.Sources {
			if !strings.Contains(got, source.URL) {
				t.Fatalf("нет %s", source.URL)
			}
		}
	}
	if strings.Contains(got, "Шестое лишнее") {
		t.Fatal("шестое событие попало в пост")
	}
}
