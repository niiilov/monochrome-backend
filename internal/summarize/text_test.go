package summarize

import (
	"strings"
	"testing"
)

func TestSplitSentences(t *testing.T) {
	got := SplitSentences("Конец. Начало второе!")
	if strings.Join(got, "|") != "Конец.|Начало второе!" {
		t.Fatalf("%#v", got)
	}
	got = SplitSentences("Срок 06.11.2026 уже прошёл. Дальше ждём.")
	if len(got) != 2 || !strings.Contains(got[0], "06.11.2026") {
		t.Fatalf("%#v", got)
	}
	got = SplitSentences("Банк работает в г. Москве и подтвердил факт.")
	if len(got) != 1 {
		t.Fatalf("сокращение разорвало фразу: %#v", got)
	}
	got = SplitSentences("Утекли 1.5 млн записей.")
	if len(got) != 1 || !strings.Contains(got[0], "1.5") {
		t.Fatalf("%#v", got)
	}
	if SplitSentences("  ") != nil {
		t.Fatal("пустая строка")
	}
}

func TestNormalizeConsequences(t *testing.T) {
	if got := NormalizeConsequences(""); got != NoConsequences {
		t.Fatalf("%q", got)
	}
	if got := NormalizeConsequences("нет данных"); got != NoConsequences {
		t.Fatalf("%q", got)
	}
	if got := NormalizeConsequences("Нет сведений о последствиях."); got != NoConsequences {
		t.Fatalf("%q", got)
	}
	const keep = "Клиенты банка не входили в сервис два часа."
	if got := NormalizeConsequences(keep); got != keep {
		t.Fatalf("%q", got)
	}
	em := string(rune(0x2014))
	got := NormalizeConsequences("Сервис" + em + "лежал")
	if strings.Contains(got, em) || !strings.Contains(got, "Сервис - лежал") {
		t.Fatalf("%q", got)
	}
}

func TestMergeHedge(t *testing.T) {
	got := mergeHedge("Банк подтвердил факт.", "по предварительным данным")
	if got != "Банк подтвердил факт. по предварительным данным." {
		t.Fatalf("%q", got)
	}
	again := mergeHedge(got, "по предварительным данным")
	if again != got {
		t.Fatalf("дубль: %q", again)
	}
}
