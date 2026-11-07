package render

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/summarize"
	"cyberdigest/internal/textutil"
	"cyberdigest/internal/timeutil"
)

func TestDigestGolden(t *testing.T) {
	got := Digest(goldenModel())
	want := strings.Join([]string{
		"# Дайджест по кибербезопасности: 06.11.2026 12:00 - 07.11.2026 12:00 МСК, контрольное время 07.11.2026 12:00 МСК",
		"",
		"Событий в выпуске: 2. часть материалов вне темы.",
		"",
		"## 1. Утекли данные клиентов банка",
		"Суть: Неизвестные выгрузили базу клиентов. Банк подтвердил инцидент.",
		"Последствия: Пострадали клиенты банка.",
		"Источники: Банк (первичный источник), 06.11.2026, https://vendor.example/a, ID: a1; Газета (СМИ), 06.11.2026, https://media.example/b, ID: b1",
		"Обоснование: Есть подтверждение банка и описание утечки.",
		"",
		"## 2. Сбой в реестре",
		"Суть: Реестр не открывался два часа.",
		"Последствия: В источнике сведения о последствиях не приведены.",
		"Источники: Ведомости (СМИ), 07.11.2026, https://media.example/c, ID: c1",
		"Обоснование: Сбой затронул пользователей реестра.",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("golden\ngot:\n%s\nwant:\n%s", got, want)
	}
	if err := CheckFormat(got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Не вошли") || strings.Contains(got, "НЕ ВОШЛО СЕКРЕТ") {
		t.Fatal("лишний блок")
	}
}

func TestDigestEmpty(t *testing.T) {
	start, end := periodBounds()
	model := domain.DigestModel{
		Period:      domain.Period{Start: start, End: end},
		ControlTime: end,
		Empty:       true,
		Shortfall:   "все материалы вне темы",
		Items:       []domain.DigestItem{{Title: "не должно попасть", Essence: "секрет пункта"}},
		NotIncluded: []domain.NotIncluded{{ArticleID: "x", Reason: "НЕ ВОШЛО СЕКРЕТ"}},
	}
	got := Digest(model)
	want := strings.Join([]string{
		"# Дайджест по кибербезопасности: 06.11.2026 12:00 - 07.11.2026 12:00 МСК, контрольное время 07.11.2026 12:00 МСК",
		"",
		"Событий в выпуске: 0. Подходящих событий за период нет. все материалы вне темы.",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	if err := CheckFormat(got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "## ") || strings.Contains(got, "не должно попасть") || strings.Contains(got, "НЕ ВОШЛО СЕКРЕТ") {
		t.Fatal(got)
	}
	post := Post(model, nil)
	email := EmailText(model)
	html := EmailHTML(model)
	for _, body := range []string{post, email, html} {
		if !strings.Contains(body, "Подходящих событий за период нет") || !strings.Contains(body, "все материалы вне темы") {
			t.Fatalf("пустое сообщение разошлось:\n%s", body)
		}
	}
	if strings.Contains(post, "## ") || strings.Contains(email, "не должно попасть") {
		t.Fatal("пустое представление содержит событие")
	}
}

func TestDigestFiveEventsAndURLCount(t *testing.T) {
	model := fiveModel(false)
	long := "https://cdn.example/" + strings.Repeat("a", 500)
	model.Items[0].Sources[0].URL = long
	got := Digest(model)
	if err := CheckFormat(got); err != nil {
		t.Fatal(err)
	}
	if textutil.CountChars(got) > DigestLimit {
		t.Fatalf("длина %d", textutil.CountChars(got))
	}
	if textutil.CountChars(got) != utf8.RuneCountInString(textutil.StripURLs(got)) {
		t.Fatal("CountChars разошёлся со StripURLs")
	}
	short := strings.Replace(got, long, "https://cdn.example/a", 1)
	if textutil.CountChars(got) != textutil.CountChars(short) {
		t.Fatalf("URL вошёл в лимит: %d vs %d", textutil.CountChars(got), textutil.CountChars(short))
	}
	if strings.Contains(got, "УНИКАЛЬНАЯ ПРИЧИНА") {
		t.Fatal("пояснение при пяти событиях попало в выпуск")
	}
	for _, item := range model.Items {
		for _, source := range item.Sources {
			if !strings.Contains(got, source.URL) || !strings.Contains(got, "ID: "+source.ArticleID) {
				t.Fatalf("нет источника %s", source.ArticleID)
			}
		}
	}
}

func TestDigestCompressesOptionalText(t *testing.T) {
	model := fiveModel(true)
	got := Digest(model)
	if err := CheckFormat(got); err != nil {
		t.Fatal(err)
	}
	if textutil.CountChars(got) > DigestLimit {
		t.Fatalf("после сжатия %d", textutil.CountChars(got))
	}
	if !strings.Contains(got, "1500") {
		t.Fatal("факт из начала сути пропал")
	}
	if strings.Contains(got, "МЕТКАХВОСТ") || strings.Contains(got, "МЕТКАВТОРАЯ") || strings.Contains(got, "СЛАБОЕОБОСНОВАНИЕ") {
		t.Fatal("необязательный хвост остался")
	}
	for _, item := range model.Items {
		for _, source := range item.Sources {
			if !strings.Contains(got, source.URL) || !strings.Contains(got, "ID: "+source.ArticleID) {
				t.Fatalf("источник удалён: %+v", source)
			}
		}
	}
}

func TestDigestReplacesLongDash(t *testing.T) {
	model := goldenModel()
	em := string(rune(0x2014))
	en := string(rune(0x2013))
	model.Items[0].Essence = "Атака" + em + "на портал"
	model.Items[0].Title = "Итог" + en + "дня"
	got := Digest(model)
	if textutil.HasLongDash(got) {
		t.Fatal(got)
	}
	if !strings.Contains(got, "Атака - на портал") || !strings.Contains(got, "Итог - дня") {
		t.Fatalf("%s", got)
	}
	if err := CheckFormat(got); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFormatRejectsBrokenDigest(t *testing.T) {
	if err := CheckFormat("просто текст"); err == nil {
		t.Fatal("ждали ошибку")
	}
	broken := Digest(goldenModel())
	broken = strings.Replace(broken, "Суть: ", "", 1)
	if err := CheckFormat(broken); err == nil {
		t.Fatal("ждали ошибку поля")
	}
	dashed := Digest(goldenModel()) + string(rune(0x2014))
	if err := CheckFormat(dashed); err == nil {
		t.Fatal("ждали ошибку тире")
	}
}

func goldenModel() domain.DigestModel {
	start, end := periodBounds()
	return domain.DigestModel{
		Period:      domain.Period{Start: start, End: end},
		ControlTime: end,
		Shortfall:   "часть материалов вне темы",
		NotIncluded: []domain.NotIncluded{{ArticleID: "z", Reason: "НЕ ВОШЛО СЕКРЕТ"}},
		Items: []domain.DigestItem{
			{
				Title:        "Утекли данные клиентов банка",
				Essence:      "Неизвестные выгрузили базу клиентов. Банк подтвердил инцидент.",
				Consequences: "Пострадали клиенты банка.",
				Rationale:    "Есть подтверждение банка и описание утечки.",
				Sources: []domain.SourceRef{
					{ArticleID: "b1", Name: "Газета", Date: "06.11.2026", URL: "https://media.example/b", Role: summarize.RoleMedia},
					{ArticleID: "a1", Name: "Банк", Date: "06.11.2026", URL: "https://vendor.example/a", Role: summarize.RolePrimary},
				},
			},
			{
				Title:     "Сбой в реестре",
				Essence:   "Реестр не открывался два часа.",
				Rationale: "Сбой затронул пользователей реестра.",
				Sources: []domain.SourceRef{
					{ArticleID: "c1", Name: "Ведомости", Date: "07.11.2026", URL: "https://media.example/c", Role: summarize.RoleMedia},
				},
			},
		},
	}
}

func fiveModel(huge bool) domain.DigestModel {
	start, end := periodBounds()
	// One optional sentence is longer than the digest cap, so every copy of its tail marker has to go.
	second := strings.Repeat("слово ", 800) + "МЕТКАВТОРАЯ."
	tail := strings.Repeat("хвост ", 800) + "МЕТКАХВОСТ."
	rationale := "Причина указана в публикации. " + strings.Repeat("абзац ", 800) + "СЛАБОЕОБОСНОВАНИЕ."
	items := make([]domain.DigestItem, 5)
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		essence := "Утекли 1500 записей клиентов. Второе предложение короткое."
		why := "Причина указана в публикации."
		if huge {
			essence = "Утекли 1500 записей клиентов. " + second + " " + tail
			why = rationale
		}
		items[i] = domain.DigestItem{
			Title:        "Событие " + id,
			Essence:      essence,
			Consequences: "Сервис не работал два часа.",
			Rationale:    why,
			Sources: []domain.SourceRef{
				{ArticleID: "p" + id, Name: "Банк", Date: "06.11.2026", URL: "https://vendor.example/" + id, Role: summarize.RolePrimary},
				{ArticleID: "s" + id, Name: "Газета", Date: "06.11.2026", URL: "https://media.example/" + id, Role: summarize.RoleMedia},
				{ArticleID: "t" + id, Name: "Лента", Date: "06.11.2026", URL: "https://tape.example/" + id, Role: summarize.RoleReprint},
			},
		}
	}
	extra := append(items, domain.DigestItem{Title: "Шестое лишнее", Essence: "Это событие не должно попасть в выпуск.", Rationale: "Лишнее.", Consequences: "Нет."})
	return domain.DigestModel{
		Period:      domain.Period{Start: start, End: end},
		ControlTime: end,
		Shortfall:   "УНИКАЛЬНАЯ ПРИЧИНА",
		Items:       extra,
	}
}

func periodBounds() (time.Time, time.Time) {
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, timeutil.MSK)
	end := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	return start, end
}
