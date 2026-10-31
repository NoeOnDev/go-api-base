package classes

import (
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// Yoga on Mondays and Wednesdays at 18:00 in New York, from October 19 to November 11, 2026.
func yoga() Rule {
	return Rule{
		Weekdays:  []time.Weekday{time.Monday, time.Wednesday},
		StartTime: civil.NewTime(18, 0),
		Duration:  time.Hour,
		StartsOn:  civil.NewDate(2026, time.October, 19),
		EndsOn:    civil.NewDate(2026, time.November, 11),
	}
}

func TestSessionsKeepTheHourOfTheClock(t *testing.T) {
	ny, _ := civil.LoadLocation("America/New_York")
	sessions := Sessions(yoga(), nil, civil.NewDate(2026, time.October, 1), civil.NewDate(2026, time.November, 30), ny)

	var dates []string
	for _, s := range sessions {
		dates = append(dates, s.Date.String())
	}
	want := []string{"2026-10-19", "2026-10-21", "2026-10-26", "2026-10-28", "2026-11-02", "2026-11-04", "2026-11-09", "2026-11-11"}
	if len(dates) != len(want) {
		t.Fatalf("sessions = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("sessions = %v, want %v", dates, want)
		}
	}
	// 18:00 is 22:00 UTC before November 1, and 23:00 UTC after.
	if got := sessions[3].StartsAt.Format(time.RFC3339); got != "2026-10-28T22:00:00Z" {
		t.Errorf("October 28 = %s", got)
	}
	if got := sessions[4].StartsAt.Format(time.RFC3339); got != "2026-11-02T23:00:00Z" {
		t.Errorf("November 2 = %s", got)
	}
	if got := sessions[4].EndsAt.Sub(sessions[4].StartsAt); got != time.Hour {
		t.Errorf("lasts %v", got)
	}

	// Without an end, it keeps going.
	open := yoga()
	open.EndsOn = civil.Date{}
	if got := len(Sessions(open, nil, civil.NewDate(2027, time.January, 4), civil.NewDate(2027, time.January, 10), ny)); got != 2 {
		t.Errorf("a week of 2027: %d sessions, want 2", got)
	}
}

func TestSessionsWithChanges(t *testing.T) {
	ny, _ := civil.LoadLocation("America/New_York")
	changes := []Change{
		{Date: civil.NewDate(2026, time.October, 26), Canceled: true, Note: "Holiday"},
		// Wednesday's moved to Thursday at 19:00.
		{Date: civil.NewDate(2026, time.October, 28), LocalStartsAt: civil.NewDate(2026, time.October, 29).At(civil.NewTime(19, 0))},
		// Monday the 2nd moved back to Saturday, October 31: it leaves the week of the 2nd.
		{Date: civil.NewDate(2026, time.November, 2), LocalStartsAt: civil.NewDate(2026, time.October, 31).At(civil.NewTime(10, 0))},
	}

	week := Sessions(yoga(), changes, civil.NewDate(2026, time.October, 26), civil.NewDate(2026, time.November, 1), ny)
	if len(week) != 3 {
		t.Fatalf("week of the 26th = %+v", week)
	}
	if !week[0].Canceled || week[0].Note != "Holiday" {
		t.Errorf("Monday = %+v", week[0])
	}
	if !week[1].Moved || week[1].LocalStartsAt.String() != "2026-10-29T19:00" || week[1].Date.String() != "2026-10-28" {
		t.Errorf("Wednesday moved = %+v", week[1])
	}
	// The one of the 2nd, moved into this week from outside it.
	if !week[2].Moved || week[2].Date.String() != "2026-11-02" || week[2].StartsAt.Format(time.RFC3339) != "2026-10-31T14:00:00Z" {
		t.Errorf("session moved in = %+v", week[2])
	}

	next := Sessions(yoga(), changes, civil.NewDate(2026, time.November, 2), civil.NewDate(2026, time.November, 8), ny)
	if len(next) != 1 || next[0].Date.String() != "2026-11-04" {
		t.Errorf("week of the 2nd = %+v, want only the 4th", next)
	}
}
