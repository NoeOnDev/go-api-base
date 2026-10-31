package appointments

import (
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/branches"
	"github.com/NoeOnDev/go-api-base/internal/civil"
)

func load(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := civil.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func weekly(day time.Weekday, opens, closes string) branches.WeeklyHours {
	o, _ := civil.ParseTime(opens)
	c, _ := civil.ParseTime(closes)
	return branches.WeeklyHours{Weekday: day, Hours: branches.Hours{OpensAt: o, ClosesAt: c}}
}

func openings(weekly []branches.WeeklyHours, date civil.Date, loc *time.Location) []branches.Opening {
	var o []branches.Opening
	for _, d := range branches.Days(weekly, nil, date.AddDays(-1), date, loc) {
		o = append(o, d.Openings...)
	}
	return o
}

func locals(s []Slot) []string {
	out := make([]string, len(s))
	for i, slot := range s {
		out[i] = slot.LocalStartsAt.Time().String()
	}
	return out
}

func TestSlots(t *testing.T) {
	bogota := load(t, "America/Bogota")
	date := civil.NewDate(2026, time.November, 4) // a Wednesday
	o := openings([]branches.WeeklyHours{weekly(time.Wednesday, "09:00", "11:00")}, date, bogota)
	past := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)

	// Every 15 minutes, and the last one ends at closing time.
	got := locals(slots(o, date, 30*time.Minute, past, nil, bogota))
	want := []string{"09:00", "09:15", "09:30", "09:45", "10:00", "10:15", "10:30"}
	if len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
		t.Errorf("slots = %v, want %v", got, want)
	}

	// A booked 09:30-10:00 takes the slots that overlap it, and the past ones are gone.
	booked := []span{{time.Date(2026, time.November, 4, 14, 30, 0, 0, time.UTC), time.Date(2026, time.November, 4, 15, 0, 0, 0, time.UTC)}}
	if got := locals(slots(o, date, 30*time.Minute, past, booked, bogota)); len(got) != 4 || got[0] != "09:00" || got[1] != "10:00" {
		t.Errorf("with a booking = %v, want 09:00, 10:00, 10:15, 10:30", got)
	}
	now := time.Date(2026, time.November, 4, 15, 10, 0, 0, time.UTC) // 10:10 in Bogotá
	if got := locals(slots(o, date, 30*time.Minute, now, nil, bogota)); len(got) != 2 || got[0] != "10:15" {
		t.Errorf("after 10:10 = %v, want 10:15, 10:30", got)
	}

	// The instants go in UTC.
	s := slots(o, date, time.Hour, past, nil, bogota)[0]
	if s.StartsAt.Location() != time.UTC || s.StartsAt.Format(time.RFC3339) != "2026-11-04T14:00:00Z" || s.EndsAt.Format(time.RFC3339) != "2026-11-04T15:00:00Z" {
		t.Errorf("first slot = %+v", s)
	}
}

func TestSlotsAcrossMidnightAndDaylightSavingTime(t *testing.T) {
	bogota := load(t, "America/Bogota")
	// A bar from 22:00 to 02:00 on Fridays: on Saturday, the slots after midnight.
	bar := []branches.WeeklyHours{weekly(time.Friday, "22:00", "02:00")}
	saturday := civil.NewDate(2026, time.November, 7)
	past := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	if got := locals(slots(openings(bar, saturday, bogota), saturday, time.Hour, past, nil, bogota)); len(got) != 5 || got[0] != "00:00" || got[4] != "01:00" {
		t.Errorf("Saturday after midnight = %v, want 00:00 to 01:00", got)
	}
	if got := locals(slots(openings(bar, saturday.AddDays(-1), bogota), saturday.AddDays(-1), time.Hour, past, nil, bogota)); len(got) != 8 || got[0] != "22:00" || got[7] != "23:45" {
		t.Errorf("Friday night = %v, want 22:00 to 23:45", got)
	}

	// In New York the clocks jump from 02:00 to 03:00 on March 8, 2026: from 01:00 to 04:00 there are
	// two real hours, and no slot at 02:xx.
	ny := load(t, "America/New_York")
	jump := civil.NewDate(2026, time.March, 8)
	before := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	got := locals(slots(openings([]branches.WeeklyHours{weekly(time.Sunday, "01:00", "04:00")}, jump, ny), jump, 30*time.Minute, before, nil, ny))
	want := []string{"01:00", "01:15", "01:30", "01:45", "03:00", "03:15", "03:30"}
	if len(got) != len(want) {
		t.Fatalf("slots on the jump = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("slots on the jump = %v, want %v", got, want)
			break
		}
	}
}
