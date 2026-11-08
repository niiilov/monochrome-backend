package journal

import (
	"strings"
	"testing"
	"time"

	"mono/internal/domain"
	"mono/internal/timeutil"
)

func TestReasonTemplates(t *testing.T) {
	period := timeutil.NewPeriod(time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK))
	cases := []struct {
		code   domain.ReasonCode
		data   Data
		status domain.Status
		want   string
	}{
		{domain.ReasonSelected, Data{Rationale: "утечка 840 000 записей"}, domain.StatusIncluded, "Событие вошло в выпуск: утечка 840 000 записей"},
		{domain.ReasonMerged, Data{EventID: "E01"}, domain.StatusMerged, "Сообщение о том же событии (E01), ссылка сохранена в источниках"},
		{domain.ReasonOffTopic, Data{ModelReason: "спорт"}, domain.StatusExcluded, "Материал не относится к теме информационной безопасности: спорт"},
		{domain.ReasonAdvertising, Data{}, domain.StatusExcluded, "Рекламный материал, совпадение терминов не делает его новостью"},
		{domain.ReasonOutOfPeriod, Data{Period: period}, domain.StatusExcluded, "Публикация вне периода 06.11.2026 12:00 - 07.11.2026 12:00 МСК"},
		{domain.ReasonDateUnconfirmed, Data{}, domain.StatusExcluded, "Дату публикации подтвердить нельзя, материал исключён из основного дайджеста"},
		{domain.ReasonStaleNoNewFacts, Data{}, domain.StatusExcluded, "Повтор раннего события без новых фактов"},
		{domain.ReasonInsufficient, Data{}, domain.StatusExcluded, "Данных для пересказа недостаточно"},
		{domain.ReasonLowerPriority, Data{Place: 6, Total: 8, Why: "балл ниже выбранных"}, domain.StatusExcluded, "Уступило по приоритету: место 6 из 8, балл ниже выбранных"},
		{domain.ReasonReadError, Data{}, domain.StatusExcluded, "Не удалось прочитать запись"},
		{domain.ReasonEmptyInput, Data{}, domain.StatusExcluded, "Пустой файл или запись"},
		{domain.ReasonDuplicateID, Data{}, domain.StatusExcluded, "Повторный ID в наборе, использована первая запись"},
		{domain.ReasonSourceDown, Data{}, domain.StatusExcluded, "Источник недоступен"},
	}
	seen := map[domain.ReasonCode]bool{}
	for _, tc := range cases {
		seen[tc.code] = true
		status, ok := StatusFor(tc.code)
		if !ok || status != tc.status {
			t.Fatalf("%s status %q ok %v", tc.code, status, ok)
		}
		got := Text(tc.code, tc.data)
		if got != tc.want {
			t.Fatalf("%s: %q", tc.code, got)
		}
		if strings.ContainsRune(got, '\u2014') || strings.ContainsRune(got, '\u2013') {
			t.Fatalf("long dash in %q", got)
		}
	}
	all := []domain.ReasonCode{
		domain.ReasonSelected, domain.ReasonMerged, domain.ReasonOffTopic, domain.ReasonAdvertising,
		domain.ReasonOutOfPeriod, domain.ReasonDateUnconfirmed, domain.ReasonStaleNoNewFacts,
		domain.ReasonInsufficient, domain.ReasonLowerPriority, domain.ReasonReadError,
		domain.ReasonEmptyInput, domain.ReasonDuplicateID, domain.ReasonSourceDown,
	}
	for _, code := range all {
		if !seen[code] {
			t.Fatalf("template missing for %s", code)
		}
	}
	if _, ok := StatusFor("nope"); ok || Text("nope", Data{}) != "" {
		t.Fatal("unknown code must be empty")
	}
}
