package rank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/extract"
	"mono/internal/llm"
	"mono/internal/prompts"
)

const (
	taskScore          = "score_event"
	taskRationale      = "rationale"
	stageRank          = "rank"
	maxScoreTokens     = 900
	maxRationaleTokens = 400

	keyImpact    = "impact"
	keyScale     = "scale"
	keyUrgency   = "urgency"
	keyRelevance = "relevance"
)

// scoreSchema is sent when the client supports response_format.
var scoreSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["impact", "scale", "urgency", "relevance"],
  "properties": {
    "impact": {"$ref": "#/$defs/criterion"},
    "scale": {"$ref": "#/$defs/criterion"},
    "urgency": {"$ref": "#/$defs/criterion"},
    "relevance": {"$ref": "#/$defs/criterion"}
  },
  "$defs": {
    "criterion": {
      "type": "object",
      "additionalProperties": false,
      "required": ["value", "quote", "note"],
      "properties": {
        "value": {"type": ["integer", "null"]},
        "quote": {"type": "string"},
        "note": {"type": "string"}
      }
    }
  }
}`)

var rationaleSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["rationale"],
  "properties": {
    "rationale": {"type": "string"}
  }
}`)

type criterion struct {
	Value *int   `json:"value"`
	Quote string `json:"quote"`
	Note  string `json:"note"`
}

type scoreVerdict struct {
	Impact    criterion `json:"impact"`
	Scale     criterion `json:"scale"`
	Urgency   criterion `json:"urgency"`
	Relevance criterion `json:"relevance"`
}

type rationaleVerdict struct {
	Rationale string `json:"rationale"`
}

// Result is the scoring outcome. Events keep input order.
// Errors holds a per-event failure that did not stop the rest of the run.
type Result struct {
	Events []domain.Event
	Errors map[string]string
}

// DataUnknown reports whether at least one criterion was dropped or never set.
// Score records UnknownDataNote on the journal trace in that case.
func DataUnknown(s domain.Scores) bool {
	return s.Impact == nil || s.Scale == nil || s.Urgency == nil || s.Relevance == nil
}

// Score asks the model to mark each event, checks quotes, and fills Total.
// An empty list returns an empty result and a nil error. One model failure
// does not drop the other events: the failed event keeps a zero total and
// a rationale built from the publication fields.
func Score(ctx context.Context, c llm.Client, events []domain.Event, arts map[string]domain.Article, cfg config.Rank) ([]domain.Event, error) {
	res, err := ScoreResult(ctx, c, events, arts, cfg)
	if err != nil {
		return res.Events, err
	}
	return res.Events, nil
}

// ScoreResult is Score plus per-event errors for the journal.
func ScoreResult(ctx context.Context, c llm.Client, events []domain.Event, arts map[string]domain.Article, cfg config.Rank) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(events) == 0 {
		return Result{}, nil
	}
	if c == nil {
		return Result{}, errors.New("rank: клиент не задан")
	}
	res := Result{Errors: map[string]string{}}
	type slot struct {
		ev  domain.Event
		err error
	}
	slots := make([]slot, len(events))
	fanout(ctx, len(events), func(ctx context.Context, i int) {
		ev, err := scoreOne(ctx, c, events[i], arts, cfg.Weights)
		slots[i] = slot{ev: ev, err: err}
	})
	if err := ctx.Err(); err != nil {
		return res, err
	}
	for i, ev := range events {
		scored := slots[i].ev
		if slots[i].err != nil {
			res.Errors[eventKey(ev.ID)] = slots[i].err.Error()
			emit(ctx, scored, []string{"Оценка не выполнена: " + clipRunes(slots[i].err.Error(), 200)})
		}
		res.Events = append(res.Events, scored)
	}
	if len(res.Errors) == 0 {
		res.Errors = nil
	}
	return res, nil
}

func scoreOne(ctx context.Context, c llm.Client, ev domain.Event, arts map[string]domain.Article, w config.Weights) (domain.Event, error) {
	out := cloneEvent(ev)
	articles := eventArticles(out, arts)
	system, user, _, err := prompts.Render(taskScore, scorePrompt{Dossier: scoreDossier(out, articles)})
	if err != nil {
		return failedEvent(out), err
	}
	var parsed scoreVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskScore,
		System:      system,
		User:        user,
		Schema:      scoreSchema,
		Temperature: 0,
		MaxTokens:   maxScoreTokens,
	}, &parsed)
	if err != nil {
		return failedEvent(out), fmt.Errorf("оценка события %s: %w", eventKey(out.ID), err)
	}
	notes := applyScores(&out, parsed, articles, w)
	g := eventGrounds(out)
	sentence, src, rerr := writeRationale(ctx, c, out, g)
	out.Rationale = sentence
	if src == rationaleFallback {
		notes = append(notes, "Обоснование собрано из сведений публикаций.")
	}
	if DataUnknown(out.Scores) {
		notes = append(notes, UnknownDataNote+": "+strings.Join(missingCriteria(out.Scores), ", ")+".")
	}
	emit(ctx, out, notes)
	if rerr != nil {
		return out, fmt.Errorf("обоснование события %s: %w", eventKey(out.ID), rerr)
	}
	return out, nil
}

func applyScores(ev *domain.Event, parsed scoreVerdict, arts []domain.Article, w config.Weights) []string {
	var notes []string
	ev.Scores = domain.Scores{}
	evidence := map[string]string{}
	set := func(key, title string, raw criterion) *int {
		value, quote, note := checkCriterion(title, raw, arts)
		if note != "" {
			notes = append(notes, note)
		}
		if value != nil && quote != "" {
			evidence[key] = quote
		}
		return value
	}
	ev.Scores.Impact = set(keyImpact, "последствия", parsed.Impact)
	ev.Scores.Scale = set(keyScale, "масштаб", parsed.Scale)
	ev.Scores.Urgency = set(keyUrgency, "срочность", parsed.Urgency)
	ev.Scores.Relevance = set(keyRelevance, "релевантность", parsed.Relevance)
	if len(evidence) == 0 {
		ev.Scores.Evidence = nil
	} else {
		ev.Scores.Evidence = evidence
	}
	ev.Total = totalOf(ev.Scores, w)
	return notes
}

func checkCriterion(title string, raw criterion, arts []domain.Article) (*int, string, string) {
	note := strings.TrimSpace(raw.Note)
	if raw.Value == nil || isNoData(note) {
		if raw.Value != nil && isNoData(note) {
			return nil, "", title + ": модель указала оценку и пометку «нет данных», оценка сброшена."
		}
		return nil, "", title + ": нет данных."
	}
	if *raw.Value < 0 || *raw.Value > 3 {
		return nil, "", title + ": значение вне диапазона 0-3, оценка сброшена."
	}
	exact, ok := findQuote(arts, raw.Quote)
	if !ok {
		return nil, "", title + ": цитата не найдена в статьях события, оценка сброшена."
	}
	return copyInt(*raw.Value), exact, ""
}

func isNoData(note string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(note)), NoDataNote)
}

const (
	rationaleModel = iota
	rationaleFallback
)

func writeRationale(ctx context.Context, c llm.Client, ev domain.Event, g grounds) (string, int, error) {
	fallback := fallbackRationale(g)
	system, user, _, err := prompts.Render(taskRationale, rationalePrompt{
		Dossier:      rationaleDossier(ev),
		Consequences: displayGround(g.Consequences),
		Scale:        displayGround(g.Scale),
		Urgency:      displayGround(g.Urgency),
	})
	if err != nil {
		return fallback, rationaleFallback, err
	}
	var parsed rationaleVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskRationale,
		System:      system,
		User:        user,
		Schema:      rationaleSchema,
		Temperature: 0,
		MaxTokens:   maxRationaleTokens,
	}, &parsed)
	if err != nil {
		return fallback, rationaleFallback, err
	}
	if sentence, ok := acceptRationale(parsed.Rationale, g); ok {
		return sentence, rationaleModel, nil
	}
	return fallback, rationaleFallback, nil
}

// totalOf is the weighted mean of the known marks.
// Nil marks are left out of both the numerator and the denominator.
func totalOf(s domain.Scores, w config.Weights) float64 {
	num, den := 0.0, 0.0
	add := func(v *int, weight float64) {
		if v == nil || weight <= 0 {
			return
		}
		num += weight * float64(*v)
		den += weight
	}
	add(s.Impact, w.Impact)
	add(s.Scale, w.Scale)
	add(s.Urgency, w.Urgency)
	add(s.Relevance, w.Relevance)
	if den == 0 {
		return 0
	}
	return num / den
}

func missingCriteria(s domain.Scores) []string {
	var out []string
	if s.Impact == nil {
		out = append(out, "последствия")
	}
	if s.Scale == nil {
		out = append(out, "масштаб")
	}
	if s.Urgency == nil {
		out = append(out, "срочность")
	}
	if s.Relevance == nil {
		out = append(out, "релевантность")
	}
	return out
}

func failedEvent(ev domain.Event) domain.Event {
	g := eventGrounds(ev)
	clearScores(&ev)
	ev.Rationale = fallbackRationale(g)
	return ev
}

func clearScores(ev *domain.Event) {
	ev.Scores = domain.Scores{}
	ev.Total = 0
}

func emit(ctx context.Context, ev domain.Event, notes []string) {
	if len(notes) == 0 {
		return
	}
	for _, id := range noteIDs(ev) {
		for _, note := range notes {
			extract.Note(ctx, stageRank, id, note)
		}
	}
}

func cloneEvent(ev domain.Event) domain.Event {
	ev.Mentions = append([]domain.Mention(nil), ev.Mentions...)
	ev.ArticleIDs = append([]string(nil), ev.ArticleIDs...)
	ev.ContextArticleIDs = append([]string(nil), ev.ContextArticleIDs...)
	ev.Conflicts = append([]domain.Conflict(nil), ev.Conflicts...)
	if ev.Scores.Evidence != nil {
		cp := make(map[string]string, len(ev.Scores.Evidence))
		for k, v := range ev.Scores.Evidence {
			cp[k] = v
		}
		ev.Scores.Evidence = cp
	}
	return ev
}

func copyInt(n int) *int {
	v := n
	return &v
}

func eventKey(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "event"
	}
	return id
}
