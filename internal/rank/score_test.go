package rank

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
	"cyberdigest/internal/extract"
	"cyberdigest/internal/journal"
	"cyberdigest/internal/llm/mock"
	"cyberdigest/internal/prompts"
	"cyberdigest/internal/textutil"
)

func TestTotalSkipsUnknownAndKeepsZero(t *testing.T) {
	w := config.Default().Rank.Weights
	zero, one, two, three := 0, 1, 2, 3
	if got := totalOf(domain.Scores{}, w); got != 0 {
		t.Fatalf("empty total %v", got)
	}
	all := domain.Scores{Impact: &two, Scale: &two, Urgency: &two, Relevance: &two}
	if got := totalOf(all, w); got != 2 {
		t.Fatalf("even total %v", got)
	}
	withZero := domain.Scores{Impact: &zero, Scale: &three, Urgency: &three, Relevance: &three}
	withNil := domain.Scores{Scale: &three, Urgency: &three, Relevance: &three}
	if !DataUnknown(withNil) || DataUnknown(withZero) {
		t.Fatal("only a nil mark is unknown")
	}
	if !(totalOf(withNil, w) > totalOf(withZero, w)) {
		t.Fatalf("nil %v should outrank a real zero %v", totalOf(withNil, w), totalOf(withZero, w))
	}
	partial := domain.Scores{Impact: &three, Urgency: &one, Relevance: &two}
	want := (0.30*3 + 0.25*1 + 0.20*2) / (0.30 + 0.25 + 0.20)
	if math.Abs(totalOf(partial, w)-want) > 1e-9 {
		t.Fatalf("partial %v, want %v", totalOf(partial, w), want)
	}
}

func TestScoreRenormalizesAndFlagsUnknown(t *testing.T) {
	w := config.Default().Rank.Weights
	text := "Утекли персональные данные тысячи клиентов. Затронуты три банка. Патч не выпущен. Организации финансового сектора в России."
	nilImpact := scorePayload(nil, ip(3), ip(3), ip(3), "", scaleQuote, urgencyQuote, relevanceQuote)
	zeroImpact := scorePayload(ip(0), ip(3), ip(3), ip(3), impactQuote, scaleQuote, urgencyQuote, relevanceQuote)
	m := mock.New()
	m.When(taskScore, "E-nil", nilImpact)
	m.When(taskScore, "E-zero", zeroImpact)
	m.When(taskRationale, "", ratSentence())
	events := []domain.Event{
		testEvent("E-nil", "a-nil", text),
		testEvent("E-zero", "a-zero", text),
	}
	events[0].Total = 7.5
	j := journal.New()
	j.Add(domain.JournalEntry{ID: "a-nil"})
	j.Add(domain.JournalEntry{ID: "a-zero"})
	ctx := extract.WithNoter(context.Background(), j)

	got, err := Score(ctx, m, events, artsOf(events, text), config.Rank{Weights: w})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Total != 7.5 || events[0].Scores.Impact != nil {
		t.Fatal("score must not mutate the input event")
	}
	again, err := Score(ctx, m, events, artsOf(events, text), config.Rank{Weights: w})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Total != again[0].Total || got[1].Total != again[1].Total {
		t.Fatalf("not deterministic: %v then %v", []float64{got[0].Total, got[1].Total}, []float64{again[0].Total, again[1].Total})
	}
	if !DataUnknown(got[0].Scores) || got[0].Scores.Impact != nil || got[0].Scores.Scale == nil {
		t.Fatalf("nil impact lost: %+v", got[0].Scores)
	}
	if math.Abs(got[0].Total-3) > 1e-9 {
		t.Fatalf("renormalized %v, want 3", got[0].Total)
	}
	if DataUnknown(got[1].Scores) || got[1].Scores.Impact == nil || *got[1].Scores.Impact != 0 {
		t.Fatalf("zero must stay a score: %+v", got[1].Scores)
	}
	if !(got[0].Total > got[1].Total) {
		t.Fatalf("unknown impact %v should outrank a scored zero %v", got[0].Total, got[1].Total)
	}
	nilRow, _ := j.Get("a-nil")
	if !traceHas(nilRow, UnknownDataNote) {
		t.Fatalf("missing flag: %+v", nilRow.Trace)
	}
	zeroRow, _ := j.Get("a-zero")
	if traceHas(zeroRow, UnknownDataNote) {
		t.Fatalf("complete score flagged unknown: %+v", zeroRow.Trace)
	}
	for _, req := range m.Requests() {
		if strings.Contains(req.User, "max_events") || strings.Contains(req.User, "0.30") || strings.Contains(req.System, "max_events") {
			t.Fatalf("prompt leaks rank settings: %s", req.Task)
		}
	}
}

func TestScoreDropsSubstitutedQuote(t *testing.T) {
	text := impactQuote + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	payload := scorePayload(ip(3), ip(2), ip(3), ip(1), "Полная выдумка про утечку", scaleQuote, urgencyQuote, relevanceQuote)
	m := mock.New()
	m.When(taskScore, "E01", payload)
	m.When(taskRationale, "E01", ratSentence())
	ev := testEvent("E01", "a", text)
	j := journal.New()
	j.Add(domain.JournalEntry{ID: "a"})
	got, err := Score(extract.WithNoter(context.Background(), j), m, []domain.Event{ev}, artsOf([]domain.Event{ev}, text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Scores.Impact != nil {
		t.Fatal("substituted quote kept a score")
	}
	if _, ok := got[0].Scores.Evidence[keyImpact]; ok {
		t.Fatal("substituted quote stored as evidence")
	}
	if got[0].Scores.Evidence[keyScale] != scaleQuote || got[0].Scores.Scale == nil || *got[0].Scores.Scale != 2 {
		t.Fatalf("valid quote dropped: %+v", got[0].Scores)
	}
	row, _ := j.Get("a")
	if !traceHas(row, "цитата не найдена") {
		t.Fatalf("trace: %+v", row.Trace)
	}
	if math.Abs(got[0].Total-totalOf(got[0].Scores, config.Default().Rank.Weights)) > 1e-9 {
		t.Fatal("total does not match the kept marks")
	}
}

func TestScoreDropsOutOfRangeAndNoDataMark(t *testing.T) {
	text := impactQuote + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	raw := map[string]any{
		"impact":    map[string]any{"value": 4, "quote": impactQuote, "note": ""},
		"scale":     map[string]any{"value": 3, "quote": scaleQuote, "note": "нет данных"},
		"urgency":   map[string]any{"value": 3, "quote": urgencyQuote, "note": ""},
		"relevance": map[string]any{"value": -1, "quote": relevanceQuote, "note": ""},
	}
	body, _ := json.Marshal(raw)
	m := mock.New()
	m.When(taskScore, "E01", string(body))
	m.When(taskRationale, "E01", ratSentence())
	ev := testEvent("E01", "a", text)
	got, err := Score(context.Background(), m, []domain.Event{ev}, artsOf([]domain.Event{ev}, text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Scores.Impact != nil || got[0].Scores.Scale != nil || got[0].Scores.Relevance != nil {
		t.Fatalf("bad marks kept: %+v", got[0].Scores)
	}
	if got[0].Scores.Urgency == nil || *got[0].Scores.Urgency != 3 {
		t.Fatalf("urgency: %+v", got[0].Scores)
	}
}

func TestScoreKeepsQuoteWithDifferentSpaces(t *testing.T) {
	spaced := "Утекли  персональные данные тысячи клиентов"
	text := spaced + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	payload := scorePayload(ip(2), ip(1), ip(1), ip(1), impactQuote, scaleQuote, urgencyQuote, relevanceQuote)
	m := mock.New()
	m.When(taskScore, "E01", payload)
	m.When(taskRationale, "E01", ratSentence())
	ev := testEvent("E01", "a", text)
	got, err := Score(context.Background(), m, []domain.Event{ev}, artsOf([]domain.Event{ev}, text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Scores.Evidence[keyImpact] != spaced {
		t.Fatalf("evidence %q", got[0].Scores.Evidence[keyImpact])
	}
	if !strings.Contains(text, got[0].Scores.Evidence[keyImpact]) {
		t.Fatal("evidence is not a slice of the article")
	}
}

func TestScoreRejectsImportanceAndBuildsFallback(t *testing.T) {
	text := impactQuote + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	m := mock.New()
	m.When(taskScore, "E01", fullScore())
	m.When(taskRationale, "E01", rat("Это событие высокой важности для всех банков."))
	ev := testEvent("E01", "a", text)
	got, err := Score(context.Background(), m, []domain.Event{ev}, artsOf([]domain.Event{ev}, text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if importanceRE.MatchString(got[0].Rationale) {
		t.Fatalf("importance leaked: %s", got[0].Rationale)
	}
	if !strings.Contains(got[0].Rationale, "персональные данные") || !strings.Contains(got[0].Rationale, "три банка") {
		t.Fatalf("fallback missed facts: %s", got[0].Rationale)
	}
	if sentenceCount(got[0].Rationale) != 1 {
		t.Fatalf("not one sentence: %s", got[0].Rationale)
	}
}

func TestScoreSaysWhenFactsAreMissing(t *testing.T) {
	m := mock.New()
	m.When(taskScore, "E01", scorePayload(nil, nil, nil, nil, "", "", "", ""))
	m.When(taskRationale, "E01", rat("Сведений недостаточно, но событие заметное."))
	ev := domain.Event{ID: "E01", ArticleIDs: []string{"a"}, PrimaryArticleID: "a"}
	arts := map[string]domain.Article{"a": {ID: "a", Text: "Коротко о сбое."}}
	got, err := Score(context.Background(), m, []domain.Event{ev}, arts, config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"последствиях не приведены", "масштабе не приведены", "срочности не приведены"} {
		if !strings.Contains(got[0].Rationale, part) {
			t.Fatalf("rationale %q misses %s", got[0].Rationale, part)
		}
	}
	if got[0].Total != 0 || !DataUnknown(got[0].Scores) {
		t.Fatalf("empty scores: total %v %+v", got[0].Total, got[0].Scores)
	}
}

func TestScoreIsolatesOneFailure(t *testing.T) {
	text := impactQuote + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	m := mock.New()
	m.Fail(taskScore, "E-bad", errors.New("временный сбой"))
	m.When(taskScore, "E-ok", fullScore())
	m.When(taskRationale, "E-ok", ratSentence())
	events := []domain.Event{
		{ID: "E-bad", ArticleIDs: []string{"bad"}, PrimaryArticleID: "bad"},
		testEvent("E-ok", "ok", text),
	}
	res, err := ScoreResult(context.Background(), m, events, artsOf(events[1:], text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("events %d", len(res.Events))
	}
	if res.Errors["E-bad"] == "" || res.Events[0].Total != 0 {
		t.Fatalf("errors %v total %v", res.Errors, res.Events[0].Total)
	}
	if !strings.Contains(res.Events[0].Rationale, "не приведены") {
		t.Fatalf("bad rationale %s", res.Events[0].Rationale)
	}
	if res.Events[1].Scores.Impact == nil || res.Events[1].Total == 0 {
		t.Fatalf("good event dropped: %+v", res.Events[1].Scores)
	}
	if m.CallsFor(taskRationale) != 1 {
		t.Fatalf("rationale calls %d", m.CallsFor(taskRationale))
	}
}

func TestScoreRationaleFailureKeepsMarks(t *testing.T) {
	text := impactQuote + ". " + scaleQuote + ". " + urgencyQuote + ". " + relevanceQuote + "."
	m := mock.New()
	m.When(taskScore, "E01", fullScore())
	m.Fail(taskRationale, "E01", errors.New("обрыв"))
	ev := testEvent("E01", "a", text)
	res, err := ScoreResult(context.Background(), m, []domain.Event{ev}, artsOf([]domain.Event{ev}, text), config.Default().Rank)
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors["E01"] == "" || res.Events[0].Scores.Impact == nil || res.Events[0].Total == 0 {
		t.Fatalf("marks lost: %+v %v", res.Events[0].Scores, res.Errors)
	}
	if !strings.Contains(res.Events[0].Rationale, "персональные данные") {
		t.Fatalf("rationale %s", res.Events[0].Rationale)
	}
}

func TestScoreEmptyAndCanceled(t *testing.T) {
	got, err := Score(context.Background(), nil, nil, nil, config.Rank{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := mock.New()
	_, err = Score(ctx, m, []domain.Event{{ID: "E01"}}, nil, config.Rank{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if m.Calls() != 0 {
		t.Fatalf("calls %d", m.Calls())
	}
	_, err = Score(context.Background(), nil, []domain.Event{{ID: "E01"}}, nil, config.Rank{})
	if err == nil {
		t.Fatal("nil client")
	}
}

func TestPromptFiles(t *testing.T) {
	names := []string{
		"score_event.system.tmpl",
		"score_event.user.tmpl",
		"rationale.system.tmpl",
		"rationale.user.tmpl",
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "..", "prompts", name))
		if err != nil {
			t.Fatal(err)
		}
		if textutil.HasLongDash(string(raw)) {
			t.Fatalf("long dash in %s", name)
		}
	}
	if _, _, ver, err := prompts.Render("score_event", scorePrompt{Dossier: "Событие E01"}); err != nil || ver != "2" {
		t.Fatalf("score_event %s %v", ver, err)
	}
	if _, _, ver, err := prompts.Render("rationale", rationalePrompt{Dossier: "Событие E01", Consequences: "нет данных", Scale: "нет данных", Urgency: "нет данных"}); err != nil || ver != "2" {
		t.Fatalf("rationale %s %v", ver, err)
	}
}
