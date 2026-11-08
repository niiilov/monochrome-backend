package critic

import (
	"encoding/json"

	"mono/internal/llm/mock"
)

// ScriptEvalMock registers answers for Gate + critic eval without the network.
// Planted unsupported claims trigger a grounded rewrite on write_digest_item.
func ScriptEvalMock(m *mock.Client) *mock.Client {
	if m == nil {
		m = mock.New()
	}
	// Critic: grounded train retelling is supported.
	m.When(Task, "840 000", mustJSON(Report{Claims: []Claim{
		{Text: "утечка 840 000 записей", Verdict: VerdictSupported, Quote: "наружу попали 840 000 записей", Reason: "есть в источнике"},
		{Text: "пароли отсутствуют", Verdict: VerdictSupported, Quote: "пароли и диагнозы, как утверждает сервис, в выгрузке отсутствуют", Reason: "есть в источнике"},
	}}))
	// Critic: planted case. Registered later so equal-score match wins over "840 000".
	m.When(Task, plantedLieCode, mustJSON(Report{Claims: []Claim{
		{Text: plantedLieCode, Verdict: VerdictUnsupported, Quote: "", Reason: "в источнике нет кражи кода"},
		{Text: plantedLieOpen, Verdict: VerdictUnsupported, Quote: "", Reason: "диагнозы по источнику отсутствуют"},
		{Text: plantedLieRansom, Verdict: VerdictUnsupported, Quote: "", Reason: "выкупа в источнике нет"},
		{Text: "утечка 840 000 записей", Verdict: VerdictSupported, Quote: "наружу попали 840 000 записей", Reason: "есть в источнике"},
	}}))
	// Fallback critic answer.
	m.When(Task, "", mustJSON(Report{Claims: []Claim{
		{Text: "общее утверждение", Verdict: VerdictSupported, Quote: "840 000", Reason: "по умолчанию"},
	}}))
	// After critic flags unsupported, Gate asks summarize to rewrite without lies.
	m.When("write_digest_item", "", mustJSON(map[string]string{
		"title":        "Утечка 840 000 записей в «Медкарта-Онлайн»",
		"essence":      "6 ноября 2026 ТАСС сообщил об утечке сервиса «Медкарта-Онлайн»: наружу попали 840 000 записей с фамилиями, полисами ОМС и телефонами. Как утверждает сервис, пароли и диагнозы в выгрузке отсутствуют.",
		"consequences": "В источнике сведения о последствиях не приведены.",
		"hedge_note":   "как утверждает сервис",
	}))
	// Post rewrite is optional; Gate may call write_post when not forcing template.
	m.When("write_post", "", mustJSON(map[string]any{
		"items": []map[string]any{{
			"n":     1,
			"title": "Утечка 840 000 записей",
			"text":  "Утекли 840 000 записей сервиса «Медкарта-Онлайн».",
		}},
	}))
	return m
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
