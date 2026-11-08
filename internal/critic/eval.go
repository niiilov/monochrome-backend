package critic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mono/internal/domain"
	"mono/internal/llm"
	"mono/internal/verify"
)

// CaseName identifies one eval fixture group.
type CaseName string

const (
	CaseTrain   CaseName = "train"
	CasePlanted CaseName = "planted"
)

// ModeName is a gate run with or without the critic.
type ModeName string

const (
	ModeWithout ModeName = "without_critic"
	ModeWith    ModeName = "with_critic"
)

// CaseSpec is one fixed digest plus etalon markers for discrepancy counting.
type CaseSpec struct {
	Name       CaseName
	Title      string
	Model      domain.DigestModel
	Events     []domain.Event
	Articles   map[string]domain.Article
	Forbidden  []string // planted lies; each hit counts as a discrepancy
	Required   []string // grounded facts that must remain
	EtalonMiss int      // expected discrepancy count without critic (for the report)
}

// ModeResult is metrics for one case and one critic mode.
type ModeResult struct {
	Mode            ModeName `json:"mode"`
	Discrepancies   int      `json:"discrepancies"`
	DurationMS      int64    `json:"duration_ms"`
	InputTokens     int      `json:"input_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	CallsTotal      int      `json:"calls_total"`
	CriticCalls     int      `json:"critic_calls"`
	Cost            *float64 `json:"cost"`
	CostUnavailable bool     `json:"cost_unavailable"`
	Retries         int      `json:"retries"`
	Degraded        bool     `json:"degraded"`
	ForbiddenHits   []string `json:"forbidden_hits"`
	MissingRequired []string `json:"missing_required"`
}

// CaseResult compares with-critic and without-critic runs.
type CaseResult struct {
	Name    CaseName     `json:"name"`
	Title   string       `json:"title"`
	Without ModeResult   `json:"without_critic"`
	With    ModeResult   `json:"with_critic"`
	Delta   CaseDelta    `json:"delta"`
}

// CaseDelta is with_critic minus without_critic.
type CaseDelta struct {
	Discrepancies int     `json:"discrepancies"`
	DurationMS    int64   `json:"duration_ms"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	Cost          *float64 `json:"cost,omitempty"`
}

// EvalReport is written to docs/critic_eval.json.
type EvalReport struct {
	GeneratedAt string       `json:"generated_at"`
	Mock        bool         `json:"mock"`
	Cases       []CaseResult `json:"cases"`
	Summary     string       `json:"summary"`
}

// RunEval compares critic on/off for the built-in cases.
// client is shared; meter must wrap the same client when non-nil.
func RunEval(ctx context.Context, client llm.Client, meter *llm.Meter, mock bool) (EvalReport, error) {
	if client == nil {
		return EvalReport{}, fmt.Errorf("critic eval: клиент LLM не задан")
	}
	cases := BuiltinCases()
	out := EvalReport{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Mock:        mock,
		Cases:       make([]CaseResult, 0, len(cases)),
	}
	for _, spec := range cases {
		without, err := runMode(ctx, client, meter, spec, false)
		if err != nil {
			return EvalReport{}, err
		}
		with, err := runMode(ctx, client, meter, spec, true)
		if err != nil {
			return EvalReport{}, err
		}
		cr := CaseResult{
			Name:    spec.Name,
			Title:   spec.Title,
			Without: without,
			With:    with,
			Delta: CaseDelta{
				Discrepancies: with.Discrepancies - without.Discrepancies,
				DurationMS:    with.DurationMS - without.DurationMS,
				InputTokens:   with.InputTokens - without.InputTokens,
				OutputTokens:  with.OutputTokens - without.OutputTokens,
			},
		}
		if with.Cost != nil && without.Cost != nil {
			v := *with.Cost - *without.Cost
			cr.Delta.Cost = &v
		}
		out.Cases = append(out.Cases, cr)
	}
	out.Summary = summarizeEval(out)
	return out, nil
}

func runMode(ctx context.Context, client llm.Client, meter *llm.Meter, spec CaseSpec, withCritic bool) (ModeResult, error) {
	before := snapshot(meter)
	start := time.Now()
	opts := verify.Options{MaxRetries: 2}
	if withCritic {
		opts.CriticEnabled = true
		opts.Critic = GateCritic{Client: client}
	}
	res, err := verify.Gate(ctx, client, cloneDigest(spec.Model), cloneEvents(spec.Events), cloneArts(spec.Articles), opts)
	if err != nil {
		return ModeResult{}, err
	}
	elapsed := time.Since(start)
	after := snapshot(meter)
	hits, missing := scoreText(joinedTexts(res), spec.Forbidden, spec.Required)
	mode := ModeWithout
	if withCritic {
		mode = ModeWith
	}
	out := ModeResult{
		Mode:            mode,
		Discrepancies:   len(hits) + len(missing),
		DurationMS:      elapsed.Milliseconds(),
		InputTokens:     after.InputTokens - before.InputTokens,
		OutputTokens:    after.OutputTokens - before.OutputTokens,
		CallsTotal:      after.CallsTotal - before.CallsTotal,
		CriticCalls:     after.ByTask[Task].Calls - before.ByTask[Task].Calls,
		CostUnavailable: after.CostUnavailable || before.CostUnavailable,
		Retries:         res.Report.Retries,
		Degraded:        res.Report.Degraded,
		ForbiddenHits:   hits,
		MissingRequired: missing,
	}
	if after.Cost != nil && before.Cost != nil {
		v := *after.Cost - *before.Cost
		out.Cost = &v
	} else if after.Cost != nil && before.Cost == nil {
		v := *after.Cost
		out.Cost = &v
	}
	return out, nil
}

func snapshot(m *llm.Meter) domain.LLMStats {
	if m == nil {
		return domain.LLMStats{ByTask: map[string]domain.LLMTaskStats{}, CostUnavailable: true}
	}
	return m.Snapshot()
}

func joinedTexts(res verify.Result) string {
	var b strings.Builder
	b.WriteString(res.DigestMD)
	b.WriteByte('\n')
	b.WriteString(res.Post)
	b.WriteByte('\n')
	b.WriteString(res.EmailText)
	for _, item := range res.Model.Items {
		b.WriteByte('\n')
		b.WriteString(item.Title)
		b.WriteByte('\n')
		b.WriteString(item.Essence)
		b.WriteByte('\n')
		b.WriteString(item.Consequences)
	}
	return b.String()
}

func scoreText(text string, forbidden, required []string) (hits, missing []string) {
	for _, marker := range forbidden {
		if marker != "" && strings.Contains(text, marker) {
			hits = append(hits, marker)
		}
	}
	for _, marker := range required {
		if marker != "" && !strings.Contains(text, marker) {
			missing = append(missing, marker)
		}
	}
	return hits, missing
}

func summarizeEval(rep EvalReport) string {
	var parts []string
	for _, c := range rep.Cases {
		parts = append(parts, fmt.Sprintf(
			"%s: расхождений без критика %d, с критиком %d (дельта %d)",
			c.Name, c.Without.Discrepancies, c.With.Discrepancies, c.Delta.Discrepancies,
		))
	}
	return strings.Join(parts, "; ")
}

// WriteReport writes JSON and Markdown next to each other.
func WriteReport(rep EvalReport, jsonPath, mdPath string) error {
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(mdPath, []byte(FormatMarkdown(rep)), 0o644)
}

// FormatMarkdown builds the human-readable eval report.
func FormatMarkdown(rep EvalReport) string {
	var b strings.Builder
	b.WriteString("# Замер эффекта ИИ-критика\n\n")
	b.WriteString("- Сгенерировано: " + rep.GeneratedAt + "\n")
	if rep.Mock {
		b.WriteString("- Режим: mock (без сети)\n\n")
	} else {
		b.WriteString("- Режим: живая модель\n\n")
	}
	b.WriteString(rep.Summary + "\n\n")
	for _, c := range rep.Cases {
		b.WriteString("## " + string(c.Name) + ": " + c.Title + "\n\n")
		b.WriteString("| Режим | Расхождения | Время, мс | Входные токены | Выходные токены | Вызовы | Вызовы критика | Ретраи | Деградация |\n")
		b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
		writeModeRow(&b, c.Without)
		writeModeRow(&b, c.With)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf(
			"Дельта (с критиком - без): расхождения %d, время %d мс, токены %+d/%+d.\n\n",
			c.Delta.Discrepancies, c.Delta.DurationMS, c.Delta.InputTokens, c.Delta.OutputTokens,
		))
		if len(c.Without.ForbiddenHits) > 0 {
			b.WriteString("Без критика остались подсаженные маркеры: " + strings.Join(c.Without.ForbiddenHits, ", ") + ".\n\n")
		}
		if len(c.With.ForbiddenHits) > 0 {
			b.WriteString("С критиком всё ещё видны маркеры: " + strings.Join(c.With.ForbiddenHits, ", ") + ".\n\n")
		} else if c.Name == CasePlanted {
			b.WriteString("С критиком подсаженные маркеры из эталона не найдены.\n\n")
		}
	}
	return b.String()
}

func writeModeRow(b *strings.Builder, m ModeResult) {
	deg := "нет"
	if m.Degraded {
		deg = "да"
	}
	label := "без критика"
	if m.Mode == ModeWith {
		label = "с критиком"
	}
	fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %d | %d | %d | %s |\n",
		label, m.Discrepancies, m.DurationMS, m.InputTokens, m.OutputTokens,
		m.CallsTotal, m.CriticCalls, m.Retries, deg)
}

func cloneDigest(m domain.DigestModel) domain.DigestModel {
	out := m
	if m.Items != nil {
		out.Items = append([]domain.DigestItem(nil), m.Items...)
		for i := range out.Items {
			if m.Items[i].Sources != nil {
				out.Items[i].Sources = append([]domain.SourceRef(nil), m.Items[i].Sources...)
			}
		}
	}
	if m.NotIncluded != nil {
		out.NotIncluded = append([]domain.NotIncluded(nil), m.NotIncluded...)
	}
	return out
}

func cloneEvents(events []domain.Event) []domain.Event {
	if events == nil {
		return nil
	}
	out := append([]domain.Event(nil), events...)
	for i := range out {
		if events[i].ArticleIDs != nil {
			out[i].ArticleIDs = append([]string(nil), events[i].ArticleIDs...)
		}
		if events[i].Mentions != nil {
			out[i].Mentions = append([]domain.Mention(nil), events[i].Mentions...)
		}
	}
	return out
}

func cloneArts(arts map[string]domain.Article) map[string]domain.Article {
	out := make(map[string]domain.Article, len(arts))
	for k, v := range arts {
		out[k] = v
	}
	return out
}
