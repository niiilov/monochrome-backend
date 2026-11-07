package timeutil

import (
	"strings"
	"testing"
	"time"
)

func TestParseDateFormats(t *testing.T) {
	wantMSK := func(y int, m time.Month, d, h, min, s int) time.Time {
		return time.Date(y, m, d, h, min, s, 0, MSK)
	}
	cases := []struct {
		name   string
		raw    string
		policy string
		want   *time.Time
		note   string
	}{
		{name: "iso offset", raw: "2026-11-07T14:00:00+05:00", want: tp(wantMSK(2026, 11, 7, 12, 0, 0))},
		{name: "iso z", raw: "2026-11-07T09:00:00Z", want: tp(wantMSK(2026, 11, 7, 12, 0, 0))},
		{name: "iso z lower", raw: "2026-11-07t04:15:00z", want: tp(wantMSK(2026, 11, 7, 7, 15, 0))},
		{name: "iso space offset", raw: "2026-11-06 18:40:00+03:00", want: tp(wantMSK(2026, 11, 6, 18, 40, 0))},
		{name: "iso numeric offset", raw: "2026-11-07T08:00:00+0300", want: tp(wantMSK(2026, 11, 7, 8, 0, 0))},
		{name: "dotted comma", raw: "07.11.2026, 11:45", want: tp(wantMSK(2026, 11, 7, 11, 45, 0)), note: NoteAssumedMSK},
		{name: "dotted tight comma", raw: "07.11.2026,11:45", want: tp(wantMSK(2026, 11, 7, 11, 45, 0)), note: NoteAssumedMSK},
		{name: "verbal space", raw: "6 ноября 2026 19:30", want: tp(wantMSK(2026, 11, 6, 19, 30, 0)), note: NoteAssumedMSK},
		{name: "dotted space seconds", raw: "7.11.2026 11:45:30", want: tp(wantMSK(2026, 11, 7, 11, 45, 30)), note: NoteAssumedMSK},
		{name: "verbal", raw: "2 ноября 2026, 14:05", want: tp(wantMSK(2026, 11, 2, 14, 5, 0)), note: NoteAssumedMSK},
		{name: "verbal day", raw: "6 ноября 2026, 19:30", want: tp(wantMSK(2026, 11, 6, 19, 30, 0)), note: NoteAssumedMSK},
		{name: "rfc1123z", raw: "Sat, 07 Nov 2026 08:00:00 +0300", want: tp(wantMSK(2026, 11, 7, 8, 0, 0))},
		{name: "rfc1123 gmt", raw: "Sat, 07 Nov 2026 05:00:00 GMT", want: tp(wantMSK(2026, 11, 7, 8, 0, 0))},
		{name: "unix", raw: "1794029400", want: tp(wantMSK(2026, 11, 7, 8, 30, 0))},
		{name: "unix ms", raw: "1794029400000", want: tp(wantMSK(2026, 11, 7, 8, 30, 0))},
		{name: "word msk", raw: "07.11.2026 14:05 МСК", want: tp(wantMSK(2026, 11, 7, 14, 5, 0))},
		{name: "word utc", raw: "07.11.2026 11:00 UTC", want: tp(wantMSK(2026, 11, 7, 14, 0, 0))},
		{name: "naive iso", raw: "2026-11-07T11:00:00", want: tp(wantMSK(2026, 11, 7, 11, 0, 0)), note: NoteAssumedMSK},
		{name: "naive rejected", raw: "2026-11-07T11:00:00", policy: PolicyReject, note: NoteRejectedZone},
		{name: "date only iso", raw: "2026-11-07", note: NoteDateOnly},
		{name: "date only dotted", raw: "07.11.2026", note: NoteDateOnly},
		{name: "date only verbal", raw: "2 ноября 2026", note: NoteDateOnly},
		{name: "empty", raw: "  ", note: NoteEmpty},
		{name: "garbage", raw: "вчера", note: NoteUnparsed},
		{name: "impossible day", raw: "31.02.2026 10:00", note: NoteUnparsed},
		{name: "unknown policy", raw: "07.11.2026 10:00", policy: "utc", note: NoteUnknownPolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, note := ParseDate(tc.raw, tc.policy)
			if note != tc.note {
				t.Fatalf("note %q, want %q", note, tc.note)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %s, want nil", got.Format(time.RFC3339))
				}
				return
			}
			if got == nil {
				t.Fatal("got nil")
			}
			if !got.Equal(*tc.want) {
				t.Fatalf("got %s, want %s", got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			if got.Location().String() != "Europe/Moscow" {
				t.Fatalf("location %s", got.Location())
			}
			if strings.ContainsRune(note, '\u2014') || strings.ContainsRune(note, '\u2013') {
				t.Fatalf("long dash in note %q", note)
			}
		})
	}
}

func TestMSKLoadedFromTZData(t *testing.T) {
	if MSK == nil || MSK.String() != "Europe/Moscow" {
		t.Fatalf("MSK = %v", MSK)
	}
	_, offset := time.Date(2026, 11, 7, 12, 0, 0, 0, MSK).Zone()
	if offset != 3*60*60 {
		t.Fatalf("offset %d", offset)
	}
}

func tp(t time.Time) *time.Time { return &t }
