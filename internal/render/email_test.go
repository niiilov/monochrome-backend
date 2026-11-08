package render

import (
	"strings"
	"testing"

	"mono/internal/textutil"
)

func TestEmailUsesDigest(t *testing.T) {
	model := goldenModel()
	digest := Digest(model)
	if EmailText(model) != "Тема: "+Subject(model)+"\n\n"+digest {
		t.Fatal("текст письма разошёлся с дайджестом")
	}
	if Subject(model) != "Дайджест по кибербезопасности: 06.11.2026 12:00 - 07.11.2026 12:00 МСК" {
		t.Fatalf("%s", Subject(model))
	}
	html := EmailHTML(model)
	if !strings.Contains(html, "<title>"+Subject(model)+"</title>") {
		t.Fatal("нет темы")
	}
	if !strings.Contains(html, "Период:") || !strings.Contains(html, "Контрольное время:") {
		t.Fatal("нет периода")
	}
	for _, url := range textutil.ExtractURLs(digest) {
		if !strings.Contains(html, url) {
			t.Fatalf("html без %s", url)
		}
	}
	if !strings.Contains(html, "Пострадали клиенты банка.") || !strings.Contains(html, "В источнике сведения о последствиях не приведены.") {
		t.Fatal("поля письма разошлись")
	}
	if strings.Contains(html, "НЕ ВОШЛО СЕКРЕТ") || strings.Contains(html, "Не вошли") {
		t.Fatal("лишний блок в письме")
	}
}

func TestEmailHTMLEscapes(t *testing.T) {
	model := goldenModel()
	model.Items[0].Title = `<script>alert(1)</script>`
	model.Items[0].Essence = `A < B & C`
	html := EmailHTML(model)
	if strings.Contains(html, "<script>alert") {
		t.Fatal(html)
	}
	if !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "A &lt; B &amp; C") {
		t.Fatalf("%s", html)
	}
}
