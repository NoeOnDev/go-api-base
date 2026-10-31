// Package civil has dates and times without a time zone: what a calendar or a wall clock says,
// like "October 31", "9:30" or "November 5 at 10:00". They are the other half of a time.Time,
// which is a moment (an instant) that every zone sees at a different hour.
//
//   - Date ("2026-10-31", a date column): a birthday, a holiday, the day of an invoice. It is
//     never converted to a zone: the birthday is the same day everywhere.
//   - Time ("09:30", a time column): the opening hour of a branch, the start of a shift. It is
//     precise to the minute.
//   - DateTime ("2026-11-05T10:00", a timestamp column without a zone): an appointment at the
//     place's hour. With its zone (In), it becomes the instant.
//
// The zero value of each one is "none": it goes to JSON as null and to PostgreSQL as NULL. A value
// that comes in a request body in another format is not a decoding error: it arrives invalid
// (IsValid is false), so the resource's validation answers 422 on its field, like any other rule
// (see validate.Civil). See docs/time.md.
package civil

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// state tells "none" (the zero value) apart from a value and from one that came in a wrong format.
type state uint8

const (
	none state = iota
	set
	malformed
)

// Date is a calendar day, without a time or a zone. Build it with NewDate, ParseDate or DateOf.
type Date struct {
	year  int
	month time.Month
	day   int
	state state
}

// Time is an hour of the day, from 00:00 to 23:59, without a date or a zone. Build it with NewTime,
// ParseTime or TimeOf. Its zero value is "no time", not midnight: NewTime(0, 0) is midnight.
type Time struct {
	hour   int
	minute int
	state  state
}

// DateTime is a day and an hour of the wall clock, without a zone. Build it with Date.At or
// ParseDateTime.
type DateTime struct {
	date Date
	time Time
}

// NewDate returns the date year-month-day. If that day does not exist (February 30), the date is
// not valid (see IsValid).
func NewDate(year int, month time.Month, day int) Date {
	d := Date{year: year, month: month, day: day, state: set}
	if DateOf(d.midnightUTC()) != d {
		return Date{state: malformed}
	}
	return d
}

// NewTime returns the hour hour:minute. Out of 00:00-23:59, it is not valid (see IsValid).
func NewTime(hour, minute int) Time {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return Time{state: malformed}
	}
	return Time{hour: hour, minute: minute, state: set}
}

// DateOf returns the day of t in t's zone: civil.DateOf(t.In(loc)) is the day of t in loc.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{year: y, month: m, day: d, state: set}
}

// TimeOf returns the hour and minute of t in t's zone (the seconds are dropped).
func TimeOf(t time.Time) Time {
	return Time{hour: t.Hour(), minute: t.Minute(), state: set}
}

// DateTimeOf returns the wall clock of t in t's zone (the seconds are dropped).
func DateTimeOf(t time.Time) DateTime {
	return DateTime{date: DateOf(t), time: TimeOf(t)}
}

// ParseDate reads a date in the format 2006-01-02.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("civil: %q is not a date like 2026-10-31", s)
	}
	return DateOf(t), nil
}

// ParseTime reads an hour in the format 15:04 (24 hours, two digits each). It also accepts
// 15:04:00, which is how PostgreSQL writes it, but not other seconds.
func ParseTime(s string) (Time, error) {
	invalid := fmt.Errorf("civil: %q is not a time like 09:30", s)
	if len(s) == 8 {
		if s[5:] != ":00" {
			return Time{}, invalid
		}
		s = s[:5]
	}
	if len(s) != 5 || s[2] != ':' || !digits(s[:2]) || !digits(s[3:]) {
		return Time{}, invalid
	}
	t := NewTime(int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0'))
	if !t.IsValid() {
		return Time{}, invalid
	}
	return t, nil
}

func digits(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParseDateTime reads a date and an hour in the format 2006-01-02T15:04 (or with :00 seconds).
func ParseDateTime(s string) (DateTime, error) {
	invalid := fmt.Errorf("civil: %q is not a date and time like 2026-11-05T10:00", s)
	if len(s) < 11 || s[10] != 'T' {
		return DateTime{}, invalid
	}
	d, err := ParseDate(s[:10])
	if err != nil {
		return DateTime{}, invalid
	}
	t, err := ParseTime(s[11:])
	if err != nil {
		return DateTime{}, invalid
	}
	return d.At(t), nil
}

// Year returns the year of d.
func (d Date) Year() int { return d.year }

// Month returns the month of d.
func (d Date) Month() time.Month { return d.month }

// Day returns the day of the month of d.
func (d Date) Day() int { return d.day }

// Hour returns the hour of t, from 0 to 23.
func (t Time) Hour() int { return t.hour }

// Minute returns the minute of t, from 0 to 59.
func (t Time) Minute() int { return t.minute }

// Date returns the date of dt.
func (dt DateTime) Date() Date { return dt.date }

// Time returns the hour of dt.
func (dt DateTime) Time() Time { return dt.time }

// String returns the date as 2006-01-02 ("" if it is not valid).
func (d Date) String() string {
	if !d.IsValid() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.year, d.month, d.day)
}

// String returns the hour as 15:04 ("" if it is not valid).
func (t Time) String() string {
	if !t.IsValid() {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", t.hour, t.minute)
}

// String returns the date and hour as 2006-01-02T15:04 ("" if it is not valid).
func (dt DateTime) String() string {
	if !dt.IsValid() {
		return ""
	}
	return dt.date.String() + "T" + dt.time.String()
}

// IsZero reports whether d is the zero value: no date.
func (d Date) IsZero() bool { return d.state == none }

// IsZero reports whether t is the zero value: no time.
func (t Time) IsZero() bool { return t.state == none }

// IsZero reports whether dt is the zero value: no date and time.
func (dt DateTime) IsZero() bool { return dt.date.IsZero() && dt.time.IsZero() }

// IsValid reports whether d is a day that exists. The zero value and a value that came in a wrong
// format are not.
func (d Date) IsValid() bool { return d.state == set }

// IsValid reports whether t is an hour of the day. The zero value and a value that came in a wrong
// format are not.
func (t Time) IsValid() bool { return t.state == set }

// IsValid reports whether both the date and the hour exist.
func (dt DateTime) IsValid() bool { return dt.date.IsValid() && dt.time.IsValid() }

// midnightUTC is d at 00:00 in UTC, to use the calendar arithmetic of the time package (UTC has
// no gaps).
func (d Date) midnightUTC() time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, time.UTC)
}

// AddDays returns the date n days later (or earlier, if n is negative).
func (d Date) AddDays(n int) Date {
	return DateOf(d.midnightUTC().AddDate(0, 0, n))
}

// DaysSince returns how many days there are from u to d: 1 if d is the day after u.
func (d Date) DaysSince(u Date) int {
	return int(d.midnightUTC().Sub(u.midnightUTC()) / (24 * time.Hour))
}

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday {
	return d.midnightUTC().Weekday()
}

// At returns the date d at the hour t.
func (d Date) At(t Time) DateTime {
	return DateTime{date: d, time: t}
}

// Compare returns -1, 0 or +1 if d is before, the same day as or after u.
func (d Date) Compare(u Date) int {
	return d.midnightUTC().Compare(u.midnightUTC())
}

// Before reports whether d is before u.
func (d Date) Before(u Date) bool { return d.Compare(u) < 0 }

// After reports whether d is after u.
func (d Date) After(u Date) bool { return d.Compare(u) > 0 }

// Minutes returns the minutes since midnight: 90 for 01:30.
func (t Time) Minutes() int { return t.hour*60 + t.minute }

// Compare returns -1, 0 or +1 if t is before, the same as or after u.
func (t Time) Compare(u Time) int {
	switch {
	case t.Minutes() < u.Minutes():
		return -1
	case t.Minutes() > u.Minutes():
		return 1
	}
	return 0
}

// Before reports whether t is before u.
func (t Time) Before(u Time) bool { return t.Compare(u) < 0 }

// After reports whether t is after u.
func (t Time) After(u Time) bool { return t.Compare(u) > 0 }

// Compare returns -1, 0 or +1 if dt is before, the same as or after u.
func (dt DateTime) Compare(u DateTime) int {
	if c := dt.date.Compare(u.date); c != 0 {
		return c
	}
	return dt.time.Compare(u.time)
}

// Before reports whether dt is before u.
func (dt DateTime) Before(u DateTime) bool { return dt.Compare(u) < 0 }

// After reports whether dt is after u.
func (dt DateTime) After(u DateTime) bool { return dt.Compare(u) > 0 }

// FormatMessage is the validation message of a date in a wrong format (see validate.Civil).
func (Date) FormatMessage() i18n.Message { return i18n.M("validation.date") }

// FormatMessage is the validation message of an hour in a wrong format (see validate.Civil).
func (Time) FormatMessage() i18n.Message { return i18n.M("validation.time") }

// FormatMessage is the validation message of a date and time in a wrong format (see validate.Civil).
func (DateTime) FormatMessage() i18n.Message { return i18n.M("validation.datetime") }

// MarshalJSON writes the date as "2006-01-02", or null if it has none.
func (d Date) MarshalJSON() ([]byte, error) {
	return marshal(d.IsValid(), d.String())
}

// UnmarshalJSON reads "2006-01-02". null is the zero value; another format leaves the date
// invalid, for the validation to reject it.
func (d *Date) UnmarshalJSON(b []byte) error {
	*d = unmarshal(b, ParseDate, Date{state: malformed})
	return nil
}

// MarshalJSON writes the hour as "15:04", or null if it has none.
func (t Time) MarshalJSON() ([]byte, error) {
	return marshal(t.IsValid(), t.String())
}

// UnmarshalJSON reads "15:04". null is the zero value; another format leaves the hour invalid, for
// the validation to reject it.
func (t *Time) UnmarshalJSON(b []byte) error {
	*t = unmarshal(b, ParseTime, Time{state: malformed})
	return nil
}

// MarshalJSON writes the date and hour as "2006-01-02T15:04", or null if it has none.
func (dt DateTime) MarshalJSON() ([]byte, error) {
	return marshal(dt.IsValid(), dt.String())
}

// UnmarshalJSON reads "2006-01-02T15:04". null is the zero value; another format leaves the value
// invalid, for the validation to reject it.
func (dt *DateTime) UnmarshalJSON(b []byte) error {
	*dt = unmarshal(b, ParseDateTime, DateTime{date: Date{state: malformed}})
	return nil
}

func marshal(valid bool, s string) ([]byte, error) {
	if !valid {
		return []byte("null"), nil
	}
	return json.Marshal(s)
}

// unmarshal reads a JSON string with parse. It never fails: encoding/json does not say which field
// a method's error belongs to, so the value arrives as bad and the resource's validation names the
// field (see validate.Civil).
func unmarshal[T any](b []byte, parse func(string) (T, error), bad T) T {
	var zero T
	if string(b) == "null" {
		return zero
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return bad
	}
	v, err := parse(s)
	if err != nil {
		return bad
	}
	return v
}
