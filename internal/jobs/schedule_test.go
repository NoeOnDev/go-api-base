package jobs

import (
	"testing"
	"time"
)

func TestDailyAt(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	utc := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	eight := DailyAt(8, 0, ny)
	for name, tt := range map[string]struct{ current, want string }{
		"before the hour, today":      {"2026-10-30T11:00:00Z", "2026-10-30T12:00:00Z"}, // 07:00 → 08:00 EDT
		"at the hour, tomorrow":       {"2026-10-30T12:00:00Z", "2026-10-31T12:00:00Z"},
		"after the hour, tomorrow":    {"2026-10-30T15:00:00Z", "2026-10-31T12:00:00Z"},
		"the clocks go back at night": {"2026-10-31T12:00:00Z", "2026-11-01T13:00:00Z"}, // 08:00 EST
		"the clocks jump at night":    {"2026-03-07T13:00:00Z", "2026-03-08T12:00:00Z"}, // 08:00 EST → 08:00 EDT
	} {
		if got := eight.Next(utc(tt.current)); !got.Equal(utc(tt.want)) {
			t.Errorf("%s: Next(%s) = %s, want %s", name, tt.current, got.UTC().Format(time.RFC3339), tt.want)
		}
	}

	// An hour that does not exist runs when the clock jumps; one that happens twice, only once.
	if got := DailyAt(2, 30, ny).Next(utc("2026-03-07T12:00:00Z")); !got.Equal(utc("2026-03-08T07:30:00Z")) {
		t.Errorf("02:30 on the jump = %s, want 03:30 EDT", got.UTC())
	}
	repeated := DailyAt(1, 30, ny)
	first := repeated.Next(utc("2026-10-31T12:00:00Z"))
	if !first.Equal(utc("2026-11-01T05:30:00Z")) {
		t.Errorf("01:30 on the repeat = %s, want the first one (EDT)", first.UTC())
	}
	if got := repeated.Next(first); !got.Equal(utc("2026-11-02T06:30:00Z")) {
		t.Errorf("after the first 01:30 = %s, want the next day", got.UTC())
	}

	// Mexico City has no daylight saving time: always 14:00 UTC.
	mx, _ := time.LoadLocation("America/Mexico_City")
	for _, current := range []string{"2026-04-04T20:00:00Z", "2026-10-24T20:00:00Z"} {
		if got := DailyAt(8, 0, mx).Next(utc(current)); got.UTC().Hour() != 14 {
			t.Errorf("08:00 in Mexico City = %s", got.UTC())
		}
	}
}

func TestWeeklyAt(t *testing.T) {
	bogota, _ := time.LoadLocation("America/Bogota")
	monday := WeeklyAt(time.Monday, 9, 0, bogota)
	saturday := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	next := monday.Next(saturday)
	if want := time.Date(2026, time.November, 2, 14, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("Next(saturday) = %s, want %s", next.UTC(), want)
	}
	if want := time.Date(2026, time.November, 9, 14, 0, 0, 0, time.UTC); !monday.Next(next).Equal(want) {
		t.Errorf("Next(monday 09:00) = %s, want %s", monday.Next(next).UTC(), want)
	}
}

func TestScheduleRejectsInvalidHours(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want a panic with 24:00")
		}
	}()
	DailyAt(24, 0, time.UTC)
}
