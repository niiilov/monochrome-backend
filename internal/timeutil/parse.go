package timeutil

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Naive-zone policies. They match period.naive_tz_policy in config.yaml.
const (
	PolicyAssumeMSK = "assume_msk"
	PolicyReject    = "reject"
)

// Trace notes returned by ParseDate. The runner copies a non-empty note
// into the journal with stage "period". A nil time later becomes date_unconfirmed.
const (
	NoteEmpty         = "Дата публикации пустая"
	NoteDateOnly      = "Дата без времени, подтвердить нельзя"
	NoteUnparsed      = "Дату публикации разобрать нельзя"
	NoteAssumedMSK    = "Дата без пояса, принята как МСК"
	NoteRejectedZone  = "Дата без пояса, подтвердить нельзя"
	NoteUnknownPolicy = "Дата без пояса, политика не распознана"
)

var (
	isoDateOnlyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dottedDateRE  = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})$`)
	dottedTimeRE  = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})(?:,\s*|\s+)(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	verbalRE      = regexp.MustCompile(`(?i)^(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\s+(\d{4})(?:\s+г\.?)?(?:,\s*|\s+)(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	verbalDateRE  = regexp.MustCompile(`(?i)^(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\s+(\d{4})(?:\s+г\.?)?$`)
	unixRE        = regexp.MustCompile(`^\d{10}$|^\d{13}$`)
)

var ruMonths = map[string]time.Month{
	"января": time.January, "февраля": time.February, "марта": time.March,
	"апреля": time.April, "мая": time.May, "июня": time.June,
	"июля": time.July, "августа": time.August, "сентября": time.September,
	"октября": time.October, "ноября": time.November, "декабря": time.December,
}

// Layouts that carry a zone. More specific layouts come first.
var zonedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02T15:04:05Z0700",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04Z0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05Z0700",
	"2006-01-02 15:04Z07:00",
	time.RFC1123Z,
	time.RFC1123,
	time.UnixDate,
	time.RubyDate,
}

var naiveLayouts = []string{
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// ParseDate reads one publication timestamp.
// A confirmed value is in MSK. Date-only, empty and unreadable values return nil and a Russian note.
// A timestamp without a zone follows naivePolicy: assume_msk (default) keeps the clock in MSK and
// returns NoteAssumedMSK; reject returns nil and NoteRejectedZone.
func ParseDate(raw, naivePolicy string) (*time.Time, string) {
	s := normalizeRaw(raw)
	if s == "" {
		return nil, NoteEmpty
	}
	policy := normalizePolicy(naivePolicy)
	if dateOnly(s) {
		return nil, NoteDateOnly
	}
	if t, ok := parseUnix(s); ok {
		return inMSK(t), ""
	}
	if t, ok := parseZoned(s); ok {
		return inMSK(t), ""
	}
	if t, ok := parseNaive(s); ok {
		return finishNaive(t, policy)
	}
	return nil, NoteUnparsed
}

func normalizeRaw(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\u202f", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) >= 11 && (s[10] == 't' || s[10] == ' ') && s[4] == '-' && s[7] == '-' {
		s = s[:10] + "T" + s[11:]
	}
	if strings.HasSuffix(s, "z") && strings.Contains(s, "T") {
		s = s[:len(s)-1] + "Z"
	}
	return s
}

func normalizePolicy(policy string) string {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		return PolicyAssumeMSK
	}
	return policy
}

func dateOnly(s string) bool {
	return isoDateOnlyRE.MatchString(s) || dottedDateRE.MatchString(s) || verbalDateRE.MatchString(s)
}

func parseUnix(s string) (time.Time, bool) {
	if !unixRE.MatchString(s) {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	var t time.Time
	switch len(s) {
	case 10:
		t = time.Unix(n, 0)
	case 13:
		t = time.UnixMilli(n)
	default:
		return time.Time{}, false
	}
	year := t.UTC().Year()
	if year < 1990 || year > 2100 {
		return time.Time{}, false
	}
	return t, true
}

func parseZoned(s string) (time.Time, bool) {
	if t, ok := parseNamedZone(s); ok {
		return t, true
	}
	for _, layout := range zonedLayouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseNamedZone accepts a trailing MSK or UTC word, for example "07.11.2026 14:05 МСК".
func parseNamedZone(s string) (time.Time, bool) {
	rest, loc, ok := cutZoneWord(s)
	if !ok || rest == "" || dateOnly(rest) {
		return time.Time{}, false
	}
	wallClock, ok := parseNaive(rest)
	if !ok {
		return time.Time{}, false
	}
	return relocate(wallClock, loc), true
}

func cutZoneWord(s string) (string, *time.Location, bool) {
	i := strings.LastIndex(s, " ")
	if i <= 0 {
		return "", nil, false
	}
	switch strings.ToLower(s[i+1:]) {
	case "мск", "msk":
		return s[:i], MSK, true
	case "utc", "gmt":
		return s[:i], time.UTC, true
	default:
		return "", nil, false
	}
}

func relocate(t time.Time, loc *time.Location) time.Time {
	year, month, day := t.Date()
	hour, min, sec := t.Clock()
	return time.Date(year, month, day, hour, min, sec, t.Nanosecond(), loc)
}

func parseNaive(s string) (time.Time, bool) {
	if t, ok := parseDotted(s); ok {
		return t, true
	}
	if t, ok := parseVerbal(s); ok {
		return t, true
	}
	for _, layout := range naiveLayouts {
		t, err := time.ParseInLocation(layout, s, MSK)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseDotted(s string) (time.Time, bool) {
	m := dottedTimeRE.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	return wall(atoi(m[3]), atoi(m[2]), atoi(m[1]), atoi(m[4]), atoi(m[5]), atoiOpt(m[6]))
}

func parseVerbal(s string) (time.Time, bool) {
	m := verbalRE.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	month, ok := ruMonths[strings.ToLower(m[2])]
	if !ok {
		return time.Time{}, false
	}
	return wall(atoi(m[3]), int(month), atoi(m[1]), atoi(m[4]), atoi(m[5]), atoiOpt(m[6]))
}

func wall(year, month, day, hour, min, sec int) (time.Time, bool) {
	if month < 1 || month > 12 || day < 1 || hour < 0 || hour > 23 || min < 0 || min > 59 || sec < 0 || sec > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, MSK)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}

func finishNaive(t time.Time, policy string) (*time.Time, string) {
	switch policy {
	case PolicyAssumeMSK:
		return inMSK(t), NoteAssumedMSK
	case PolicyReject:
		return nil, NoteRejectedZone
	default:
		return nil, NoteUnknownPolicy
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atoiOpt(s string) int {
	if s == "" {
		return 0
	}
	return atoi(s)
}
