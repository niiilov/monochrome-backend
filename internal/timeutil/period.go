package timeutil

import (
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
)

// NewPeriod is the half-open window (control-24h, control] in MSK.
// The width is 24 hours, as fixed by the stage contract. config.Period.Hours is not read here.
func NewPeriod(control time.Time) domain.Period {
	end := control.In(MSK)
	return domain.Period{Start: end.Add(-24 * time.Hour), End: end}
}

// FormatMSK renders t as ДД.ММ.ГГГГ ЧЧ:ММ in Moscow.
func FormatMSK(t time.Time) string {
	return t.In(MSK).Format("02.01.2006 15:04")
}

// FormatPeriod renders the window as "ДД.ММ.ГГГГ ЧЧ:ММ - ДД.ММ.ГГГГ ЧЧ:ММ МСК".
func FormatPeriod(p domain.Period) string {
	return FormatMSK(p.Start) + " - " + FormatMSK(p.End) + " МСК"
}

// FilterPeriod splits articles into three lists, preserving input order inside each list.
// in is inside (start, end], out is a confirmed time outside the window (context for update checks),
// unconfirmed has no usable timestamp. Returned copies carry PublishedAt in MSK when the date is confirmed.
// notes maps an article ID to a Russian trace line for stage "period" (empty zone, date-only, unreadable).
// The input slice is not modified. An empty naive policy means assume_msk.
func FilterPeriod(arts []domain.Article, period domain.Period, cfg config.Period) (in, out, unconfirmed []domain.Article, notes map[string]string) {
	for _, article := range arts {
		next, when, note := resolve(article, cfg.NaiveTZPolicy)
		if note != "" && next.ID != "" {
			if notes == nil {
				notes = make(map[string]string)
			}
			notes[next.ID] = note
		}
		if when == nil {
			unconfirmed = append(unconfirmed, next)
			continue
		}
		if period.Contains(*when) {
			in = append(in, next)
			continue
		}
		out = append(out, next)
	}
	return in, out, unconfirmed, notes
}

func resolve(article domain.Article, policy string) (domain.Article, *time.Time, string) {
	next := cloneArticle(article)
	if article.PublishedAt != nil {
		when := article.PublishedAt.In(MSK)
		next.PublishedAt = &when
		return next, next.PublishedAt, ""
	}
	when, note := ParseDate(article.PublishedRaw, policy)
	if when != nil {
		next.PublishedAt = when
	}
	return next, when, note
}

func cloneArticle(article domain.Article) domain.Article {
	out := article
	if article.PublishedAt != nil {
		t := article.PublishedAt.In(MSK)
		out.PublishedAt = &t
	}
	if article.MissingFields != nil {
		out.MissingFields = append([]string(nil), article.MissingFields...)
	}
	if article.Flags != nil {
		out.Flags = append([]string(nil), article.Flags...)
	}
	return out
}
