package timeutil

import (
	"testing"
	"time"

	"cyberdigest/internal/config"
	"cyberdigest/internal/domain"
)

func TestNewPeriodAndBoundaries(t *testing.T) {
	control := time.Date(2026, 11, 7, 12, 0, 0, 0, MSK)
	p := NewPeriod(control)
	if !p.End.Equal(control) {
		t.Fatalf("end %s", p.End)
	}
	if !p.Start.Equal(control.Add(-24 * time.Hour)) {
		t.Fatalf("start %s", p.Start)
	}
	if got := FormatPeriod(p); got != "06.11.2026 12:00 - 07.11.2026 12:00 МСК" {
		t.Fatalf("format %q", got)
	}

	plus5 := time.FixedZone("PLUS5", 5*60*60)
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "exactly start", at: p.Start, want: false},
		{name: "start+1s", at: p.Start.Add(time.Second), want: true},
		{name: "exactly end", at: p.End, want: true},
		{name: "end+1s", at: p.End.Add(time.Second), want: false},
		{name: "utc equal start", at: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), want: false},
		{name: "utc start+1s", at: time.Date(2026, 11, 6, 9, 0, 1, 0, time.UTC), want: true},
		{name: "utc equal end", at: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), want: true},
		{name: "utc end+1s", at: time.Date(2026, 11, 7, 9, 0, 1, 0, time.UTC), want: false},
		{name: "plus5 equal start", at: time.Date(2026, 11, 6, 14, 0, 0, 0, plus5), want: false},
		{name: "plus5 inside", at: time.Date(2026, 11, 7, 10, 0, 0, 0, plus5), want: true},
		{name: "plus5 equal end", at: time.Date(2026, 11, 7, 14, 0, 0, 0, plus5), want: true},
		{name: "plus5 end+1s", at: time.Date(2026, 11, 7, 14, 0, 1, 0, plus5), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Contains(tc.at); got != tc.want {
				t.Fatalf("Contains(%s) = %v, want %v", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestNewPeriodFromUTCControl(t *testing.T) {
	control := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	p := NewPeriod(control)
	if p.End.Location().String() != "Europe/Moscow" {
		t.Fatalf("location %s", p.End.Location())
	}
	if FormatMSK(p.End) != "07.11.2026 12:00" || FormatMSK(p.Start) != "06.11.2026 12:00" {
		t.Fatalf("window %s .. %s", FormatMSK(p.Start), FormatMSK(p.End))
	}
}

func TestFilterPeriodBuckets(t *testing.T) {
	control := time.Date(2026, 11, 7, 12, 0, 0, 0, MSK)
	period := NewPeriod(control)
	preset := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC) // 11:00 MSK, inside
	arts := []domain.Article{
		{ID: "start", PublishedRaw: "2026-11-06T12:00:00+03:00"},
		{ID: "start-plus", PublishedRaw: "2026-11-06T12:00:01+03:00"},
		{ID: "end", PublishedRaw: "2026-11-07T12:00:00+03:00"},
		{ID: "end-plus", PublishedRaw: "2026-11-07T12:00:01+03:00"},
		{ID: "utc", PublishedRaw: "2026-11-07T04:15:00Z"},
		{ID: "plus5", PublishedRaw: "2026-11-07T10:00:00+05:00"},
		{ID: "naive", PublishedRaw: "2026-11-07T11:00:00"},
		{ID: "dotted", PublishedRaw: "07.11.2026, 11:45"},
		{ID: "verbal", PublishedRaw: "6 ноября 2026, 19:30"},
		{ID: "rfc", PublishedRaw: "Sat, 07 Nov 2026 08:00:00 +0300"},
		{ID: "unix", PublishedRaw: "1794029400"},
		{ID: "date-only", PublishedRaw: "07.11.2026"},
		{ID: "empty", PublishedRaw: ""},
		{ID: "garbage", PublishedRaw: "не дата"},
		{ID: "preset", PublishedAt: &preset, PublishedRaw: "ignored"},
		{ID: "outside", PublishedRaw: "2026-11-01T09:00:00+03:00"},
	}
	originalPreset := arts[14].PublishedAt

	in, out, unconfirmed, notes := FilterPeriod(arts, period, config.Period{NaiveTZPolicy: PolicyAssumeMSK})

	if ids(in) != "start-plus,end,utc,plus5,naive,dotted,verbal,rfc,unix,preset" {
		t.Fatalf("in %s", ids(in))
	}
	if ids(out) != "start,end-plus,outside" {
		t.Fatalf("out %s", ids(out))
	}
	if ids(unconfirmed) != "date-only,empty,garbage" {
		t.Fatalf("unconfirmed %s", ids(unconfirmed))
	}
	if notes["naive"] != NoteAssumedMSK || notes["dotted"] != NoteAssumedMSK || notes["verbal"] != NoteAssumedMSK {
		t.Fatalf("naive notes %#v", notes)
	}
	if notes["date-only"] != NoteDateOnly || notes["empty"] != NoteEmpty || notes["garbage"] != NoteUnparsed {
		t.Fatalf("bad notes %#v", notes)
	}
	if _, ok := notes["utc"]; ok {
		t.Fatal("explicit zone must not produce a trace note")
	}
	if arts[14].PublishedAt != originalPreset {
		t.Fatal("input article was modified")
	}
	for _, article := range in {
		if article.PublishedAt == nil || article.PublishedAt.Location().String() != "Europe/Moscow" {
			t.Fatalf("%s published_at", article.ID)
		}
	}
	naive := find(in, "naive")
	if FormatMSK(*naive.PublishedAt) != "07.11.2026 11:00" {
		t.Fatalf("naive wall %s", FormatMSK(*naive.PublishedAt))
	}
	plus5 := find(in, "plus5")
	if FormatMSK(*plus5.PublishedAt) != "07.11.2026 08:00" {
		t.Fatalf("plus5 wall %s", FormatMSK(*plus5.PublishedAt))
	}
}

func TestFilterPeriodRejectsNaive(t *testing.T) {
	period := NewPeriod(time.Date(2026, 11, 7, 12, 0, 0, 0, MSK))
	arts := []domain.Article{{ID: "n", PublishedRaw: "07.11.2026, 11:45"}}
	in, out, unconfirmed, notes := FilterPeriod(arts, period, config.Period{NaiveTZPolicy: PolicyReject})
	if len(in) != 0 || len(out) != 0 || ids(unconfirmed) != "n" {
		t.Fatalf("in %d out %d unconfirmed %s", len(in), len(out), ids(unconfirmed))
	}
	if notes["n"] != NoteRejectedZone || unconfirmed[0].PublishedAt != nil {
		t.Fatalf("note %q at %v", notes["n"], unconfirmed[0].PublishedAt)
	}
}

func TestFilterPeriodEmptyPolicyAssumesMSK(t *testing.T) {
	period := NewPeriod(time.Date(2026, 11, 7, 12, 0, 0, 0, MSK))
	arts := []domain.Article{{ID: "n", PublishedRaw: "07.11.2026, 11:45"}}
	in, _, unconfirmed, notes := FilterPeriod(arts, period, config.Period{})
	if ids(in) != "n" || len(unconfirmed) != 0 || notes["n"] != NoteAssumedMSK {
		t.Fatalf("in %s notes %q", ids(in), notes["n"])
	}
}

func ids(arts []domain.Article) string {
	out := make([]string, len(arts))
	for i, article := range arts {
		out[i] = article.ID
	}
	return stringsJoin(out)
}

func stringsJoin(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for _, part := range parts[1:] {
		s += "," + part
	}
	return s
}

func find(arts []domain.Article, id string) domain.Article {
	for _, article := range arts {
		if article.ID == id {
			return article
		}
	}
	return domain.Article{}
}
