package cluster

import (
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/journal"
)

// ApplyJournal writes cluster statuses for in-period articles and context articles.
// The representative of the earliest event is «включён», other members of a kept
// event are «объединён». A short text, a repeat without new facts, and anything
// outside the period are «исключён». Context always stays excluded, even when an
// event links it. S6 replaces the representative reason when it selects the digest.
func ApplyJournal(j *journal.Journal, res Result, period domain.Period, in, ctxArts []domain.Article) {
	if j == nil {
		return
	}
	primaryOf := map[string]string{}
	member := map[string][]string{}
	for _, ev := range res.Events {
		primaryOf[ev.ID] = ev.PrimaryArticleID
		seen := map[string]bool{}
		for _, id := range ev.ArticleIDs {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			member[id] = append(member[id], ev.ID)
		}
	}
	stale := toSet(res.Stale)
	ctxIDs := map[string]bool{}
	for _, a := range ctxArts {
		if a.ID != "" {
			ctxIDs[a.ID] = true
		}
	}
	for _, a := range in {
		if a.ID == "" || ctxIDs[a.ID] {
			continue
		}
		events := member[a.ID]
		errText := res.Errors[a.ID]
		if len(events) > 0 {
			first := events[0]
			status := domain.StatusMerged
			code := domain.ReasonMerged
			if primaryOf[first] == a.ID {
				status = domain.StatusIncluded
				code = domain.ReasonSelected
			}
			reason := journal.Text(code, journal.Data{EventID: first})
			reason = appendAlso(reason, events[1:])
			j.Set(a.ID, status, first, code, reason, errText)
			continue
		}
		if _, ok := stale[a.ID]; ok {
			j.Set(a.ID, domain.StatusExcluded, "", domain.ReasonStaleNoNewFacts, journal.Text(domain.ReasonStaleNoNewFacts, journal.Data{}), errText)
			continue
		}
		j.Set(a.ID, domain.StatusExcluded, "", domain.ReasonInsufficient, journal.Text(domain.ReasonInsufficient, journal.Data{}), errText)
	}
	for _, a := range ctxArts {
		if a.ID == "" {
			continue
		}
		j.Set(a.ID, domain.StatusExcluded, "", domain.ReasonOutOfPeriod, journal.Text(domain.ReasonOutOfPeriod, journal.Data{Period: period}), res.Errors[a.ID])
		var linked []string
		seen := map[string]bool{}
		for _, ev := range res.Events {
			for _, id := range ev.ContextArticleIDs {
				if id == a.ID && !seen[ev.ID] {
					seen[ev.ID] = true
					linked = append(linked, ev.ID)
				}
			}
		}
		if len(linked) > 0 {
			j.Note(a.ID, "cluster", "Более ранняя публикация для событий: "+strings.Join(linked, ", ")+".")
		}
	}
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
