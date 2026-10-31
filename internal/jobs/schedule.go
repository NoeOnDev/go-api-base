package jobs

import (
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

// DailyAt is the schedule of a periodic job that runs every day at hour:minute of the wall clock
// in loc: jobs.DailyAt(8, 0, deps.TimeZone). It is Laravel's ->dailyAt('08:00')->timezone(…).
//
// It follows the clock, not a fixed interval: after a daylight saving time change, 08:00 is still
// 08:00 in loc. An hour that does not exist that day (the spring jump) runs when the clock jumps,
// and one that happens twice (the autumn) runs only the first time (see civil.DateTime.In).
// It panics with an hour out of 00:00-23:59: it is a mistake in the code.
func DailyAt(hour, minute int, loc *time.Location) river.PeriodicSchedule {
	return localSchedule{at: mustTime("DailyAt", hour, minute), loc: loc}
}

// WeeklyAt is the schedule of a periodic job that runs every week on day at hour:minute of the
// wall clock in loc: jobs.WeeklyAt(time.Monday, 9, 0, deps.TimeZone). Like DailyAt, it follows
// the clock.
func WeeklyAt(day time.Weekday, hour, minute int, loc *time.Location) river.PeriodicSchedule {
	return localSchedule{at: mustTime("WeeklyAt", hour, minute), loc: loc, weekly: true, weekday: day}
}

func mustTime(name string, hour, minute int) civil.Time {
	t := civil.NewTime(hour, minute)
	if !t.IsValid() {
		panic(fmt.Sprintf("jobs.%s: %02d:%02d is not a time of the day", name, hour, minute))
	}
	return t
}

// localSchedule implements river.PeriodicSchedule for an hour of the wall clock.
type localSchedule struct {
	at      civil.Time
	loc     *time.Location
	weekly  bool
	weekday time.Weekday
}

// Next returns the first time after current at which the clock of loc shows the hour (and, if it is
// weekly, the day).
func (s localSchedule) Next(current time.Time) time.Time {
	day := civil.DateOf(current.In(s.loc))
	// Today may have already passed: at most a week (and one day) ahead.
	for i := range 9 {
		d := day.AddDays(i)
		if s.weekly && d.Weekday() != s.weekday {
			continue
		}
		if next := d.At(s.at).In(s.loc); next.After(current) {
			return next
		}
	}
	panic("jobs: no next run within a week") // unreachable: some day of the next 8 matches
}
