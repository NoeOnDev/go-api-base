package shifts

import (
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// The hours worked are instants (clock in, clock out); the summary splits them by days of the
// worker's clock. These functions do not touch the database, so they are tested on their own,
// daylight saving time included (schedule_test.go).

// Worked is a range of time worked. End is the zero time while it is still open.
type Worked struct {
	Start, End time.Time
}

// DayTotal is how many minutes someone worked on a date of their clock.
type DayTotal struct {
	Date    civil.Date `json:"date"`
	Minutes int        `json:"minutes"`
}

// SplitByDay returns the minutes worked on each date from from to to, in loc. A range that crosses
// midnight counts on both days, each with its part; an open one counts until now. A day lasts what
// it really lasts: 23 or 25 hours when the clocks change.
func SplitByDay(worked []Worked, from, to civil.Date, loc *time.Location, now time.Time) []DayTotal {
	var totals []DayTotal
	for d := from; !d.After(to); d = d.AddDays(1) {
		dayStart, dayEnd := civil.DayRange(d, d, loc)
		var total time.Duration
		for _, w := range worked {
			end := w.End
			if end.IsZero() {
				end = now
			}
			start := later(w.Start, dayStart)
			if stop := earlier(end, dayEnd); stop.After(start) {
				total += stop.Sub(start)
			}
		}
		totals = append(totals, DayTotal{Date: d, Minutes: int(total / time.Minute)})
	}
	return totals
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
