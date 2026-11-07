package timeutil

import (
	"time"
	_ "time/tzdata" // keep Europe/Moscow available without a system zoneinfo database
)

// MSK is Europe/Moscow. The digest window and PublishedAt use this location.
var MSK = mustMSK()

func mustMSK() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		panic("timeutil: Europe/Moscow: " + err.Error())
	}
	return loc
}

func inMSK(t time.Time) *time.Time {
	out := t.In(MSK)
	return &out
}
