package normalize

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/ingest"
)

func TestCleanHTMLWhitespaceAndControls(t *testing.T) {
	raw := "<script>ignore()</script><p>В Казани нашли <b>14</b> банкоматов.</p><p>Сумма 3&nbsp;200&nbsp;000 рублей.</p>\x00\x01конец"
	got := Clean(raw)
	if strings.Contains(got, "<") || strings.Contains(got, "ignore") || strings.Contains(got, "\x00") {
		t.Fatalf("грязный текст: %q", got)
	}
	if !strings.Contains(got, "14") || !strings.Contains(got, "3 200 000") {
		t.Fatalf("потеряны числа: %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("абзацы схлопнулись: %q", got)
	}
	wrapped := "строка\t\tраз\r\n\r\n\r\nдва"
	if Clean(wrapped) != "строка раз\n\nдва" {
		t.Fatalf("пробелы: %q", Clean(wrapped))
	}
}

func TestApplyKeepsRawAndSetsKind(t *testing.T) {
	raw := "<p>Текст &amp; факт.</p>"
	a := domain.Article{
		ID:     "n-1",
		Title:  "  Заголовок\t",
		Text:   raw,
		Source: "Хакер",
		URL:    "https://xakep.ru/n",
	}
	Apply(&a)
	if a.RawText != raw {
		t.Fatalf("RawText изменился: %q", a.RawText)
	}
	if strings.Contains(a.Text, "<") || !strings.Contains(a.Text, "Текст & факт.") {
		t.Fatalf("text: %q", a.Text)
	}
	if a.Title != "Заголовок" {
		t.Fatalf("title: %q", a.Title)
	}
	if a.SourceKind != "media" {
		t.Fatalf("kind: %s", a.SourceKind)
	}
	again := a.Text
	Apply(&a)
	if a.Text != again || a.RawText != raw {
		t.Fatal("повторный Apply не идемпотентен")
	}
}

func TestSyntheticNormalize(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "synthetic", "articles.json")
	arts, _, err := ingest.LoadDataset(context.Background(), []string{path}, config.Default().Ingest)
	if err != nil {
		t.Fatal(err)
	}
	ApplyAll(arts)
	by := map[string]domain.Article{}
	for _, a := range arts {
		by[a.ID] = a
	}
	if by["syn-07"].SourceKind != domain.SourceVendor {
		t.Fatalf("pt: %s", by["syn-07"].SourceKind)
	}
	if by["syn-12"].SourceKind != domain.SourceRegulator {
		t.Fatalf("rkn: %s", by["syn-12"].SourceKind)
	}
	if by["syn-36"].SourceKind != domain.SourceAuthor {
		t.Fatalf("habr: %s", by["syn-36"].SourceKind)
	}
	if by["syn-37"].SourceKind != domain.SourceUnknown {
		t.Fatalf("blog: %s", by["syn-37"].SourceKind)
	}
	html := by["syn-38"]
	if !strings.Contains(html.RawText, "<p>") || strings.Contains(html.Text, "<") {
		t.Fatalf("html: raw %q text %q", html.RawText, html.Text)
	}
	if !strings.Contains(html.Text, "3 200 000") || !strings.Contains(html.Text, "14") {
		t.Fatalf("числа после чистки: %q", html.Text)
	}
	if by["syn-22"].Text != "" || !containsField(by["syn-22"].MissingFields, "text") {
		t.Fatalf("пустой текст: %+v", by["syn-22"])
	}
	if by["syn-30"].PublishedRaw != "1794029400" {
		t.Fatalf("unix: %q", by["syn-30"].PublishedRaw)
	}
}

func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

func TestChunksKeepNumbersDatesAndOverlap(t *testing.T) {
	if Chunks("", 40) != nil {
		t.Fatal("пустой текст")
	}
	if got := Chunks("короткий текст", 100); len(got) != 1 || got[0] != "короткий текст" {
		t.Fatalf("короткий: %#v", got)
	}
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("Предложение номер ")
		b.WriteString(string(rune('A' + i)))
		b.WriteString(" описывает проверку службы безопасности. ")
	}
	b.WriteString("Сумма ущерба составила 1 000 000 рублей, событие датировано 06.11.2026 и 6 ноября 2026 года.")
	chunks := Chunks(b.String(), 80)
	if len(chunks) < 2 {
		t.Fatalf("ожидалось несколько кусков: %#v", chunks)
	}
	assertIntact(t, chunks, "1 000 000")
	assertIntact(t, chunks, "06.11.2026")
	assertIntact(t, chunks, "6 ноября 2026")
	if shared := maxSharedEdge(chunks); shared < 8 {
		t.Fatalf("перекрытие %d рун, куски: %#v", shared, chunks)
	}
	for _, ch := range chunks {
		if n := utf8.RuneCountInString(ch); n > 80 && !containsToken(ch) {
			t.Fatalf("кусок длиннее лимита без защищённого токена (%d): %q", n, ch)
		}
	}

	para := strings.Repeat("абзац первый про сеть. ", 6) + "\n\n" +
		"Ключевая дата 06.11.2026 и число 12 500 должны сохраниться целиком." + "\n\n" +
		strings.Repeat("абзац третий про сеть. ", 6)
	parts := Chunks(para, 90)
	assertIntact(t, parts, "06.11.2026")
	assertIntact(t, parts, "12 500")
	if !strings.Contains(parts[0], "абзац первый") || strings.Contains(parts[0], "12 500") {
		t.Fatalf("первый кусок должен закончиться на абзаце: %q", parts[0])
	}

	forced := Chunks("xxxx 1 000 000 yyyy", 4)
	assertIntact(t, forced, "1 000 000")
}

func containsToken(s string) bool {
	return strings.Contains(s, "1 000 000") || strings.Contains(s, "06.11.2026") || strings.Contains(s, "6 ноября 2026")
}

func assertIntact(t *testing.T, chunks []string, token string) {
	t.Helper()
	found := false
	for _, ch := range chunks {
		if strings.Contains(ch, token) {
			found = true
		}
	}
	if !found {
		t.Fatalf("токен %q не найден в %#v", token, chunks)
	}
	tr := []rune(token)
	for i := 0; i < len(chunks)-1; i++ {
		a := []rune(chunks[i])
		b := []rune(chunks[i+1])
		for k := 1; k < len(tr); k++ {
			suf := string(tr[:k])
			pre := string(tr[k:])
			if strings.HasSuffix(chunks[i], suf) && strings.HasPrefix(chunks[i+1], pre) &&
				!strings.Contains(chunks[i], token) && !strings.Contains(chunks[i+1], token) {
				t.Fatalf("токен %q разорван между кусками %q | %q", token, string(a), string(b))
			}
		}
	}
}

func maxSharedEdge(chunks []string) int {
	best := 0
	for i := 0; i < len(chunks)-1; i++ {
		a := []rune(chunks[i])
		b := []rune(chunks[i+1])
		limit := len(a)
		if len(b) < limit {
			limit = len(b)
		}
		for n := limit; n > best; n-- {
			if string(a[len(a)-n:]) == string(b[:n]) {
				best = n
				break
			}
		}
	}
	return best
}
