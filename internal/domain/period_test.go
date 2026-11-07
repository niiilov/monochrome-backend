package domain

import (
	"testing"
	"time"
)

func TestPeriodContains(t *testing.T) {
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	end := start.Add(24 * time.Hour)
	p := Period{Start: start, End: end}

	if p.Contains(start) {
		t.Fatal("ровно start не входит в (start, end]")
	}
	if !p.Contains(start.Add(time.Second)) {
		t.Fatal("start+1s должен входить")
	}
	if !p.Contains(end) {
		t.Fatal("ровно end должен входить")
	}
	if p.Contains(end.Add(time.Second)) {
		t.Fatal("end+1s не входит")
	}
}
