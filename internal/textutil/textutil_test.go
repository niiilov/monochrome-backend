package textutil

import (
	"strings"
	"testing"
)

func TestStripURLTrailingPunctuation(t *testing.T) {
	in := "Смотрите (https://example.com/path), пожалуйста."
	got := StripURLs(in)
	want := "Смотрите (), пожалуйста."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	urls := ExtractURLs(in)
	if len(urls) != 1 || urls[0] != "https://example.com/path" {
		t.Fatalf("urls: %#v", urls)
	}
}

func TestCountCharsRussianAndLimits(t *testing.T) {
	if got := CountChars("Привет"); got != 6 {
		t.Fatalf("руны: got %d", got)
	}
	body := strings.Repeat("я", 4000)
	if got := CountChars(body); got != 4000 {
		t.Fatalf("лимит дайджеста: got %d", got)
	}
	post := strings.Repeat("я", 2000)
	if got := CountChars(post); got != 2000 {
		t.Fatalf("лимит поста: got %d", got)
	}
	withURL := body + " (https://example.com/a),"
	if got := CountChars(withURL); got != 4000+len([]rune(" (),")) {
		t.Fatalf("URL не должен входить в лимит: got %d", got)
	}
	if CountChars(body+"я") != 4001 {
		t.Fatal("4001 знак должен превышать лимит дайджеста")
	}
}

func TestLongDash(t *testing.T) {
	plain := "обычный-дефис"
	if HasLongDash(plain) {
		t.Fatal("дефис не должен считаться длинным тире")
	}
	em := "слово \u2014 слово"
	en := "5\u201310"
	if !HasLongDash(em) || !HasLongDash(en) {
		t.Fatal("длинное тире не обнаружено")
	}
	if got := ReplaceLongDash(em); got != "слово - слово" {
		t.Fatalf("пробельное тире: %q", got)
	}
	if got := ReplaceLongDash(en); got != "5-10" {
		t.Fatalf("числовой диапазон: %q", got)
	}
	if got := ReplaceLongDash("слово\u2014слово"); got != "слово - слово" {
		t.Fatalf("тире без пробелов: %q", got)
	}
}

func TestExtractNumbers(t *testing.T) {
	got := ExtractNumbers("около 1 000 записей, 1,5 млн рублей и 20%")
	want := []string{"1000", "1500000", "20%"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %#v", got)
	}
	dates := ExtractNumbers("сводка 05.11.2026 и ещё 20%")
	if strings.Join(dates, ",") != "20%" {
		t.Fatalf("числа из даты попали в выборку: %#v", dates)
	}
}

func TestExtractDates(t *testing.T) {
	got := ExtractDates("с 5 ноября по 05.11.2026")
	want := []string{"05.11", "05.11.2026"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %#v", got)
	}
	full := ExtractDates("5 ноября 2026")
	if len(full) != 1 || full[0] != "05.11.2026" {
		t.Fatalf("словесная дата с годом: %#v", full)
	}
}

func TestExtractEmails(t *testing.T) {
	got := ExtractEmails("Пишите на soc@example.com, пожалуйста.")
	if len(got) != 1 || got[0] != "soc@example.com" {
		t.Fatalf("got %#v", got)
	}
}
