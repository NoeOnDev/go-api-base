package branches

import (
	"testing"
	"time"

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

func hours(opens, closes string) Hours {
	o, _ := civil.ParseTime(opens)
	c, _ := civil.ParseTime(closes)
	return Hours{OpensAt: o, ClosesAt: c}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func utc(v time.Time) string { return v.UTC().Format(time.RFC3339) }

// A clinic in Bogotá (UTC-5, no daylight saving time): Monday to Friday 9:00-13:00 and 15:00-19:00,
// Saturday 9:00-13:00.
func clinic() []WeeklyHours {
	var w []WeeklyHours
	for d := time.Monday; d <= time.Friday; d++ {
		w = append(w, WeeklyHours{d, hours("09:00", "13:00")}, WeeklyHours{d, hours("15:00", "19:00")})
	}
	return append(w, WeeklyHours{time.Saturday, hours("09:00", "13:00")})
}

func TestDays(t *testing.T) {
	bogota := load(t, "America/Bogota")
	holiday := Exception{Date: civil.NewDate(2026, time.November, 2), Closed: true, Note: "All Saints"}
	short := Exception{Date: civil.NewDate(2026, time.November, 3), Hours: &Hours{OpensAt: civil.NewTime(10, 0), ClosesAt: civil.NewTime(14, 0)}}

	days := Days(clinic(), []Exception{holiday, short}, civil.NewDate(2026, time.October, 31), civil.NewDate(2026, time.November, 4), bogota)
	if len(days) != 5 {
		t.Fatalf("%d days, want 5", len(days))
	}
	want := map[string][]string{
		"2026-10-31": {"2026-10-31T14:00:00Z 2026-10-31T18:00:00Z"},                                              // Saturday
		"2026-11-01": {},                                                                                         // Sunday
		"2026-11-02": {},                                                                                         // the holiday
		"2026-11-03": {"2026-11-03T15:00:00Z 2026-11-03T19:00:00Z"},                                              // other hours
		"2026-11-04": {"2026-11-04T14:00:00Z 2026-11-04T18:00:00Z", "2026-11-04T20:00:00Z 2026-11-05T00:00:00Z"}, // split
	}
	for _, d := range days {
		var got []string
		for _, o := range d.Openings {
			got = append(got, utc(o.StartsAt)+" "+utc(o.EndsAt))
		}
		if len(got) != len(want[d.Date.String()]) {
			t.Errorf("%s: openings %v, want %v", d.Date, got, want[d.Date.String()])
			continue
		}
		for i := range got {
			if got[i] != want[d.Date.String()][i] {
				t.Errorf("%s: openings %v, want %v", d.Date, got, want[d.Date.String()])
			}
		}
	}
	if !days[2].Exception || days[2].Note != "All Saints" || days[0].Exception {
		t.Errorf("exceptions not marked: %+v, %+v", days[0], days[2])
	}
}

func TestDaysAcrossDaylightSavingTime(t *testing.T) {
	ny := load(t, "America/New_York")
	daily := []WeeklyHours{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		daily = append(daily, WeeklyHours{d, hours("09:00", "17:00")})
	}
	// 9:00 is 13:00 UTC in the summer and 14:00 UTC in the winter: the hours follow the clock.
	days := Days(daily, nil, civil.NewDate(2026, time.October, 31), civil.NewDate(2026, time.November, 1), ny)
	if got := utc(days[0].Openings[0].StartsAt); got != "2026-10-31T13:00:00Z" {
		t.Errorf("Saturday opens at %s, want 13:00 UTC", got)
	}
	if got := utc(days[1].Openings[0].StartsAt); got != "2026-11-01T14:00:00Z" {
		t.Errorf("Sunday opens at %s, want 14:00 UTC", got)
	}

	// A night that crosses the change lasts one hour less: 22:00 to 06:00 is 7 real hours.
	night := []WeeklyHours{{time.Saturday, hours("22:00", "06:00")}}
	o := Days(night, nil, civil.NewDate(2026, time.March, 7), civil.NewDate(2026, time.March, 7), ny)[0].Openings[0]
	if got := o.EndsAt.Sub(o.StartsAt); got != 7*time.Hour {
		t.Errorf("the night of the jump lasts %v, want 7h", got)
	}

	// The hour that does not exist moves forward as long as the jump; a range left empty does not
	// open.
	jump := civil.NewDate(2026, time.March, 8)
	gap := Days([]WeeklyHours{{time.Sunday, hours("02:00", "02:30")}}, nil, jump, jump, ny)[0].Openings
	if len(gap) != 1 || utc(gap[0].StartsAt) != "2026-03-08T07:00:00Z" || utc(gap[0].EndsAt) != "2026-03-08T07:30:00Z" {
		t.Errorf("02:00-02:30 on the jump = %+v, want 03:00-03:30 EDT", gap)
	}
	if got := Days([]WeeklyHours{{time.Sunday, hours("02:15", "03:00")}}, nil, jump, jump, ny)[0].Openings; len(got) != 0 {
		t.Errorf("02:15-03:00 on the jump opens: %+v", got)
	}
}

func TestStatusAt(t *testing.T) {
	bogota := load(t, "America/Bogota")
	weekly := clinic()

	for name, tt := range map[string]struct {
		now                   string
		open                  bool
		closesAt, nextOpensAt string
	}{
		"in the morning":   {"2026-11-04T15:00:00Z", true, "2026-11-04T18:00:00Z", "2026-11-04T20:00:00Z"},
		"at lunch":         {"2026-11-04T19:00:00Z", false, "", "2026-11-04T20:00:00Z"},
		"at closing time":  {"2026-11-05T00:00:00Z", false, "", "2026-11-05T14:00:00Z"},
		"on Saturday noon": {"2026-11-07T17:30:00Z", true, "2026-11-07T18:00:00Z", "2026-11-09T14:00:00Z"},
		"on Sunday":        {"2026-11-08T15:00:00Z", false, "", "2026-11-09T14:00:00Z"},
	} {
		s := StatusAt(at(t, tt.now), weekly, nil, bogota)
		if s.Open != tt.open || timeOrEmpty(s.ClosesAt) != tt.closesAt || timeOrEmpty(s.NextOpensAt) != tt.nextOpensAt {
			t.Errorf("%s: %v, %s, %s; want %v, %s, %s", name, s.Open, timeOrEmpty(s.ClosesAt), timeOrEmpty(s.NextOpensAt), tt.open, tt.closesAt, tt.nextOpensAt)
		}
	}

	// A bar from 18:00 to 02:00, and from 00:00 to 02:00 on Sunday: after midnight it is still open,
	// with the range of the day before; two ranges that touch close at the end of the second one.
	bar := []WeeklyHours{{time.Friday, hours("18:00", "00:00")}, {time.Saturday, hours("00:00", "02:00")}}
	s := StatusAt(at(t, "2026-11-07T06:30:00Z"), bar, nil, bogota) // Saturday 01:30 in Bogotá
	if !s.Open || timeOrEmpty(s.ClosesAt) != "2026-11-07T07:00:00Z" {
		t.Errorf("after midnight: %v until %s, want open until 02:00 (07:00 UTC)", s.Open, timeOrEmpty(s.ClosesAt))
	}

	// Without hours, it never opens.
	if s := StatusAt(time.Now(), nil, nil, bogota); s.Open || s.NextOpensAt != nil {
		t.Errorf("without hours: %+v", s)
	}
}

func timeOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return utc(*t)
}

func TestOverlaps(t *testing.T) {
	for name, tt := range map[string]struct {
		a, b WeeklyHours
		want bool
	}{
		"same day, apart":          {WeeklyHours{time.Monday, hours("09:00", "13:00")}, WeeklyHours{time.Monday, hours("15:00", "19:00")}, false},
		"same day, touching":       {WeeklyHours{time.Monday, hours("09:00", "13:00")}, WeeklyHours{time.Monday, hours("13:00", "19:00")}, false},
		"same day, overlapping":    {WeeklyHours{time.Monday, hours("09:00", "14:00")}, WeeklyHours{time.Monday, hours("13:00", "19:00")}, true},
		"other day":                {WeeklyHours{time.Monday, hours("09:00", "14:00")}, WeeklyHours{time.Tuesday, hours("09:00", "14:00")}, false},
		"crossing into the next":   {WeeklyHours{time.Monday, hours("22:00", "03:00")}, WeeklyHours{time.Tuesday, hours("02:00", "05:00")}, true},
		"crossing, then touching":  {WeeklyHours{time.Monday, hours("22:00", "03:00")}, WeeklyHours{time.Tuesday, hours("03:00", "05:00")}, false},
		"Saturday night to Sunday": {WeeklyHours{time.Saturday, hours("22:00", "03:00")}, WeeklyHours{time.Sunday, hours("01:00", "05:00")}, true},
	} {
		if got := overlaps(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: overlaps = %v, want %v", name, got, tt.want)
		}
		if got := overlaps(tt.b, tt.a); got != tt.want {
			t.Errorf("%s (reversed): overlaps = %v, want %v", name, got, tt.want)
		}
	}
}
