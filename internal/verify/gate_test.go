package verify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberdigest/internal/llm/mock"
)

func TestGateCleanDoesNotCallModel(t *testing.T) {
	w := sample()
	res, err := Gate(context.Background(), nil, w.model, w.events, w.arts, Options{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Degraded || res.Report.Retries != 0 {
		t.Fatalf("retries %d degraded %v", res.Report.Retries, res.Report.Degraded)
	}
	denyErrors(t, res.Findings)
	if !strings.Contains(res.DigestMD, "20") || !strings.Contains(res.Post, "20") || !strings.Contains(res.EmailText, "20") {
		t.Fatal("пропало число 20")
	}
}

func TestGateEmptyIssue(t *testing.T) {
	w := sample()
	w.model.Items = nil
	w.model.Empty = true
	w.model.Shortfall = "все материалы вне темы"
	res, err := Gate(context.Background(), nil, w.model, nil, w.arts, Options{})
	if err != nil {
		t.Fatal(err)
	}
	denyErrors(t, res.Findings)
	for _, text := range []string{res.DigestMD, res.Post, res.EmailText} {
		if !strings.Contains(text, "Подходящих событий за период нет") {
			t.Fatalf("нет фразы пустого выпуска:\n%s", text)
		}
	}
	if strings.Contains(res.DigestMD, "\n## ") {
		t.Fatalf("в пустом выпуске есть пункт:\n%s", res.DigestMD)
	}
}

func TestGateRetriesThenKeepsGroundedText(t *testing.T) {
	w := spoil(sample())
	client := mock.New()
	client.When("write_digest_item", "", itemPayload(
		"Утечка 20 записей",
		"В сообщении Роскомнадзор 6 ноября 2026 подтвердил: утекли 20 записей. Компания заявила о проверке. «данные скомпрометированы»",
		"",
		"",
	))
	res, err := Gate(context.Background(), client, w.model, w.events, w.arts, Options{MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Degraded {
		t.Fatalf("деградация не нужна: %+v", res.Findings)
	}
	if res.Report.Retries != 1 || client.CallsFor("write_digest_item") != 1 {
		t.Fatalf("retries %d calls %d", res.Report.Retries, client.CallsFor("write_digest_item"))
	}
	if strings.Contains(res.DigestMD, "99999") || strings.Contains(res.Post, "99999") {
		t.Fatal("выдуманное число осталось после успешного ретрая")
	}
	denyErrors(t, res.Findings)
	var hinted bool
	for _, req := range client.Requests() {
		if req.Task == "write_digest_item" && strings.Contains(req.User, "99999") {
			hinted = true
		}
	}
	if !hinted {
		t.Fatal("в повторный запрос не попала подсказка")
	}
}

func TestGateDegradesInventedNumberURLAndQuote(t *testing.T) {
	w := spoil(sample())
	client := mock.New()
	client.When("write_digest_item", "", itemPayload(
		"Утечка 99999 записей",
		"Утекли 99999 записей. «полностью выдумано». Компания заявила о проверке. Подробности: https://evil.example/phish",
		"",
		"",
	))
	res, err := Gate(context.Background(), client, w.model, w.events, w.arts, Options{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Report.Degraded {
		t.Fatal("ожидалась деградация")
	}
	if res.Report.Retries != 2 || client.CallsFor("write_digest_item") != 2 {
		t.Fatalf("retries %d calls %d", res.Report.Retries, client.CallsFor("write_digest_item"))
	}
	for _, text := range []string{res.DigestMD, res.Post, res.EmailText, res.EmailHTML} {
		if strings.Contains(text, "99999") {
			t.Fatalf("выдуманное число доехало до результата:\n%s", text)
		}
		if strings.Contains(text, "полностью выдумано") {
			t.Fatalf("выдуманная цитата доехала до результата:\n%s", text)
		}
		if strings.Contains(text, "evil.example") {
			t.Fatalf("выдуманный URL доехал до результата:\n%s", text)
		}
	}
	if strings.Contains(res.Model.Items[0].Essence, "99999") || strings.Contains(res.Model.Items[0].Title, "99999") {
		t.Fatal("модель сохранила выдуманное число")
	}
	if !strings.Contains(res.DigestMD, "20") || !strings.Contains(res.Post, "20") || !strings.Contains(res.EmailText, "20") {
		t.Fatalf("пропал факт 20:\n%s", res.DigestMD)
	}
	denyErrors(t, res.Findings)
	if res.Report.Findings == nil {
		t.Fatal("findings в отчёте не должен быть null")
	}
}

func spoil(w world) world {
	w.model = cloneModel(w.model)
	item := &w.model.Items[0]
	item.Title = "Утечка 99999 записей"
	item.Essence = "Утекли 99999 записей. «полностью выдумано». Компания заявила о проверке. Подробности: https://evil.example/phish"
	return w
}

func itemPayload(title, essence, consequences, hedge string) string {
	raw, err := json.Marshal(map[string]string{
		"title":        title,
		"essence":      essence,
		"consequences": consequences,
		"hedge_note":   hedge,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}
