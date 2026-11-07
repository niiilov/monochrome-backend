package verify

import (
	"context"
	"regexp"
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/guard"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/render"
	"cyberdigest/internal/summarize"
)

var hrefRE = regexp.MustCompile(`(?i)href\s*=\s*"([^"]*)"`)

// Options controls how many times Gate asks the model to rewrite a broken event.
// MaxRetries 0 degrades on the first failed check. The config default is 2.
type Options struct {
	MaxRetries int
}

// Result is a digest that passed the gate or was replaced with a safe retelling.
type Result struct {
	Model     domain.DigestModel
	DigestMD  string
	Post      string
	EmailText string
	EmailHTML string
	Findings  []Finding
	Report    domain.VerifyReport
}

// Gate checks the digest, rewrites only the broken events, then degrades.
// A degraded item is a template sentence from Fact.Text and does not call the model.
// An invented number, URL or quote is removed before the result is returned.
func Gate(ctx context.Context, c llm.Client, model domain.DigestModel, events []domain.Event, arts map[string]domain.Article, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	model = cloneModel(model)
	if arts == nil {
		arts = map[string]domain.Article{}
	}
	reg := NewRegistry(arts)
	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	forceTemplate := c == nil
	retries := 0
	degraded := false
	var last []Finding

	for {
		digest, post, email, html, err := publish(ctx, c, model, forceTemplate)
		if err != nil {
			return Result{}, err
		}
		findings := inspect(model, post, email, html, reg, arts, events)
		last = findings
		if !hasError(findings) {
			return pack(model, digest, post, email, html, findings, retries, degraded), nil
		}
		if retries >= maxRetries {
			break
		}
		retries++
		ids := eventErrorIDs(findings)
		if c != nil && len(ids) > 0 {
			if err := rewriteItems(ctx, c, &model, events, arts, ids, findings); err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				continue
			}
			continue
		}
		forceTemplate = true
	}

	var changed bool
	model, changed = degradeEvents(model, events, arts, eventErrorIDs(last), true)
	degraded = degraded || changed
	digest, post, email, html, err := publish(ctx, c, model, true)
	if err != nil {
		return Result{}, err
	}
	findings := inspect(model, post, email, html, reg, arts, events)
	if hasError(findings) {
		model, changed = degradeEvents(model, events, arts, eventErrorIDs(findings), false)
		if changed {
			degraded = true
			digest, post, email, html, err = publish(ctx, c, model, true)
			if err != nil {
				return Result{}, err
			}
			findings = inspect(model, post, email, html, reg, arts, events)
		}
	}
	return pack(model, digest, post, email, html, findings, retries, degraded), nil
}

func publish(ctx context.Context, c llm.Client, m domain.DigestModel, template bool) (digest, post, email, html string, err error) {
	if err = ctx.Err(); err != nil {
		return "", "", "", "", err
	}
	digest = render.Digest(m)
	email = render.EmailText(m)
	html = render.EmailHTML(m)
	shorts := templateShorts(m)
	if !template && c != nil && !m.Empty && len(m.Items) > 0 {
		built, callErr := summarize.ShortItems(ctx, c, m, render.PostLimit)
		if callErr != nil {
			if ctx.Err() != nil {
				return "", "", "", "", ctx.Err()
			}
		} else {
			shorts = built
		}
	}
	post = render.Post(m, shorts)
	return digest, post, email, html, nil
}

func inspect(m domain.DigestModel, post, email, html string, reg Registry, arts map[string]domain.Article, events []domain.Event) []Finding {
	findings := Check(m, post, email, reg, arts, events)
	return append(findings, injectionExtra(html, reg, findings)...)
}

func injectionExtra(html string, reg Registry, existing []Finding) []Finding {
	seen := map[string]bool{}
	for _, f := range existing {
		if f.Rule == RuleInjection {
			seen[f.Message] = true
		}
	}
	var out []Finding
	add := func(message string) {
		if message == "" || seen[message] {
			return
		}
		seen[message] = true
		out = append(out, finding(RuleInjection, SevError, "email", message))
	}
	// Tags are stripped first. A raw href="url">text run would otherwise
	// glue the address to the link text and look like a URL outside the registry.
	for _, violation := range guard.CheckOutput(visibleHTML(html), reg.AllowedURLs(), nil) {
		add(injectionMessage(violation))
	}
	for _, href := range hrefRE.FindAllStringSubmatch(html, -1) {
		if len(href) < 2 {
			continue
		}
		link := strings.TrimSpace(href[1])
		low := strings.ToLower(link)
		if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
			if !urlKnown(link, reg.AllowedURLs()) {
				add("в письме URL вне реестра: " + link)
			}
			continue
		}
		if link != "" && !strings.HasPrefix(link, "#") && !strings.HasPrefix(low, "mailto:") {
			add("в письме недопустимая ссылка: " + clipRunes(link, 80))
		}
	}
	return out
}

func visibleHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteByte(' ')
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func rewriteItems(ctx context.Context, c llm.Client, model *domain.DigestModel, events []domain.Event, arts map[string]domain.Article, ids []string, findings []Finding) error {
	byID := eventByID(events)
	hints := map[string][]string{}
	seen := map[string]bool{}
	var subset []domain.Event
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		event, ok := byID[id]
		if !ok {
			continue
		}
		seen[id] = true
		event.Selected = true
		subset = append(subset, event)
		for _, f := range findings {
			if f.Severity == SevError && f.EventID() == id && strings.TrimSpace(f.Message) != "" {
				hints[id] = append(hints[id], f.Message)
			}
		}
	}
	if len(subset) == 0 {
		return nil
	}
	items, err := summarize.Items(ctx, c, subset, arts, hints)
	if err != nil {
		return err
	}
	for _, item := range items {
		replaceItem(model, item)
	}
	return nil
}

func replaceItem(model *domain.DigestModel, next domain.DigestItem) {
	for i, item := range model.Items {
		if item.EventID != next.EventID {
			continue
		}
		n := item.N
		next.N = n
		if next.N <= 0 {
			next.N = i + 1
		}
		model.Items[i] = next
		return
	}
}

func eventErrorIDs(findings []Finding) []string {
	seen := map[string]bool{}
	var ids []string
	for _, f := range findings {
		if f.Severity != SevError {
			continue
		}
		id := f.EventID()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func pack(model domain.DigestModel, digest, post, email, html string, findings []Finding, retries int, degraded bool) Result {
	reportFindings := make([]domain.VerifyFinding, 0, len(findings))
	for _, f := range findings {
		reportFindings = append(reportFindings, f.Report())
	}
	if reportFindings == nil {
		reportFindings = []domain.VerifyFinding{}
	}
	return Result{
		Model:     model,
		DigestMD:  digest,
		Post:      post,
		EmailText: email,
		EmailHTML: html,
		Findings:  findings,
		Report: domain.VerifyReport{
			Retries:  retries,
			Findings: reportFindings,
			Degraded: degraded,
		},
	}
}

func cloneModel(m domain.DigestModel) domain.DigestModel {
	out := m
	if m.Items != nil {
		out.Items = make([]domain.DigestItem, len(m.Items))
		for i, item := range m.Items {
			out.Items[i] = item
			if item.Sources != nil {
				out.Items[i].Sources = append([]domain.SourceRef(nil), item.Sources...)
			}
		}
	}
	if m.NotIncluded != nil {
		out.NotIncluded = append([]domain.NotIncluded(nil), m.NotIncluded...)
	}
	return out
}
