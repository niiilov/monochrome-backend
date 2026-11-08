package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"mono/internal/config"
	"mono/internal/domain"
	"mono/internal/extract"
	"mono/internal/guard"
	"mono/internal/llm"
	"mono/internal/normalize"
	"mono/internal/prompts"
	"mono/internal/textutil"
)

const (
	taskConfirm = "cluster_confirm"
	taskUpdate  = "update_check"
	stage       = "cluster"
	reserve     = 2000
	maxTokens   = 800
	updateMax   = 600
	lineLimit   = 500
)

var confirmSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["same_event", "shared_entities", "reason", "conflicts"],
  "properties": {
    "same_event": {"type": "boolean"},
    "shared_entities": {"type": "array", "items": {"type": "string"}},
    "reason": {"type": "string"},
    "conflicts": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["field", "description", "article_ids"],
        "properties": {
          "field": {"type": "string"},
          "description": {"type": "string"},
          "article_ids": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}`)

var updateSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["new_facts"],
  "properties": {
    "new_facts": {"type": "array", "items": {"type": "string"}}
  }
}`)

type confirmVerdict struct {
	SameEvent      bool              `json:"same_event"`
	SharedEntities []string          `json:"shared_entities"`
	Reason         string            `json:"reason"`
	Conflicts      []domain.Conflict `json:"conflicts"`
}

type updateVerdict struct {
	NewFacts []string `json:"new_facts"`
}

type side struct {
	MentionID string
	ArticleID string
	Source    string
	Entities  string
	Facts     string
	Article   string
}

type pairPrompt struct {
	Left  side
	Right side
}

type updatePrompt struct {
	FreshID string
	Earlier string
	Fresh   string
}

// Result is the clustering outcome the journal stage reads.
// Stale lists in-period articles excluded as repeats without new facts.
// Insufficient lists articles that did not form an event because the text was too thin
// or every quote was dropped. Errors keeps a per-article failure that did not stop the run.
type Result struct {
	Events       []domain.Event
	Insufficient []string
	Stale        []string
	Errors       map[string]string
}

type candidate struct {
	i, j  int
	score float64
}

type link struct {
	i, j      int
	conflicts []domain.Conflict
}

type uf struct {
	parent []int
}

// Build groups mentions into events. See BuildResult for journal fields.
func Build(ctx context.Context, c llm.Client, mentions []domain.Mention, arts map[string]domain.Article, ctxArts []domain.Article, cfg config.Cluster) ([]domain.Event, error) {
	res, err := BuildResult(ctx, c, mentions, arts, ctxArts, cfg)
	if err != nil {
		return nil, err
	}
	return res.Events, nil
}

// BuildResult clusters mentions, checks updates against ctxArts, and reports
// which articles are stale or too short to retell.
func BuildResult(ctx context.Context, c llm.Client, mentions []domain.Mention, arts map[string]domain.Article, ctxArts []domain.Article, cfg config.Cluster) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = normCfg(cfg)
	arts = cloneArts(arts)
	ctxIDs := map[string]bool{}
	for _, a := range ctxArts {
		if a.ID != "" {
			ctxIDs[a.ID] = true
		}
	}

	var kept []domain.Mention
	insuff := map[string]struct{}{}
	for _, m := range mentions {
		id := strings.TrimSpace(m.ArticleID)
		if id == "" || ctxIDs[id] {
			continue
		}
		m.ArticleID = id
		if !m.SufficientText {
			insuff[id] = struct{}{}
			continue
		}
		kept = append(kept, cloneMention(m))
	}
	for _, m := range kept {
		delete(insuff, m.ArticleID)
	}

	res := Result{Errors: map[string]string{}}
	if len(kept) == 0 {
		res.Insufficient = mapKeys(insuff)
		res.Errors = nil
		return res, nil
	}

	cands := candidates(kept, cfg)
	var links []link
	groups := newUF(len(kept))
	if len(cands) > 0 && c == nil {
		return Result{}, errors.New("cluster: клиент не задан")
	}
	for _, pair := range cands {
		same, conflicts, reason, err := confirmPair(ctx, c, kept[pair.i], kept[pair.j], arts)
		left := kept[pair.i]
		right := kept[pair.j]
		if err != nil {
			msg := clipLine(err.Error())
			remember(res.Errors, left.ArticleID, msg)
			remember(res.Errors, right.ArticleID, msg)
			extract.Note(ctx, stage, left.ArticleID, "Не удалось подтвердить пару "+left.ID+" и "+right.ID+": "+msg)
			extract.Note(ctx, stage, right.ArticleID, "Не удалось подтвердить пару "+left.ID+" и "+right.ID+": "+msg)
			continue
		}
		if !same {
			note := "Пара " + left.ID + " и " + right.ID + " не объединена."
			if reason != "" {
				note = "Пара " + left.ID + " и " + right.ID + " не объединена: " + reason
			}
			extract.Note(ctx, stage, left.ArticleID, note)
			extract.Note(ctx, stage, right.ArticleID, note)
			continue
		}
		groups.union(pair.i, pair.j)
		links = append(links, link{i: pair.i, j: pair.j, conflicts: conflicts})
		note := "Пара " + left.ID + " и " + right.ID + " объединена."
		if reason != "" {
			note = "Пара " + left.ID + " и " + right.ID + " объединена: " + reason
		}
		extract.Note(ctx, stage, left.ArticleID, note)
		extract.Note(ctx, stage, right.ArticleID, note)
	}

	buckets := map[int][]int{}
	var roots []int
	for i := range kept {
		root := groups.find(i)
		if _, ok := buckets[root]; !ok {
			roots = append(roots, root)
		}
		buckets[root] = append(buckets[root], i)
	}
	sort.Ints(roots)

	events := make([]domain.Event, 0, len(roots))
	for _, root := range roots {
		events = append(events, assemble(buckets[root], kept, arts, links))
	}

	events, stale, err := applyUpdates(ctx, c, events, arts, ctxArts, cfg, res.Errors)
	if err != nil {
		return Result{}, err
	}
	events = assignIDs(events, arts)
	if len(res.Errors) == 0 {
		res.Errors = nil
	}
	res.Events = events
	res.Insufficient = mapKeys(insuff)
	res.Stale = stale
	return res, nil
}

func candidates(mentions []domain.Mention, cfg config.Cluster) []candidate {
	bags := make([]bag, len(mentions))
	for i, m := range mentions {
		bags[i] = mentionBag(m)
	}
	var all []candidate
	for i := 0; i < len(mentions); i++ {
		for j := i + 1; j < len(mentions); j++ {
			if mentions[i].ArticleID == mentions[j].ArticleID {
				continue
			}
			score := pairScore(bags[i], bags[j])
			if score <= cfg.SimilarityThreshold {
				continue
			}
			all = append(all, candidate{i: i, j: j, score: score})
		}
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].score != all[b].score {
			return all[a].score > all[b].score
		}
		ai, aj := mentions[all[a].i].ID, mentions[all[a].j].ID
		bi, bj := mentions[all[b].i].ID, mentions[all[b].j].ID
		if ai != bi {
			return ai < bi
		}
		if aj != bj {
			return aj < bj
		}
		if all[a].i != all[b].i {
			return all[a].i < all[b].i
		}
		return all[a].j < all[b].j
	})
	used := map[string]int{}
	picked := make([]candidate, 0, len(all))
	for _, c := range all {
		left := mentions[c.i].ArticleID
		right := mentions[c.j].ArticleID
		if used[left] >= cfg.MaxPairsPerArticle || used[right] >= cfg.MaxPairsPerArticle {
			continue
		}
		used[left]++
		used[right]++
		picked = append(picked, c)
	}
	return picked
}

func confirmPair(ctx context.Context, c llm.Client, left, right domain.Mention, arts map[string]domain.Article) (bool, []domain.Conflict, string, error) {
	if c == nil {
		return false, nil, "", errors.New("cluster: клиент не задан")
	}
	system, user, _, err := prompts.Render("cluster_confirm", pairPrompt{
		Left:  sideOf(left, arts),
		Right: sideOf(right, arts),
	})
	if err != nil {
		return false, nil, "", err
	}
	var verdict confirmVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskConfirm,
		System:      system,
		User:        user,
		Schema:      confirmSchema,
		Temperature: 0,
		MaxTokens:   maxTokens,
	}, &verdict)
	if err != nil {
		return false, nil, "", err
	}
	reason := clipLine(verdict.Reason)
	if !verdict.SameEvent {
		return false, nil, reason, nil
	}
	conflicts := cleanConflicts(verdict.Conflicts, left.ArticleID, right.ArticleID)
	conflicts = append(conflicts, localConflicts(left, right)...)
	return true, dedupeConflicts(conflicts), reason, nil
}

func sideOf(m domain.Mention, arts map[string]domain.Article) side {
	a := arts[m.ArticleID]
	body := clipArticle(a.Text, textBudget()/2)
	if strings.TrimSpace(a.Text) == "" {
		body = clipArticle(corpusFallback(m), textBudget()/2)
	}
	return side{
		MentionID: m.ID,
		ArticleID: m.ArticleID,
		Source:    strings.TrimSpace(a.Source),
		Entities:  strings.Join(m.Entities, ", "),
		Facts:     formatFacts(m),
		Article:   guard.WrapUntrusted(m.ArticleID, a.Title, body),
	}
}

func corpusFallback(m domain.Mention) string {
	return mentionCorpus(m)
}

func formatFacts(m domain.Mention) string {
	if len(m.Facts) == 0 {
		return "нет"
	}
	var b strings.Builder
	for _, fact := range m.Facts {
		b.WriteString("- ")
		b.WriteString(fact.Text)
		if fact.Quote != "" {
			b.WriteString(" | цитата: ")
			b.WriteString(fact.Quote)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func assemble(idxs []int, mentions []domain.Mention, arts map[string]domain.Article, links []link) domain.Event {
	in := map[int]bool{}
	for _, i := range idxs {
		in[i] = true
	}
	ms := make([]domain.Mention, 0, len(idxs))
	for _, i := range idxs {
		ms = append(ms, mentions[i])
	}
	var conflicts []domain.Conflict
	for _, ln := range links {
		if in[ln.i] && in[ln.j] {
			conflicts = append(conflicts, ln.conflicts...)
		}
	}
	ev := domain.Event{Mentions: ms, Conflicts: dedupeConflicts(conflicts)}
	finalize(&ev, arts)
	return ev
}

func applyUpdates(ctx context.Context, c llm.Client, events []domain.Event, arts map[string]domain.Article, ctxArts []domain.Article, cfg config.Cluster, errs map[string]string) ([]domain.Event, []string, error) {
	if len(ctxArts) == 0 && !needsEarlier(events) {
		return events, nil, nil
	}
	before := map[string]bool{}
	for _, ev := range events {
		for _, m := range ev.Mentions {
			if m.ArticleID != "" {
				before[m.ArticleID] = true
			}
		}
	}
	out := make([]domain.Event, 0, len(events))
	for _, ev := range events {
		matched := onlyEarlier(matchContext(ev, ctxArts, cfg), ev, arts)
		ev.ContextArticleIDs = contextIDs(matched, arts)
		var drop map[string]bool
		if len(matched) > 0 {
			if c == nil {
				return nil, nil, errors.New("cluster: клиент не задан")
			}
			drop = checkFresh(ctx, c, &ev, matched, arts, errs)
		} else {
			drop = earlierWithoutContext(ev)
		}
		ev.Mentions = filterMentions(ev.Mentions, drop)
		if len(ev.Mentions) == 0 {
			continue
		}
		finalize(&ev, arts)
		out = append(out, ev)
	}
	after := map[string]bool{}
	for _, ev := range out {
		for _, id := range ev.ArticleIDs {
			after[id] = true
		}
	}
	var stale []string
	for id := range before {
		if !after[id] {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	return out, stale, nil
}

func needsEarlier(events []domain.Event) bool {
	for _, ev := range events {
		for _, m := range ev.Mentions {
			if m.ReferencesEarlier {
				return true
			}
		}
	}
	return false
}

func matchContext(ev domain.Event, ctxArts []domain.Article, cfg config.Cluster) []domain.Article {
	if len(ctxArts) == 0 || len(ev.Mentions) == 0 {
		return nil
	}
	parts := make([]bag, len(ev.Mentions))
	for i, m := range ev.Mentions {
		parts[i] = mentionBag(m)
	}
	eventBag := mergeBags(parts)
	type hit struct {
		a     domain.Article
		score float64
	}
	var hits []hit
	for _, a := range ctxArts {
		if strings.TrimSpace(a.ID) == "" {
			continue
		}
		score := pairScore(eventBag, contextBag(a, eventBag.entities))
		if score <= cfg.SimilarityThreshold {
			continue
		}
		hits = append(hits, hit{a: a, score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].a.ID < hits[j].a.ID
	})
	if len(hits) > cfg.MaxPairsPerArticle {
		hits = hits[:cfg.MaxPairsPerArticle]
	}
	out := make([]domain.Article, len(hits))
	for i, h := range hits {
		out[i] = h.a
	}
	return out
}

func contextIDs(matched []domain.Article, arts map[string]domain.Article) []string {
	ids := make([]string, 0, len(matched))
	seen := map[string]bool{}
	for _, a := range matched {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		ids = append(ids, a.ID)
		if _, ok := arts[a.ID]; !ok {
			arts[a.ID] = a
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return articleLess(arts[ids[i]], arts[ids[j]])
	})
	return ids
}

func checkFresh(ctx context.Context, c llm.Client, ev *domain.Event, matched []domain.Article, arts map[string]domain.Article, errs map[string]string) map[string]bool {
	drop := map[string]bool{}
	seen := map[string]bool{}
	for i := range ev.Mentions {
		id := ev.Mentions[i].ArticleID
		if seen[id] {
			continue
		}
		seen[id] = true
		facts, err := updateOne(ctx, c, id, arts[id], matched)
		if err != nil {
			msg := clipLine(err.Error())
			remember(errs, id, msg)
			extract.Note(ctx, stage, id, "Проверка новых фактов не выполнена: "+msg)
			continue
		}
		if len(facts) == 0 {
			for j := range ev.Mentions {
				if ev.Mentions[j].ArticleID == id {
					drop[ev.Mentions[j].ID] = true
				}
			}
			extract.Note(ctx, stage, id, "Свежая публикация не содержит новых фактов относительно ранней.")
			continue
		}
		for j := range ev.Mentions {
			if ev.Mentions[j].ArticleID == id {
				ev.Mentions[j].NewFacts = facts
			}
		}
	}
	return drop
}

func updateOne(ctx context.Context, c llm.Client, freshID string, fresh domain.Article, earlier []domain.Article) ([]string, error) {
	var blocks []string
	budget := textBudget()
	each := budget / (len(earlier) + 1)
	if each < 500 {
		each = 500
	}
	for _, a := range earlier {
		body := clipArticle(a.Text, each)
		blocks = append(blocks, guard.WrapUntrusted(a.ID, a.Title, body))
	}
	freshBody := clipArticle(fresh.Text, each)
	if strings.TrimSpace(fresh.Text) == "" && strings.TrimSpace(fresh.Title) != "" {
		freshBody = fresh.Title
	}
	system, user, _, err := prompts.Render("update_check", updatePrompt{
		FreshID: freshID,
		Earlier: strings.Join(blocks, "\n\n"),
		Fresh:   guard.WrapUntrusted(freshID, fresh.Title, freshBody),
	})
	if err != nil {
		return nil, err
	}
	var verdict updateVerdict
	_, err = llm.CompleteJSON(ctx, c, llm.Request{
		Task:        taskUpdate,
		System:      system,
		User:        user,
		Schema:      updateSchema,
		Temperature: 0,
		MaxTokens:   updateMax,
	}, &verdict)
	if err != nil {
		return nil, err
	}
	return cleanList(verdict.NewFacts), nil
}

// onlyEarlier drops context articles that are not older than the event.
// A note published after the control time is out of period, but it is not an earlier report.
func onlyEarlier(matched []domain.Article, ev domain.Event, arts map[string]domain.Article) []domain.Article {
	when := earliestPublication(ev, arts)
	if when.IsZero() {
		return matched
	}
	out := make([]domain.Article, 0, len(matched))
	for _, a := range matched {
		if a.PublishedAt == nil || !a.PublishedAt.Before(when) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func earliestPublication(ev domain.Event, arts map[string]domain.Article) time.Time {
	var best time.Time
	for _, m := range ev.Mentions {
		a, ok := arts[m.ArticleID]
		if !ok || a.PublishedAt == nil {
			continue
		}
		if best.IsZero() || a.PublishedAt.Before(best) {
			best = *a.PublishedAt
		}
	}
	return best
}

// earlierWithoutContext drops a repeat that points at an event outside this cluster.
// A reprint that sits next to the original report stays, so its link remains on the event.
func earlierWithoutContext(ev domain.Event) map[string]bool {
	hasOriginal := false
	for _, m := range ev.Mentions {
		if !m.ReferencesEarlier {
			hasOriginal = true
			break
		}
	}
	drop := map[string]bool{}
	if hasOriginal {
		return drop
	}
	for _, m := range ev.Mentions {
		if m.ReferencesEarlier && len(m.NewFacts) == 0 {
			drop[m.ID] = true
		}
	}
	return drop
}

func filterMentions(ms []domain.Mention, drop map[string]bool) []domain.Mention {
	if len(drop) == 0 {
		return ms
	}
	out := make([]domain.Mention, 0, len(ms))
	for _, m := range ms {
		if drop[m.ID] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func assignIDs(events []domain.Event, arts map[string]domain.Article) []domain.Event {
	sort.Slice(events, func(i, j int) bool {
		return eventLess(events[i], events[j], arts)
	})
	for i := range events {
		events[i].ID = fmt.Sprintf("E%02d", i+1)
	}
	return events
}

func eventLess(a, b domain.Event, arts map[string]domain.Article) bool {
	ak := eventKey(a, arts)
	bk := eventKey(b, arts)
	if ak.missing != bk.missing {
		return !ak.missing
	}
	if !ak.when.Equal(bk.when) {
		return ak.when.Before(bk.when)
	}
	if ak.articleID != bk.articleID {
		return ak.articleID < bk.articleID
	}
	return ak.mentionID < bk.mentionID
}

type sortKey struct {
	missing   bool
	when      time.Time
	articleID string
	mentionID string
}

func eventKey(ev domain.Event, arts map[string]domain.Article) sortKey {
	key := sortKey{missing: true, articleID: "~", mentionID: "~"}
	for _, m := range ev.Mentions {
		a := arts[m.ArticleID]
		cand := sortKey{missing: a.PublishedAt == nil, articleID: a.ID, mentionID: m.ID}
		if a.PublishedAt != nil {
			cand.when = a.PublishedAt.UTC()
		}
		if a.ID == "" {
			cand.articleID = m.ArticleID
		}
		if betterKey(cand, key) {
			key = cand
		}
	}
	return key
}

func betterKey(a, b sortKey) bool {
	if a.missing != b.missing {
		return !a.missing
	}
	if !a.missing && !a.when.Equal(b.when) {
		return a.when.Before(b.when)
	}
	if a.articleID != b.articleID {
		return a.articleID < b.articleID
	}
	return a.mentionID < b.mentionID
}

func finalize(ev *domain.Event, arts map[string]domain.Article) {
	if len(ev.Mentions) == 0 {
		ev.ArticleIDs = nil
		ev.PrimaryArticleID = ""
		ev.IndependentSources = 0
		return
	}
	primary := ev.Mentions[0]
	for _, m := range ev.Mentions[1:] {
		if betterPrimary(arts[m.ArticleID], m, arts[primary.ArticleID], primary) {
			primary = m
		}
	}
	ev.PrimaryArticleID = primary.ArticleID
	order := articleOrder(ev.Mentions, arts, ev.PrimaryArticleID)
	ev.ArticleIDs = order
	sort.SliceStable(ev.Mentions, func(i, j int) bool {
		return mentionLess(ev.Mentions[i], ev.Mentions[j], arts, ev.PrimaryArticleID)
	})
	ev.Conflicts = filterConflicts(dedupeConflicts(ev.Conflicts), order)
	ev.IndependentSources = countIndependent(ev.Mentions)
}

func betterPrimary(a domain.Article, am domain.Mention, b domain.Article, bm domain.Mention) bool {
	ta, tb := sourceTier(a.SourceKind), sourceTier(b.SourceKind)
	if ta != tb {
		return ta < tb
	}
	if a.PublishedAt != nil || b.PublishedAt != nil {
		if a.PublishedAt == nil {
			return false
		}
		if b.PublishedAt == nil {
			return true
		}
		if !a.PublishedAt.Equal(*b.PublishedAt) {
			return a.PublishedAt.Before(*b.PublishedAt)
		}
	}
	aid, bid := a.ID, b.ID
	if aid == "" {
		aid = am.ArticleID
	}
	if bid == "" {
		bid = bm.ArticleID
	}
	if aid != bid {
		return aid < bid
	}
	return am.ID < bm.ID
}

func sourceTier(k domain.SourceKind) int {
	switch k {
	case domain.SourceVendor, domain.SourceRegulator:
		return 0
	default:
		return 1
	}
}

func articleOrder(ms []domain.Mention, arts map[string]domain.Article, primary string) []string {
	ids := uniqueIDs(ms)
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == primary {
			return true
		}
		if ids[j] == primary {
			return false
		}
		return articleLess(arts[ids[i]], arts[ids[j]])
	})
	return ids
}

func uniqueIDs(ms []domain.Mention) []string {
	seen := map[string]bool{}
	var ids []string
	for _, m := range ms {
		if m.ArticleID == "" || seen[m.ArticleID] {
			continue
		}
		seen[m.ArticleID] = true
		ids = append(ids, m.ArticleID)
	}
	return ids
}

func articleLess(a, b domain.Article) bool {
	if a.PublishedAt != nil || b.PublishedAt != nil {
		if a.PublishedAt == nil {
			return false
		}
		if b.PublishedAt == nil {
			return true
		}
		if !a.PublishedAt.Equal(*b.PublishedAt) {
			return a.PublishedAt.Before(*b.PublishedAt)
		}
	}
	return a.ID < b.ID
}

func mentionLess(a, b domain.Mention, arts map[string]domain.Article, primary string) bool {
	if a.ArticleID != b.ArticleID {
		if a.ArticleID == primary {
			return true
		}
		if b.ArticleID == primary {
			return false
		}
		return articleLess(arts[a.ArticleID], arts[b.ArticleID])
	}
	return a.ID < b.ID
}

func countIndependent(ms []domain.Mention) int {
	seen := map[string]bool{}
	original := map[string]bool{}
	for _, m := range ms {
		if m.ArticleID == "" {
			continue
		}
		seen[m.ArticleID] = true
		if !m.IsReprint {
			original[m.ArticleID] = true
		}
	}
	n := 0
	for id := range seen {
		if original[id] {
			n++
		}
	}
	return n
}

func localConflicts(a, b domain.Mention) []domain.Conflict {
	var out []domain.Conflict
	an, bn := mentionBag(a).numbers, mentionBag(b).numbers
	if diverges(an, bn) {
		out = append(out, domain.Conflict{
			Field:       "число",
			Description: "Числа расходятся: " + a.ArticleID + " указывает " + sortedJoin(an) + "; " + b.ArticleID + " указывает " + sortedJoin(bn) + ".",
			ArticleIDs:  sortedIDs(a.ArticleID, b.ArticleID),
		})
	}
	ad, bd := mentionBag(a).dates, mentionBag(b).dates
	if diverges(ad, bd) {
		out = append(out, domain.Conflict{
			Field:       "дата",
			Description: "Даты расходятся: " + a.ArticleID + " указывает " + sortedJoin(ad) + "; " + b.ArticleID + " указывает " + sortedJoin(bd) + ".",
			ArticleIDs:  sortedIDs(a.ArticleID, b.ArticleID),
		})
	}
	return out
}

func cleanConflicts(in []domain.Conflict, idA, idB string) []domain.Conflict {
	allowed := map[string]bool{idA: true, idB: true}
	var out []domain.Conflict
	for _, c := range in {
		c.Field = clipLine(c.Field)
		c.Description = clipLine(c.Description)
		if c.Field == "" || c.Description == "" {
			continue
		}
		var ids []string
		seen := map[string]bool{}
		for _, id := range c.ArticleIDs {
			id = strings.TrimSpace(id)
			if !allowed[id] || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(ids) < 2 {
			ids = sortedIDs(idA, idB)
		} else {
			sort.Strings(ids)
		}
		c.ArticleIDs = ids
		out = append(out, c)
	}
	return out
}

func filterConflicts(in []domain.Conflict, articleIDs []string) []domain.Conflict {
	ok := map[string]bool{}
	for _, id := range articleIDs {
		ok[id] = true
	}
	var out []domain.Conflict
	for _, c := range in {
		keep := true
		for _, id := range c.ArticleIDs {
			if !ok[id] {
				keep = false
				break
			}
		}
		if keep && len(c.ArticleIDs) >= 2 {
			out = append(out, c)
		}
	}
	return out
}

func dedupeConflicts(in []domain.Conflict) []domain.Conflict {
	seen := map[string]bool{}
	var out []domain.Conflict
	for _, c := range in {
		ids := append([]string(nil), c.ArticleIDs...)
		sort.Strings(ids)
		key := c.Field + "|" + c.Description + "|" + strings.Join(ids, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		c.ArticleIDs = ids
		out = append(out, c)
	}
	return out
}

func sortedIDs(a, b string) []string {
	if a > b {
		a, b = b, a
	}
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

func cleanList(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		item = clipLine(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func clipLine(s string) string {
	s = textutil.ReplaceLongDash(s)
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= lineLimit {
		return s
	}
	return string([]rune(s)[:lineLimit])
}

func remember(dst map[string]string, id, msg string) {
	if id == "" || msg == "" {
		return
	}
	if _, ok := dst[id]; ok {
		return
	}
	dst[id] = msg
}

func mapKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func cloneArts(in map[string]domain.Article) map[string]domain.Article {
	out := make(map[string]domain.Article, len(in))
	for id, article := range in {
		out[id] = article
	}
	return out
}

func cloneMention(m domain.Mention) domain.Mention {
	out := m
	if m.Facts != nil {
		out.Facts = append([]domain.Fact(nil), m.Facts...)
	}
	if m.Hedges != nil {
		out.Hedges = append([]string(nil), m.Hedges...)
	}
	if m.Entities != nil {
		out.Entities = append([]string(nil), m.Entities...)
	}
	if m.NewFacts != nil {
		out.NewFacts = append([]string(nil), m.NewFacts...)
	}
	return out
}

func textBudget() int {
	limit := config.Default().LLM.MaxInputChars
	if limit <= reserve+500 {
		return limit
	}
	return limit - reserve
}

func clipArticle(text string, budget int) string {
	parts := normalize.Chunks(text, budget)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func normCfg(cfg config.Cluster) config.Cluster {
	if cfg == (config.Cluster{}) {
		return config.Default().Cluster
	}
	def := config.Default().Cluster
	if cfg.MaxPairsPerArticle <= 0 {
		cfg.MaxPairsPerArticle = def.MaxPairsPerArticle
	}
	if cfg.SimilarityThreshold < 0 || cfg.SimilarityThreshold > 1 {
		cfg.SimilarityThreshold = def.SimilarityThreshold
	}
	return cfg
}

func newUF(n int) *uf {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &uf{parent: p}
}

func (u *uf) find(i int) int {
	if u.parent[i] != i {
		u.parent[i] = u.find(u.parent[i])
	}
	return u.parent[i]
}

func (u *uf) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if ra > rb {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}
