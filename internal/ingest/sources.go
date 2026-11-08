package ingest

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"mono/internal/domain"
)

// sourceSite is one outlet from case appendix 1, plus regulators organizers may add.
type sourceSite struct {
	kind  domain.SourceKind
	names []string
	hosts []string
}

// sites is matched in order: regulator, vendor, author, then media.
// InfoWatch stays media: the appendix lists it with news sites.
// Vendors are the last five rows: Positive Technologies, Солар, Лаборатория Касперского, UserGate, ИнфоТеКС.
var sites = []sourceSite{
	{
		kind:  domain.SourceRegulator,
		names: []string{"фстэк", "фсб", "роскомнадзор", "минцифры", "нкцки", "госсопка", "банк россии", "цб рф"},
		hosts: []string{"fstec.ru", "fsb.ru", "rkn.gov.ru", "digital.gov.ru", "cert.gov.ru", "cbr.ru"},
	},
	{
		kind: domain.SourceVendor,
		names: []string{
			"positive technologies", "лаборатория касперского", "kaspersky",
			"usergate", "инфотекс", "infotecs", "ростелеком-солар", "rt-solar", "солар",
		},
		hosts: []string{"ptsecurity.com", "kaspersky.ru", "kaspersky.com", "usergate.com", "infotecs.ru", "rt-solar.ru"},
	},
	{
		kind:  domain.SourceAuthor,
		names: []string{"хабр", "habr"},
		hosts: []string{"habr.com"},
	},
	{
		kind: domain.SourceMedia,
		names: []string{
			"securitylab", "хакер", "comnews", "коммерсантъ", "коммерсант", "тасс",
			"риа новости", "риа", "it-world", "securitymedia", "infowatch", "инфобезопасность",
			"ict.moscow", "газета.ru", "газета.ру", "аргументы и факты", "aif", "cnews",
			"rb.ru", "3dnews", "компьютерра", "itweek", "ixbt", "overclockers",
			"российская газета", "известия",
		},
		hosts: []string{
			"securitylab.ru", "xakep.ru", "comnews.ru", "kommersant.ru", "tass.ru",
			"ria.ru", "it-world.ru", "securitymedia.org", "infowatch.ru", "infobezopasnost.ru",
			"ict.moscow", "gazeta.ru", "aif.ru", "cnews.ru", "rb.ru", "3dnews.ru",
			"computerra.ru", "itweek.ru", "ixbt.com", "overclockers.ru", "rg.ru", "iz.ru",
		},
	},
}

// SourceKindOf classifies a publication by source name and URL.
// iXBT blogs under /live/ are author pages. Anything else in the registry is media.
// An unknown outlet returns domain.SourceUnknown.
func SourceKindOf(name, rawURL string) domain.SourceKind {
	host, path := splitHostPath(rawURL)
	if host == "ixbt.com" && strings.Contains(path, "/live/") {
		return domain.SourceAuthor
	}
	if host != "" {
		if kind, ok := kindByHost(host); ok {
			return kind
		}
	}
	folded := foldName(name)
	if folded == "" {
		return domain.SourceUnknown
	}
	for _, site := range sites {
		for _, n := range site.names {
			if hasPhrase(folded, n) {
				return site.kind
			}
		}
	}
	return domain.SourceUnknown
}

func kindByHost(host string) (domain.SourceKind, bool) {
	bestLen := 0
	var kind domain.SourceKind
	found := false
	for _, site := range sites {
		for _, h := range site.hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				if len(h) > bestLen {
					bestLen = len(h)
					kind = site.kind
					found = true
				}
			}
		}
	}
	return kind, found
}

func splitHostPath(rawURL string) (host, path string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		if !strings.Contains(rawURL, "://") {
			u, err = url.Parse("https://" + rawURL)
		}
		if err != nil || u == nil || u.Hostname() == "" {
			return "", ""
		}
	}
	host = strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host, strings.ToLower(u.EscapedPath())
}

func foldName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "ё", "е")
}

func hasPhrase(text, phrase string) bool {
	if phrase == "" || text == "" {
		return false
	}
	start := 0
	for {
		i := strings.Index(text[start:], phrase)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(phrase)
		if beforeOK(text, i) && afterOK(text, end) {
			return true
		}
		start = i + len(phrase)
		if start >= len(text) {
			return false
		}
	}
}

func beforeOK(s string, i int) bool {
	if i <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func afterOK(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
