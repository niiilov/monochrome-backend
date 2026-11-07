package render

import (
	"strings"
	"unicode/utf8"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/summarize"
	"cyberdigest/internal/textutil"
	"cyberdigest/internal/timeutil"
)

const (
	// DigestLimit is the digest cap in runes after URLs are removed.
	DigestLimit = 4000
	// PostLimit is the post cap in runes after URLs are removed.
	PostLimit = 2000

	emptyEvents   = "Подходящих событий за период нет"
	noRationale   = "Обоснование отбора в данных события не приведено."
	noEssence     = "В источнике недостаточно сведений для пересказа."
	noTitle       = "Событие"
	noSources     = "источники не указаны"
	unknownName   = "источник не указан"
	unknownDate   = "дата не указана"
	unknownID     = "нет"
	fewerThanFive = "Подходящих событий меньше пяти."
)

func periodPhrase(p domain.Period) string {
	return timeutil.FormatPeriod(p)
}

func controlPhrase(m domain.DigestModel) string {
	return timeutil.FormatMSK(m.ControlTime) + " МСК"
}

func cleanLine(s string) string {
	s = textutil.ReplaceLongDash(s)
	return strings.Join(strings.Fields(s), " ")
}

func finishSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	if r == '.' || r == '!' || r == '?' {
		return s
	}
	return s + "."
}

func stripEmptyLead(shortfall string) string {
	s := strings.TrimSpace(shortfall)
	lower := strings.ToLower(s)
	lead := strings.ToLower(emptyEvents)
	if strings.HasPrefix(lower, lead) {
		s = strings.TrimSpace(s[len(emptyEvents):])
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "."))
	return strings.TrimSpace(s)
}

func emptyMessage(shortfall string) string {
	reason := stripEmptyLead(shortfall)
	if reason == "" {
		return emptyEvents + "."
	}
	return emptyEvents + ". " + finishSentence(reason)
}

func fallback(s, repl string) string {
	if strings.TrimSpace(s) == "" {
		return repl
	}
	return s
}

func cloneModel(m domain.DigestModel) domain.DigestModel {
	if m.Items != nil {
		items := make([]domain.DigestItem, len(m.Items))
		for i, item := range m.Items {
			items[i] = item
			if item.Sources != nil {
				items[i].Sources = append([]domain.SourceRef(nil), item.Sources...)
			}
		}
		m.Items = items
	}
	if m.NotIncluded != nil {
		m.NotIncluded = append([]domain.NotIncluded(nil), m.NotIncluded...)
	}
	return m
}

func orderSources(in []domain.SourceRef) []domain.SourceRef {
	if len(in) < 2 {
		return in
	}
	primary := make([]domain.SourceRef, 0, len(in))
	rest := make([]domain.SourceRef, 0, len(in))
	for _, source := range in {
		if source.Role == summarize.RolePrimary {
			primary = append(primary, source)
		} else {
			rest = append(rest, source)
		}
	}
	return append(primary, rest...)
}

func prepare(m domain.DigestModel) domain.DigestModel {
	m = cloneModel(m)
	m.Shortfall = cleanLine(m.Shortfall)
	if m.Empty {
		m.Items = nil
	}
	if len(m.Items) > 5 {
		m.Items = m.Items[:5]
	}
	for i := range m.Items {
		item := &m.Items[i]
		item.N = i + 1
		item.Title = fallback(cleanLine(item.Title), noTitle)
		item.Essence = fallback(cleanLine(item.Essence), noEssence)
		item.Consequences = summarize.NormalizeConsequences(item.Consequences)
		item.Rationale = fallback(cleanLine(item.Rationale), noRationale)
		item.Sources = orderSources(item.Sources)
		for j := range item.Sources {
			item.Sources[j].Name = cleanLine(item.Sources[j].Name)
			item.Sources[j].Role = cleanLine(item.Sources[j].Role)
			item.Sources[j].Date = cleanLine(item.Sources[j].Date)
			item.Sources[j].URL = strings.TrimSpace(item.Sources[j].URL)
			item.Sources[j].ArticleID = strings.TrimSpace(item.Sources[j].ArticleID)
		}
	}
	fitDigest(&m)
	return m
}
