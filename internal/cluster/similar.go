package cluster

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"mono/internal/domain"
	"mono/internal/textutil"
)

var (
	cveRE        = regexp.MustCompile(`(?i)\bCVE-\d{4}-\d{4,7}\b`)
	quotedNameRE = regexp.MustCompile(`«([^»]{3,80})»`)
)

// weakEntity is a folded token that is too common to glue two stories together.
var weakEntity = map[string]bool{
	"система": true, "системы": true, "данные": true, "клиенты": true,
	"клиентов": true, "компания": true, "компании": true, "интернет": true,
	"безопасность": true, "информация": true, "уязвимость": true,
	"обновление": true, "организация": true, "организации": true,
	"источник": true, "сообщение": true,
}

type bag struct {
	entities []string
	cves     []string
	numbers  []string
	dates    []string
	bigrams  []string
}

type part struct {
	w float64
	v float64
}

func mentionBag(m domain.Mention) bag {
	text := mentionCorpus(m)
	entities := normEntities(m.Entities)
	cves := findCVEs(text + "\n" + strings.Join(m.Entities, "\n"))
	entities = unique(append(entities, cves...))
	return bag{
		entities: entities,
		cves:     cves,
		numbers:  unique(textutil.ExtractNumbers(text)),
		dates:    unique(textutil.ExtractDates(text)),
		bigrams:  bigrams(tokens(text)),
	}
}

func mentionCorpus(m domain.Mention) string {
	var b strings.Builder
	writeLine(&b, m.TitleDraft)
	writeLine(&b, m.Affected)
	writeLine(&b, m.Consequences)
	for _, fact := range m.Facts {
		writeLine(&b, fact.Text)
		writeLine(&b, fact.Quote)
	}
	for _, item := range m.NewFacts {
		writeLine(&b, item)
	}
	for _, item := range m.Entities {
		writeLine(&b, item)
	}
	return b.String()
}

func writeLine(b *strings.Builder, s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	b.WriteString(s)
	b.WriteByte('\n')
}

// contextBag builds a bag for an out-of-period article.
// Entities are the event entities that actually occur in the article, plus CVEs.
func contextBag(a domain.Article, eventEntities []string) bag {
	text := strings.TrimSpace(a.Title + "\n" + a.Text)
	folded := foldKey(text)
	var entities []string
	for _, e := range eventEntities {
		key := foldKey(e)
		if len([]rune(key)) < 3 {
			continue
		}
		if strings.Contains(folded, key) {
			entities = append(entities, key)
		}
	}
	cves := findCVEs(text)
	entities = unique(append(entities, cves...))
	return bag{
		entities: entities,
		cves:     cves,
		numbers:  unique(textutil.ExtractNumbers(text)),
		dates:    unique(textutil.ExtractDates(text)),
		bigrams:  bigrams(tokens(text)),
	}
}

func mergeBags(parts []bag) bag {
	var out bag
	for _, p := range parts {
		out.entities = append(out.entities, p.entities...)
		out.cves = append(out.cves, p.cves...)
		out.numbers = append(out.numbers, p.numbers...)
		out.dates = append(out.dates, p.dates...)
		out.bigrams = append(out.bigrams, p.bigrams...)
	}
	out.entities = unique(out.entities)
	out.cves = unique(out.cves)
	out.numbers = unique(out.numbers)
	out.dates = unique(out.dates)
	out.bigrams = unique(out.bigrams)
	return out
}

// pairScore is in [0, 1]. Shared CVE is at least 0.8.
// Numbers and dates count only after a shared entity or CVE, so a lone figure
// does not glue unrelated stories together.
func pairScore(a, b bag) float64 {
	ev, hasEnt := overlap(a.entities, b.entities)
	cv, hasCVE := overlap(a.cves, b.cves)
	lex, hasLex := jaccard(a.bigrams, b.bigrams)
	sharedCVE := hasCVE && cv > 0
	sharedEnt := (hasEnt && ev > 0) || sharedCVE
	var parts []part
	if sharedEnt {
		if hasEnt {
			parts = append(parts, part{0.55, ev})
		}
		if hasCVE {
			parts = append(parts, part{0.25, cv})
		}
		if nv, ok := overlap(a.numbers, b.numbers); ok {
			parts = append(parts, part{0.15, nv})
		}
		if dv, ok := overlap(a.dates, b.dates); ok {
			parts = append(parts, part{0.10, dv})
		}
		if hasLex {
			parts = append(parts, part{0.20, lex})
		}
	} else if hasLex {
		parts = append(parts, part{1, lex})
	}
	if len(parts) == 0 {
		return 0
	}
	var sw, acc float64
	for _, p := range parts {
		sw += p.w
		acc += p.w * p.v
	}
	score := acc / sw
	if sharedCVE && score < 0.8 {
		score = 0.8
	}
	// A shared product name is enough to ask the model. The confirm step
	// still decides whether the mentions are one incident.
	if distinctiveShared(a.entities, b.entities) && score < 0.6 {
		score = 0.6
	}
	if score > 1 {
		return 1
	}
	return score
}

func overlap(a, b []string) (float64, bool) {
	sa, sb := toSet(a), toSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0, false
	}
	inter := 0
	for x := range sa {
		if _, ok := sb[x]; ok {
			inter++
		}
	}
	den := len(sa)
	if len(sb) < den {
		den = len(sb)
	}
	if den == 0 {
		return 0, false
	}
	return float64(inter) / float64(den), true
}

func jaccard(a, b []string) (float64, bool) {
	sa, sb := toSet(a), toSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0, false
	}
	inter := 0
	for x := range sa {
		if _, ok := sb[x]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0, false
	}
	return float64(inter) / float64(union), true
}

func diverges(a, b []string) bool {
	sa, sb := toSet(a), toSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return false
	}
	return !subset(sa, sb) && !subset(sb, sa)
}

func subset(a, b map[string]struct{}) bool {
	for x := range a {
		if _, ok := b[x]; !ok {
			return false
		}
	}
	return true
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		out[item] = struct{}{}
	}
	return out
}

func unique(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func distinctiveShared(a, b []string) bool {
	sb := toSet(b)
	for x := range toSet(a) {
		if _, ok := sb[x]; !ok {
			continue
		}
		if distinctiveName(x) {
			return true
		}
	}
	return false
}

func distinctiveName(s string) bool {
	if strings.Contains(s, " ") || weakEntity[s] {
		return false
	}
	return len([]rune(s)) >= 6
}

// quotedNames pulls product and organisation names out of «...» so two
// articles about one product stay candidates even when the extracted
// entity lists use different words.
func quotedNames(text string) []string {
	var out []string
	for _, m := range quotedNameRE.FindAllStringSubmatch(text, -1) {
		key := foldKey(m[1])
		if !distinctiveName(key) {
			continue
		}
		out = append(out, key)
	}
	return unique(out)
}

func normEntities(items []string) []string {
	var out []string
	for _, item := range items {
		for _, part := range strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n'
		}) {
			key := foldKey(part)
			if len([]rune(key)) < 3 {
				continue
			}
			out = append(out, key)
		}
	}
	return unique(out)
}

func foldKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'«»")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}

func findCVEs(s string) []string {
	found := cveRE.FindAllString(s, -1)
	out := make([]string, 0, len(found))
	for _, item := range found {
		out = append(out, strings.ToLower(item))
	}
	return unique(out)
}

func tokens(s string) []string {
	s = strings.ToLower(strings.ReplaceAll(s, "ё", "е"))
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func bigrams(words []string) []string {
	if len(words) < 2 {
		return nil
	}
	out := make([]string, 0, len(words)-1)
	for i := 0; i < len(words)-1; i++ {
		out = append(out, words[i]+" "+words[i+1])
	}
	return unique(out)
}

func sortedJoin(items []string) string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	return strings.Join(cp, ", ")
}
