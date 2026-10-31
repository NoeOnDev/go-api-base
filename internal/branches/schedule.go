package branches

import (
	"slices"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// The hours of a branch are wall-clock times of its zone. These functions turn them into the real
// openings of some days, in instants. They do not touch the database, so they are tested on their
// own, daylight saving time included (schedule_test.go). See "Modeling schedules" in docs/time.md.

// Hours is a range of the wall clock. If ClosesAt is not after OpensAt, the range crosses midnight
// and ends the next day (18:00 to 02:00).
type Hours struct {
	OpensAt  civil.Time `json:"opens_at"`
	ClosesAt civil.Time `json:"closes_at"`
}

// crossesMidnight reports whether h ends the next day.
func (h Hours) crossesMidnight() bool { return !h.ClosesAt.After(h.OpensAt) }

// WeeklyHours is a range of one day of the week.
type WeeklyHours struct {
	// Weekday goes from 0 (Sunday) to 6 (Saturday), like time.Weekday and JavaScript's getDay().
	Weekday time.Weekday `json:"weekday"`
	Hours
}

// Exception replaces the weekly hours of a date: a holiday (Closed) or a day with other hours.
type Exception struct {
	Date   civil.Date `json:"date"`
	Closed bool       `json:"closed"`
	// Hours are those of that day, if it is not closed.
	Hours *Hours `json:"hours"`
	Note  string `json:"note"`
}

// Opening is a range of a day in instants: when the branch really opens and closes.
type Opening struct {
	Hours
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// Day is the hours of one date: the weekly ones, or those of its exception.
type Day struct {
	Date      civil.Date `json:"date"`
	Exception bool       `json:"exception"`
	Note      string     `json:"note"`
	// Openings is empty if the branch is closed that day. A range that crosses midnight belongs to
	// the day it starts.
	Openings []Opening `json:"openings"`
}

// Days returns the hours of each date from from to to (both included) in loc.
func Days(weekly []WeeklyHours, exceptions []Exception, from, to civil.Date, loc *time.Location) []Day {
	byDate := make(map[civil.Date]Exception, len(exceptions))
	for _, e := range exceptions {
		byDate[e.Date] = e
	}

	var days []Day
	for d := from; !d.After(to); d = d.AddDays(1) {
		day := Day{Date: d, Openings: []Opening{}}
		var hours []Hours
		if e, ok := byDate[d]; ok {
			day.Exception, day.Note = true, e.Note
			if !e.Closed && e.Hours != nil {
				hours = []Hours{*e.Hours}
			}
		} else {
			for _, w := range weekly {
				if w.Weekday == d.Weekday() {
					hours = append(hours, w.Hours)
				}
			}
		}
		for _, h := range hours {
			if o, ok := open(d, h, loc); ok {
				day.Openings = append(day.Openings, o)
			}
		}
		slices.SortFunc(day.Openings, func(a, b Opening) int { return a.StartsAt.Compare(b.StartsAt) })
		days = append(days, day)
	}
	return days
}

// open turns the range h of the date d into instants. When the clocks change, an hour that does not
// exist moves forward as long as the jump (see civil.DateTime.In): 02:00 to 02:30 on the day of the
// spring jump opens from 03:00 to 03:30. A range left empty that way (02:15 to 03:00) does not open.
func open(d civil.Date, h Hours, loc *time.Location) (Opening, bool) {
	closesOn := d
	if h.crossesMidnight() {
		closesOn = d.AddDays(1)
	}
	o := Opening{
		Hours:    h,
		StartsAt: d.At(h.OpensAt).In(loc).UTC(),
		EndsAt:   closesOn.At(h.ClosesAt).In(loc).UTC(),
	}
	return o, o.EndsAt.After(o.StartsAt)
}

// Status is whether a branch is open at a moment.
type Status struct {
	Open bool `json:"open"`
	// ClosesAt is when it closes, if it is open.
	ClosesAt *time.Time `json:"closes_at"`
	// NextOpensAt is when it opens next (after ClosesAt, if it is open), or null if it has no hours
	// in the next two weeks.
	NextOpensAt *time.Time `json:"next_opens_at"`
}

// statusDays is how many days ahead StatusAt looks for the next opening.
const statusDays = 14

// StatusAt returns the status at the instant now. exceptions must cover from the day before now
// (a range that crosses midnight may still be open) to statusDays later, in loc.
func StatusAt(now time.Time, weekly []WeeklyHours, exceptions []Exception, loc *time.Location) Status {
	today := civil.DateOf(now.In(loc))
	var openings []Opening
	for _, d := range Days(weekly, exceptions, today.AddDays(-1), today.AddDays(statusDays), loc) {
		openings = append(openings, d.Openings...)
	}

	// Ranges that touch or overlap are one opening: 18:00-00:00 and 00:00-02:00 close at 02:00.
	slices.SortFunc(openings, func(a, b Opening) int { return a.StartsAt.Compare(b.StartsAt) })
	var merged []Opening
	for _, o := range openings {
		if n := len(merged); n > 0 && !o.StartsAt.After(merged[n-1].EndsAt) {
			if o.EndsAt.After(merged[n-1].EndsAt) {
				merged[n-1].EndsAt = o.EndsAt
			}
			continue
		}
		merged = append(merged, o)
	}

	var s Status
	for _, o := range merged {
		switch {
		case !now.Before(o.StartsAt) && now.Before(o.EndsAt):
			s.Open = true
			s.ClosesAt = &o.EndsAt
		case o.StartsAt.After(now):
			s.NextOpensAt = &o.StartsAt
			return s
		}
	}
	return s
}

// overlaps reports whether two weekly ranges overlap, counting the ones that cross midnight
// (Saturday night into Sunday too).
func overlaps(a, b WeeklyHours) bool {
	const week = 7 * 24 * 60
	start := func(w WeeklyHours) int { return int(w.Weekday)*24*60 + w.OpensAt.Minutes() }
	end := func(w WeeklyHours) int {
		e := int(w.Weekday)*24*60 + w.ClosesAt.Minutes()
		if w.crossesMidnight() {
			e += 24 * 60
		}
		return e
	}
	// Compared also a week apart, for the ones that wrap from Saturday to Sunday.
	for _, shift := range []int{-week, 0, week} {
		if start(a) < end(b)+shift && start(b)+shift < end(a) {
			return true
		}
	}
	return false
}
