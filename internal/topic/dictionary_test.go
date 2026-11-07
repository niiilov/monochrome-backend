package topic

import (
	"strings"
	"testing"

	"cyberdigest/internal/domain"
)

func TestStemWordForms(t *testing.T) {
	cases := []struct {
		text string
		term string
	}{
		{"фишинговые письма сотрудникам", "фишинг"},
		{"фишингextra не отдельное слово", ""},
		{"кибербезопасности компании", "кибербезопасность"},
		{"утечке персональных данных", "персональные данные"},
		{"учётной записи администратора", "учётная запись"},
		{"учетные записи заблокированы", "учётная запись"},
		{"компрометация учётной записи", "компрометация учётной записи"},
		{"подбор пароля к шлюзу", "подбор пароля"},
		{"сертификатов отозвано 10", "сертификат"},
		{"сертификация СЗИ завершена", "сертификация СЗИ"},
	}
	for _, tc := range cases {
		hits := Prefilter(domain.Article{Text: tc.text})
		if tc.term == "" {
			for _, hit := range hits {
				if hit == "фишинг" {
					t.Fatalf("%q matched фишинг: %v", tc.text, hits)
				}
			}
			continue
		}
		if !contains(hits, tc.term) {
			t.Fatalf("%q: want %q in %v", tc.text, tc.term, hits)
		}
	}
}

func TestAbbreviationsAreWholeWords(t *testing.T) {
	if hits := Prefilter(domain.Article{Text: "гибкий график и кибербезопасность"}); contains(hits, "ИБ") {
		t.Fatalf("ИБ inside a longer word: %v", hits)
	}
	hits := Prefilter(domain.Article{Title: "Требования по ИБ и КИИ", Text: "Обработка ПДн. Атака DDoS и ddos."})
	for _, term := range []string{"ИБ", "КИИ", "ПДн", "DDoS"} {
		if !contains(hits, term) {
			t.Fatalf("missing %s in %v", term, hits)
		}
	}
	if got := Prefilter(domain.Article{Text: "mydoos и DDoSx рядом"}); contains(got, "DDoS") {
		t.Fatalf("DDoS without a boundary: %v", got)
	}
	if got := Prefilter(domain.Article{Text: "по дороге в офис"}); len(got) != 0 {
		t.Fatalf("preposition по matched: %v", got)
	}
}

func TestEnglishWordBoundaries(t *testing.T) {
	hits := Prefilter(domain.Article{Text: "A ransomware note and a zero-day exploit"})
	for _, term := range []string{"ransomware", "zero-day", "exploit"} {
		if !contains(hits, term) {
			t.Fatalf("missing %s in %v", term, hits)
		}
	}
	if got := Prefilter(domain.Article{Text: "ransomwareextra is not malwareX"}); len(got) != 0 {
		t.Fatalf("english substring matched: %v", got)
	}
	weak := Scan("", "The backup of a password and a token")
	wantWeak := map[string]bool{"backup": true, "пароль": true, "токен": true}
	if len(weak) != len(wantWeak) {
		t.Fatalf("weak english hits: %+v", weak)
	}
	for _, hit := range weak {
		if !hit.Weak || !wantWeak[hit.Term] {
			t.Fatalf("unexpected hit %+v", hit)
		}
	}
}

func TestWeakTermsDoNotStandAlone(t *testing.T) {
	hits := Scan("Новая база данных", "Сертификат и учётная запись. Ведомство ФСТЭК. Роскомнадзор.")
	if HasStrong(hits) {
		t.Fatalf("homonyms looked strong: %+v", hits)
	}
	for _, term := range []string{"база данных", "сертификат", "учётная запись", "ФСТЭК", "Роскомнадзор"} {
		found := false
		for _, hit := range hits {
			if hit.Term == term && hit.Weak {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing weak %s in %+v", term, hits)
		}
	}
	strong := Scan("", "приказ ФСТЭК о компрометации учётной записи")
	if !HasStrong(strong) {
		t.Fatalf("expected a strong phrase: %+v", strong)
	}
	if !contains(Prefilter(domain.Article{Text: "приказ ФСТЭК"}), "приказ ФСТЭК") {
		t.Fatalf("phrase missing: %+v", strong)
	}
}

func TestCaseFold(t *testing.T) {
	hits := Prefilter(domain.Article{Text: "RaNsOmWaRe и УТЕЧКА ПДН"})
	if !contains(hits, "ransomware") || !contains(hits, "утечка данных") || !contains(hits, "ПДн") {
		t.Fatalf("%v", hits)
	}
}

func contains(list []string, term string) bool {
	for _, item := range list {
		if item == term {
			return true
		}
	}
	return false
}

func TestNoLongDashInDictionaryNames(t *testing.T) {
	for _, e := range entries() {
		if strings.ContainsRune(e.name, '\u2014') || strings.ContainsRune(e.name, '\u2013') || strings.ContainsRune(e.group, '\u2014') || strings.ContainsRune(e.group, '\u2013') {
			t.Fatalf("long dash in %q / %q", e.group, e.name)
		}
	}
}
