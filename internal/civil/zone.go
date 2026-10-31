package civil

import (
	"fmt"
	"time"
)

// maxZoneNameLen is the longest time zone name accepted. The IANA ones are much shorter
// (America/Argentina/ComodRivadavia, the longest, has 32 characters).
const maxZoneNameLen = 64

// LoadLocation loads an IANA time zone, like "America/Mexico_City" or "UTC". Unlike
// time.LoadLocation, it rejects "" and "Local", which are not zones but "UTC" and "the server's".
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" || len(name) > maxZoneNameLen {
		return nil, fmt.Errorf("civil: %q is not a time zone like America/Mexico_City", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("civil: %q is not a time zone like America/Mexico_City", name)
	}
	return loc, nil
}

// In returns the instant at which the wall clock of loc shows dt, which must be valid.
//
// When the clocks change, an hour can be missing or happen twice. The rule is the one of
// JavaScript's Temporal and Java's java.time:
//   - An hour that does not exist (it fell in the spring jump: in New York, 02:30 on March 8,
//     2026) moves forward as long as the jump lasts: 03:30.
//   - An hour that happens twice (in the autumn: 01:30 on November 1, 2026 in New York) is the
//     first time.
func (dt DateTime) In(loc *time.Location) time.Time {
	// The wall clock as if it were UTC. The instant is that minus the zone's offset, which is the
	// one before or the one after the change, if there is one that day.
	wall := dt.wallUTC()
	_, before := wall.Add(-24 * time.Hour).In(loc).Zone()
	_, after := wall.Add(24 * time.Hour).In(loc).Zone()
	early := wall.Add(-time.Duration(before) * time.Second).In(loc)
	late := wall.Add(-time.Duration(after) * time.Second).In(loc)

	earlyOK, lateOK := DateTimeOf(early) == dt, DateTimeOf(late) == dt
	switch {
	case earlyOK && lateOK:
		// It happens twice (or there is no change): the first one.
		if late.Before(early) {
			return late
		}
		return early
	case earlyOK:
		return early
	case lateOK:
		return late
	}
	// It does not exist: with the offset before the jump, the hour lands after it, moved forward
	// as much as the jump.
	return early
}

// Start returns the first instant of the day d in loc. It is 00:00, except on the days when the
// clocks jump forward at midnight (in Santiago de Chile, for example), when it is 01:00.
func (d Date) Start(loc *time.Location) time.Time {
	return d.At(NewTime(0, 0)).In(loc)
}

// DayRange returns the instants [start, end) of the days from from to to (both included) in loc,
// to filter a timestamptz column by days of someone: created_at >= start AND created_at < end.
// That way the filter uses the column's index. A day can last 23 or 25 hours, so the end is the
// start of the day after to, never start plus 24 hours.
func DayRange(from, to Date, loc *time.Location) (start, end time.Time) {
	return from.Start(loc), to.AddDays(1).Start(loc)
}

// wallUTC is the wall clock of dt as if it were UTC.
func (dt DateTime) wallUTC() time.Time {
	return time.Date(dt.date.year, dt.date.month, dt.date.day, dt.time.hour, dt.time.minute, 0, 0, time.UTC)
}
