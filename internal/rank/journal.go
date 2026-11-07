package rank

import (
	"math"
	"strconv"
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/journal"
)

// ApplyJournal writes the final inclusion reason for events in Select order.
// The chosen primary article stays «включён» with the rationale. Other articles
// of a chosen event stay «объединён». Every article that belongs only to an
// event outside the limit becomes «исключён» with lower_priority, the place
// in the sorted list, and the score. Group is the event that claimed the
// article. Further events of the same article are appended to the reason.
// failures, keyed by event ID, fills an empty Error column and is traced
// when the row already has an error. Context articles are left untouched.
func ApplyJournal(j *journal.Journal, events []domain.Event, failures map[string]string) {
	if j == nil || len(events) == 0 {
		return
	}
	byArticle := eventIDsByArticle(events)
	claimed := map[string]bool{}
	for _, ev := range events {
		if !ev.Selected {
			continue
		}
		claim(j, ev, memberIDs(ev), claimed, byArticle, failures[eventKey(ev.ID)], true, 0, len(events))
	}
	for i, ev := range events {
		if ev.Selected {
			continue
		}
		claim(j, ev, memberIDs(ev), claimed, byArticle, failures[eventKey(ev.ID)], false, i+1, len(events))
	}
}

func claim(j *journal.Journal, ev domain.Event, ids []string, claimed map[string]bool, byArticle map[string][]string, failure string, selected bool, place, total int) {
	primary := strings.TrimSpace(ev.PrimaryArticleID)
	if primary == "" && len(ids) > 0 {
		primary = ids[0]
	}
	for _, id := range ids {
		if claimed[id] {
			continue
		}
		claimed[id] = true
		status := domain.StatusMerged
		code := domain.ReasonMerged
		data := journal.Data{EventID: ev.ID}
		if !selected {
			status = domain.StatusExcluded
			code = domain.ReasonLowerPriority
			data = journal.Data{Place: place, Total: total, Why: scoreWhy(ev)}
		} else if id == primary {
			status = domain.StatusIncluded
			code = domain.ReasonSelected
			data = journal.Data{Rationale: ev.Rationale}
		}
		prev, _ := j.Get(id)
		errText := prev.Error
		if strings.TrimSpace(errText) == "" {
			errText = failure
		} else if strings.TrimSpace(failure) != "" {
			j.Note(id, stageRank, failure)
		}
		reason := appendAlso(journal.Text(code, data), others(byArticle[id], ev.ID))
		j.Set(id, status, ev.ID, code, reason, errText)
		if DataUnknown(ev.Scores) {
			j.Note(id, stageRank, UnknownDataNote)
		}
	}
}

func eventIDsByArticle(events []domain.Event) map[string][]string {
	out := map[string][]string{}
	for _, ev := range events {
		for _, id := range memberIDs(ev) {
			if hasID(out[id], ev.ID) {
				continue
			}
			out[id] = append(out[id], ev.ID)
		}
	}
	return out
}

func others(ids []string, group string) []string {
	if len(ids) < 2 {
		return nil
	}
	var rest []string
	for _, id := range ids {
		if id == group || hasID(rest, id) {
			continue
		}
		rest = append(rest, id)
	}
	return rest
}

func hasID(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func appendAlso(reason string, rest []string) string {
	if len(rest) == 0 {
		return reason
	}
	if !strings.HasSuffix(reason, ".") {
		reason += "."
	}
	return reason + " Также события: " + strings.Join(rest, ", ") + "."
}

func scoreWhy(ev domain.Event) string {
	why := "балл " + formatScore(ev.Total)
	if DataUnknown(ev.Scores) {
		why += ", " + UnknownDataNote
	}
	return why
}

func formatScore(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0.00"
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func memberIDs(ev domain.Event) []string {
	return uniqueIDs(ev.ArticleIDs, ev.PrimaryArticleID)
}

func noteIDs(ev domain.Event) []string {
	ids := memberIDs(ev)
	if len(ids) > 0 {
		return ids
	}
	return uniqueIDs(ev.ContextArticleIDs)
}

func uniqueIDs(ids []string, extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range ids {
		add(id)
	}
	for _, id := range extra {
		add(id)
	}
	return out
}
