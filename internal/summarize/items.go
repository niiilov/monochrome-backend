package summarize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/guard"
	"cyberdigest/internal/llm"
	"cyberdigest/internal/prompts"
)

const itemSchemaJSON = `{"type":"object","additionalProperties":false,"required":["title","essence","consequences","hedge_note"],"properties":{"title":{"type":"string"},"essence":{"type":"string"},"consequences":{"type":"string"},"hedge_note":{"type":"string"}}}`

type itemJSON struct {
	Title        string `json:"title"`
	Essence      string `json:"essence"`
	Consequences string `json:"consequences"`
	HedgeNote    string `json:"hedge_note"`
}

type itemView struct {
	EventID             string
	Facts               []factView
	Hedges              []string
	Conflicts           []conflictView
	Sources             []sourceView
	Affected            string
	MentionConsequences string
	CompanyStatement    bool
	RetryHints          []string
}

type factView struct {
	Block string
}

type conflictView struct {
	Field       string
	Description string
	ArticleIDs  string
}

type sourceView struct {
	ArticleID string
	Name      string
	Role      string
	Date      string
}

// Items writes one digest item per selected event.
// Calls run in parallel, at most four at a time.
// retryHints maps an event ID to notes that must be fixed on this attempt.
// When any event is marked selected, the others are skipped.
// A rank greater than zero sorts the kept events. At most five are kept.
// If the draft is over the digest limit, the long items are requested once more.
func Items(ctx context.Context, c llm.Client, events []domain.Event, arts map[string]domain.Article, retryHints map[string][]string) ([]domain.DigestItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	picked := pickEvents(events)
	if len(picked) == 0 {
		return nil, nil
	}
	if c == nil {
		return nil, errors.New("summarize: клиент LLM не задан")
	}
	items, err := writeAll(ctx, c, picked, arts, retryHints)
	if err != nil {
		return nil, err
	}
	if indexes := oversizeIndexes(items); len(indexes) > 0 {
		hints := map[string][]string{}
		var again []domain.Event
		for _, i := range indexes {
			id := picked[i].ID
			hints[id] = append(append([]string{}, retryHints[id]...), shrinkHint)
			again = append(again, picked[i])
		}
		rewritten, err := writeAll(ctx, c, again, arts, hints)
		if err != nil {
			return nil, err
		}
		for j, i := range indexes {
			items[i] = rewritten[j]
		}
	}
	for i := range items {
		items[i].N = i + 1
	}
	return items, nil
}

func writeAll(ctx context.Context, c llm.Client, events []domain.Event, arts map[string]domain.Article, retryHints map[string][]string) ([]domain.DigestItem, error) {
	items := make([]domain.DigestItem, len(events))
	err := parallel(ctx, len(events), func(ctx context.Context, i int) error {
		item, err := writeItem(ctx, c, events[i], arts, retryHints[events[i].ID])
		if err != nil {
			return err
		}
		items[i] = item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func writeItem(ctx context.Context, c llm.Client, event domain.Event, arts map[string]domain.Article, hints []string) (domain.DigestItem, error) {
	view := buildItemView(event, arts, hints)
	system, user, _, err := prompts.Render("write_digest_item", view)
	if err != nil {
		return domain.DigestItem{}, fmt.Errorf("summarize: промпт события %s: %w", event.ID, err)
	}
	var parsed itemJSON
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        "write_digest_item",
		System:      system,
		User:        user,
		Schema:      json.RawMessage(itemSchemaJSON),
		Temperature: 0,
		MaxTokens:   900,
	}, &parsed)
	if err != nil {
		return domain.DigestItem{}, fmt.Errorf("summarize: событие %s: %w", event.ID, err)
	}
	title := cleanProse(parsed.Title)
	essence := mergeHedge(parsed.Essence, parsed.HedgeNote)
	if title == "" || essence == "" {
		return domain.DigestItem{}, fmt.Errorf("summarize: событие %s: модель вернула пустой заголовок или пустую суть", event.ID)
	}
	rationale := cleanProse(event.Rationale)
	if rationale == "" {
		rationale = noRationale
	}
	return domain.DigestItem{
		EventID:      event.ID,
		Title:        title,
		Essence:      essence,
		Consequences: NormalizeConsequences(parsed.Consequences),
		Sources:      SourceRefs(event, arts),
		Rationale:    rationale,
	}, nil
}

func buildItemView(event domain.Event, arts map[string]domain.Article, hints []string) itemView {
	var facts []factView
	var hedges []string
	var affected []string
	var consequences []string
	company := false
	for _, mention := range event.Mentions {
		if mention.IsCompanyStatement {
			company = true
		}
		affected = append(affected, mention.Affected)
		consequences = append(consequences, mention.Consequences)
		hedges = append(hedges, mention.Hedges...)
		name := sourceName(lookupArticle(arts, mention.ArticleID))
		for _, fact := range mention.Facts {
			if block := factBlock(fact, name); block != "" {
				facts = append(facts, factView{Block: block})
			}
		}
	}
	if company {
		hedges = append(hedges, "компания заявила")
	}
	hedges = uniqueLines(hedges)
	var conflicts []conflictView
	for _, conflict := range event.Conflicts {
		conflicts = append(conflicts, conflictView{
			Field:       oneLine(conflict.Field),
			Description: oneLine(conflict.Description),
			ArticleIDs:  strings.Join(conflict.ArticleIDs, ", "),
		})
	}
	refs := SourceRefs(event, arts)
	sources := make([]sourceView, 0, len(refs))
	for _, ref := range refs {
		sources = append(sources, sourceView{
			ArticleID: ref.ArticleID,
			Name:      ref.Name,
			Role:      ref.Role,
			Date:      ref.Date,
		})
	}
	return itemView{
		EventID:             event.ID,
		Facts:               facts,
		Hedges:              hedges,
		Conflicts:           conflicts,
		Sources:             sources,
		Affected:            joinOrNone(affected),
		MentionConsequences: joinOrNone(consequences),
		CompanyStatement:    company,
		RetryHints:          uniqueLines(hints),
	}
}

func factBlock(fact domain.Fact, source string) string {
	var b strings.Builder
	if text := strings.TrimSpace(fact.Text); text != "" {
		b.WriteString(text)
	}
	if quote := strings.TrimSpace(fact.Quote); quote != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Цитата: ")
		b.WriteString(quote)
	}
	if b.Len() == 0 {
		return ""
	}
	return guard.WrapUntrusted(fact.ArticleID, source, b.String())
}

func joinOrNone(parts []string) string {
	parts = uniqueLines(parts)
	if len(parts) == 0 {
		return "нет"
	}
	return strings.Join(parts, "; ")
}

func pickEvents(events []domain.Event) []domain.Event {
	if len(events) == 0 {
		return nil
	}
	selected := make([]domain.Event, 0, len(events))
	anySelected := false
	for _, event := range events {
		if event.Selected {
			anySelected = true
			selected = append(selected, event)
		}
	}
	if !anySelected {
		selected = append(selected, events...)
	}
	ranked := false
	for _, event := range selected {
		if event.Rank > 0 {
			ranked = true
			break
		}
	}
	if ranked {
		sort.SliceStable(selected, func(i, j int) bool {
			ri, rj := selected[i].Rank, selected[j].Rank
			if ri == 0 {
				return false
			}
			if rj == 0 {
				return true
			}
			return ri < rj
		})
	}
	if len(selected) > maxEvents {
		selected = selected[:maxEvents]
	}
	return selected
}

func oversizeIndexes(items []domain.DigestItem) []int {
	if len(items) == 0 || digestWeight(items) <= digestCharLimit {
		return nil
	}
	fair := digestCharLimit / len(items)
	var indexes []int
	for i, item := range items {
		if proseWeight(item) > fair {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func proseWeight(item domain.DigestItem) int {
	return runeCount(item.Title) + runeCount(item.Essence) + runeCount(item.Consequences)
}

func digestWeight(items []domain.DigestItem) int {
	total := 220
	for _, item := range items {
		total += proseWeight(item) + runeCount(item.Rationale) + 70
		for _, source := range item.Sources {
			total += runeCount(source.Name) + runeCount(source.Role) + runeCount(source.Date) + runeCount(source.ArticleID) + 16
		}
	}
	return total
}
