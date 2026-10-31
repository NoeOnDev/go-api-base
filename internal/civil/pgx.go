package civil

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// The types are read from and written to PostgreSQL with pgx: Date in a date column, Time in a
// time one and DateTime in a timestamp (without a zone) one. sqlc generates them for those columns
// (see sqlc.yaml); a nullable column is a pointer, nil for NULL.

const microsPerMinute = int64(time.Minute / time.Microsecond)

// ScanDate implements pgtype.DateScanner. NULL is the zero value.
func (d *Date) ScanDate(v pgtype.Date) error {
	switch {
	case !v.Valid:
		*d = Date{}
	case v.InfinityModifier != pgtype.Finite:
		return errors.New("civil: cannot read an infinite date")
	default:
		*d = DateOf(v.Time)
	}
	return nil
}

// DateValue implements pgtype.DateValuer. The zero value is NULL.
func (d Date) DateValue() (pgtype.Date, error) {
	if d.IsZero() {
		return pgtype.Date{}, nil
	}
	if !d.IsValid() {
		return pgtype.Date{}, errors.New("civil: cannot store an invalid date; validate it first")
	}
	return pgtype.Date{Time: d.midnightUTC(), Valid: true}, nil
}

// ScanTime implements pgtype.TimeScanner. NULL is the zero value; seconds and 24:00 are rejected.
func (t *Time) ScanTime(v pgtype.Time) error {
	if !v.Valid {
		*t = Time{}
		return nil
	}
	if v.Microseconds%microsPerMinute != 0 {
		return errors.New("civil: the time has seconds; Time is precise to the minute")
	}
	minutes := int(v.Microseconds / microsPerMinute)
	parsed := NewTime(minutes/60, minutes%60)
	if !parsed.IsValid() {
		return fmt.Errorf("civil: %d minutes is not a time of the day", minutes)
	}
	*t = parsed
	return nil
}

// TimeValue implements pgtype.TimeValuer. The zero value is NULL.
func (t Time) TimeValue() (pgtype.Time, error) {
	if t.IsZero() {
		return pgtype.Time{}, nil
	}
	if !t.IsValid() {
		return pgtype.Time{}, errors.New("civil: cannot store an invalid time; validate it first")
	}
	return pgtype.Time{Microseconds: int64(t.Minutes()) * microsPerMinute, Valid: true}, nil
}

// ScanTimestamp implements pgtype.TimestampScanner. NULL is the zero value; seconds are rejected.
func (dt *DateTime) ScanTimestamp(v pgtype.Timestamp) error {
	switch {
	case !v.Valid:
		*dt = DateTime{}
	case v.InfinityModifier != pgtype.Finite:
		return errors.New("civil: cannot read an infinite timestamp")
	case v.Time.Second() != 0 || v.Time.Nanosecond() != 0:
		return errors.New("civil: the timestamp has seconds; DateTime is precise to the minute")
	default:
		*dt = DateTimeOf(v.Time)
	}
	return nil
}

// TimestampValue implements pgtype.TimestampValuer. The zero value is NULL.
func (dt DateTime) TimestampValue() (pgtype.Timestamp, error) {
	if dt.IsZero() {
		return pgtype.Timestamp{}, nil
	}
	if !dt.IsValid() {
		return pgtype.Timestamp{}, errors.New("civil: cannot store an invalid date and time; validate it first")
	}
	return pgtype.Timestamp{Time: dt.wallUTC(), Valid: true}, nil
}
