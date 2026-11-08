package ingest

import (
	"testing"

	"mono/internal/domain"
)

func TestSourceKindOf(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want domain.SourceKind
	}{
		{"SecurityLab", "https://www.securitylab.ru/news/1", domain.SourceMedia},
		{"Хакер", "https://xakep.ru/1", domain.SourceMedia},
		{"ТАСС", "", domain.SourceMedia},
		{"CNews", "https://safe.cnews.ru/news/1", domain.SourceMedia},
		{"Хабр", "https://habr.com/ru/articles/1", domain.SourceAuthor},
		{"iXBT", "https://www.ixbt.com/live/security/1", domain.SourceAuthor},
		{"iXBT", "https://www.ixbt.com/news/1", domain.SourceMedia},
		{"Positive Technologies", "https://ptsecurity.com/research/1", domain.SourceVendor},
		{"Солар", "https://rt-solar.ru/events/news/1", domain.SourceVendor},
		{"Лаборатория Касперского", "", domain.SourceVendor},
		{"UserGate", "https://www.usergate.com/ru", domain.SourceVendor},
		{"ИнфоТеКС", "https://infotecs.ru/1", domain.SourceVendor},
		{"InfoWatch", "https://www.infowatch.ru/analytics/1", domain.SourceMedia},
		{"Роскомнадзор", "", domain.SourceRegulator},
		{"Блог Петрова", "https://blog.example.com/1", domain.SourceUnknown},
		{"", "", domain.SourceUnknown},
	}
	for _, tc := range cases {
		got := SourceKindOf(tc.name, tc.url)
		if got != tc.want {
			t.Errorf("%s %s: got %s want %s", tc.name, tc.url, got, tc.want)
		}
	}
}
