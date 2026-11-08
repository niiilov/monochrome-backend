package topic

import "testing"

func TestMatchesKeywords(t *testing.T) {
	if !MatchesKeywords("Утечки в банке", "Подробности позже.", []string{"утечка"}) {
		t.Fatal("stem")
	}
	if !MatchesKeywords("Патч", "Исправлена CVE-2024-3094 в xz.", []string{"CVE-2024-3094"}) {
		t.Fatal("cve")
	}
	if MatchesKeywords("CVENT купил стартап", "", []string{"CVE"}) {
		t.Fatal("token boundary")
	}
	if MatchesKeywords("Фишинг", "Письмо", nil) {
		t.Fatal("empty keywords")
	}
	if !MatchesKeywords("Отчёт", "Новая утечка данных клиентов.", []string{"утечка данных"}) {
		t.Fatal("phrase")
	}
}
