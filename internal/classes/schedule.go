package classes

import (
	"slices"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// A class is a rule (some days of the week, at an hour of the clock, from one date to another) and
// the changes to some sessions. These functions compute the sessions of some days without the
// database, so they are tested on their own, daylight saving time included (schedule_test.go).

// Rule is when a class repeats.
type Rule struct {
	Weekdays  []time.Weekday
	StartTime civil.Time
	Duration  time.Duration
	StartsOn  civil.Date
	// EndsOn is the last day; the zero value is no end.
	EndsOn civil.Date
}

// isSession reports whether the rule has a session on d.
func (r Rule) isSession(d civil.Date) bool {
	return !d.Before(r.StartsOn) && (r.EndsOn.IsZero() || !d.After(r.EndsOn)) && slices.Contains(r.Weekdays, d.Weekday())
}

// Change is a change to the session of the rule on Date: canceled, or moved to LocalStartsAt.
type Change struct {
	Date          civil.Date
	Canceled      bool
	LocalStartsAt civil.DateTime // the zero value: not moved
	Note          string
}

// Session is one session of a class.
type Session struct {
	// Date is the date of the session in the rule (the one a change refers to).
	Date civil.Date `json:"date"`
	// LocalStartsAt is its hour of the clock; StartsAt and EndsAt, its instants.
	LocalStartsAt civil.DateTime `json:"local_starts_at"`
	StartsAt      time.Time      `json:"starts_at"`
	EndsAt        time.Time      `json:"ends_at"`
	Canceled      bool           `json:"canceled"`
	Moved         bool           `json:"moved"`
	Note          string         `json:"note"`
}

// Sessions returns the sessions of the class whose day of the clock (the new one, if it was moved) is
// between from and to, sorted by when they start. The canceled ones are included, marked. changes
// must include those of the sessions of those days and those moved into them.
//
// The hour of the clock is kept: a class at 18:00 in New York is at 18:00 in summer and in winter,
// although in UTC it is at 22:00 or at 23:00.
func Sessions(r Rule, changes []Change, from, to civil.Date, loc *time.Location) []Session {
	byDate := make(map[civil.Date]Change, len(changes))
	for _, c := range changes {
		byDate[c.Date] = c
	}

	// The dates of the rule in the range, and those of the moved sessions that may land in it.
	var dates []civil.Date
	for d := from; !d.After(to); d = d.AddDays(1) {
		dates = append(dates, d)
	}
	for _, c := range changes {
		if c.Date.Before(from) || c.Date.After(to) {
			dates = append(dates, c.Date)
		}
	}

	sessions := []Session{}
	for _, d := range dates {
		if !r.isSession(d) {
			continue
		}
		s := Session{Date: d, LocalStartsAt: d.At(r.StartTime)}
		if c, ok := byDate[d]; ok {
			s.Canceled, s.Note = c.Canceled, c.Note
			if !c.LocalStartsAt.IsZero() {
				s.LocalStartsAt, s.Moved = c.LocalStartsAt, true
			}
		}
		if day := s.LocalStartsAt.Date(); day.Before(from) || day.After(to) {
			continue
		}
		s.StartsAt = s.LocalStartsAt.In(loc).UTC()
		s.EndsAt = s.StartsAt.Add(r.Duration)
		sessions = append(sessions, s)
	}
	slices.SortFunc(sessions, func(a, b Session) int { return a.StartsAt.Compare(b.StartsAt) })
	return sessions
}
