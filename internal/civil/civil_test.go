package civil

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParse(t *testing.T) {
	for _, s := range []string{"2026-10-31", "2028-02-29", "0001-01-01"} {
		d, err := ParseDate(s)
		if err != nil || d.String() != s || !d.IsValid() {
			t.Errorf("ParseDate(%q) = %v, %v", s, d, err)
		}
	}
	for _, s := range []string{"", "2026-02-29", "2026-02-31", "2026-13-01", "2026-1-5", "31/10/2026", "2026-10-31T10:00"} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q): want an error", s)
		}
	}

	for s, want := range map[string]string{"09:30": "09:30", "00:00": "00:00", "23:59": "23:59", "09:30:00": "09:30"} {
		if got, err := ParseTime(s); err != nil || got.String() != want {
			t.Errorf("ParseTime(%q) = %v, %v; want %s", s, got, err, want)
		}
	}
	for _, s := range []string{"", "9:30", "09:5", "24:00", "12:60", "09:30:15", "09h30", "-1:00", "09:30:0", "+9:30"} {
		if _, err := ParseTime(s); err == nil {
			t.Errorf("ParseTime(%q): want an error", s)
		}
	}

	if dt, err := ParseDateTime("2026-11-05T10:00"); err != nil || dt != NewDate(2026, time.November, 5).At(NewTime(10, 0)) {
		t.Errorf("ParseDateTime = %v, %v", dt, err)
	}
	for _, s := range []string{"2026-11-05", "2026-11-05 10:00", "2026-11-05T10:00Z", "2026-11-31T10:00", "2026-11-05T25:00"} {
		if _, err := ParseDateTime(s); err == nil {
			t.Errorf("ParseDateTime(%q): want an error", s)
		}
	}
}

func TestZeroAndInvalid(t *testing.T) {
	if !(Date{}).IsZero() || (Date{}).IsValid() || !(Time{}).IsZero() || !(DateTime{}).IsZero() {
		t.Error("the zero values are none, and not valid")
	}
	// Midnight is a time, not the zero value.
	if midnight := NewTime(0, 0); midnight.IsZero() || !midnight.IsValid() || midnight.String() != "00:00" {
		t.Errorf("NewTime(0, 0) = %v, valid %v", midnight, midnight.IsValid())
	}
	for _, d := range []Date{NewDate(2026, time.February, 30), NewDate(2026, 13, 1)} {
		if d.IsZero() || d.IsValid() {
			t.Errorf("%v: want an invalid date that is not zero", d)
		}
	}
	if tm := NewTime(24, 0); tm.IsZero() || tm.IsValid() {
		t.Error("24:00 is not a time of the day")
	}
}

func TestArithmetic(t *testing.T) {
	d := NewDate(2026, time.December, 30)
	if got := d.AddDays(3); got != NewDate(2027, time.January, 2) {
		t.Errorf("AddDays(3) = %v", got)
	}
	if got := d.AddDays(-365); got != NewDate(2025, time.December, 30) {
		t.Errorf("AddDays(-365) = %v", got)
	}
	if got := NewDate(2027, time.January, 2).DaysSince(d); got != 3 {
		t.Errorf("DaysSince = %d, want 3", got)
	}
	if got := d.Weekday(); got != time.Wednesday {
		t.Errorf("Weekday = %v, want Wednesday", got)
	}
	if !d.Before(d.AddDays(1)) || d.After(d.AddDays(1)) || d.Compare(d) != 0 {
		t.Error("Date comparisons do not follow the calendar")
	}
	if !NewTime(9, 30).Before(NewTime(10, 0)) || NewTime(1, 30).Minutes() != 90 {
		t.Error("Time comparisons are wrong")
	}
	if !d.At(NewTime(23, 0)).Before(d.AddDays(1).At(NewTime(0, 0))) {
		t.Error("DateTime comparisons are wrong")
	}
	if d.Year() != 2026 || d.Month() != time.December || d.Day() != 30 {
		t.Errorf("accessors of %v", d)
	}
}

func TestJSON(t *testing.T) {
	type payload struct {
		On   Date     `json:"on"`
		At   Time     `json:"at"`
		When DateTime `json:"when"`
		Opt  Date     `json:"opt"`
	}
	in := `{"on":"2026-10-31","at":"09:30","when":"2026-11-05T10:00","opt":null}`
	var p payload
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatal(err)
	}
	if p.On != NewDate(2026, time.October, 31) || p.At != NewTime(9, 30) || p.When.String() != "2026-11-05T10:00" || !p.Opt.IsZero() {
		t.Fatalf("decoded %+v", p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("JSON = %s, want %s", out, in)
	}

	// Missing fields and null are the zero value, and go back as null.
	p = payload{}
	if err := json.Unmarshal([]byte(`{"at":null}`), &p); err != nil || !p.At.IsZero() || !p.On.IsZero() {
		t.Errorf("missing fields: %+v, %v", p, err)
	}
	if out, _ := json.Marshal(payload{}); string(out) != `{"on":null,"at":null,"when":null,"opt":null}` {
		t.Errorf("JSON of the zero values = %s", out)
	}

	// Another format does not fail: the value arrives invalid, for the validation to name the field.
	for _, body := range []string{
		`{"on":"31/10/2026","at":"9:30","when":"2026-11-05 10:00","opt":20261031}`,
		`{"on":true,"at":"24:00","when":"2026-11-05T10:00:30","opt":"2026-02-30"}`,
	} {
		var p payload
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		for name, v := range map[string]interface {
			IsZero() bool
			IsValid() bool
		}{"on": p.On, "at": p.At, "when": p.When, "opt": p.Opt} {
			if v.IsZero() || v.IsValid() {
				t.Errorf("%s: %s should be invalid and not zero", body, name)
			}
		}
	}
	if msg := (Date{}).FormatMessage(); msg.Key != "validation.date" {
		t.Errorf("FormatMessage = %v", msg)
	}
}

func TestIn(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	day := func(m time.Month, d, h, min int) DateTime { return NewDate(2026, m, d).At(NewTime(h, min)) }
	for name, tt := range map[string]struct {
		dt   DateTime
		want string
	}{
		"a normal day":     {day(time.October, 31, 10, 0), "2026-10-31T14:00:00Z"},
		"winter":           {day(time.December, 1, 10, 0), "2026-12-01T15:00:00Z"},
		"does not exist":   {day(time.March, 8, 2, 30), "2026-03-08T07:30:00Z"}, // 03:30 EDT
		"after the jump":   {day(time.March, 8, 3, 0), "2026-03-08T07:00:00Z"},
		"before the jump":  {day(time.March, 8, 1, 59), "2026-03-08T06:59:00Z"},
		"happens twice":    {day(time.November, 1, 1, 30), "2026-11-01T05:30:00Z"}, // the first: EDT
		"after the repeat": {day(time.November, 1, 2, 0), "2026-11-01T07:00:00Z"},
	} {
		got := tt.dt.In(ny)
		if got.UTC().Format(time.RFC3339) != tt.want {
			t.Errorf("%s: %v.In(New York) = %s, want %s", name, tt.dt, got.UTC().Format(time.RFC3339), tt.want)
		}
		if got.Location() != ny {
			t.Errorf("%s: location = %v, want New York", name, got.Location())
		}
	}

	// Mexico City has no daylight saving time since 2022: always UTC-6.
	mx := mustLoad(t, "America/Mexico_City")
	for _, d := range []Date{NewDate(2026, time.April, 5), NewDate(2026, time.October, 25)} {
		if got := d.At(NewTime(9, 0)).In(mx).UTC().Hour(); got != 15 {
			t.Errorf("%v 09:00 in Mexico City = %d:00 UTC, want 15:00", d, got)
		}
	}
}

func TestStartAndDayRange(t *testing.T) {
	// In Santiago de Chile the clocks jump from 00:00 to 01:00 on September 6, 2026: the day starts
	// at 01:00.
	santiago := mustLoad(t, "America/Santiago")
	start := NewDate(2026, time.September, 6).Start(santiago)
	if got := start.Format("2006-01-02 15:04"); got != "2026-09-06 01:00" {
		t.Errorf("start of the day in Santiago = %s, want 2026-09-06 01:00", got)
	}

	ny := mustLoad(t, "America/New_York")
	for d, hours := range map[Date]float64{
		NewDate(2026, time.March, 8):    23,
		NewDate(2026, time.November, 1): 25,
		NewDate(2026, time.October, 31): 24,
	} {
		start, end := DayRange(d, d, ny)
		if got := end.Sub(start).Hours(); got != hours {
			t.Errorf("%v lasts %v hours in New York, want %v", d, got, hours)
		}
	}

	start, end := DayRange(NewDate(2026, time.October, 1), NewDate(2026, time.October, 31), mustLoad(t, "America/Bogota"))
	if start.UTC().Format(time.RFC3339) != "2026-10-01T05:00:00Z" || end.UTC().Format(time.RFC3339) != "2026-11-01T05:00:00Z" {
		t.Errorf("October in Bogotá = [%s, %s)", start.UTC(), end.UTC())
	}

	// From an instant to the day of someone: 02:00 UTC on the 31st is still the 30th in Mexico City.
	instant := time.Date(2026, time.October, 31, 2, 0, 0, 0, time.UTC)
	if got := DateOf(instant.In(mustLoad(t, "America/Mexico_City"))); got != NewDate(2026, time.October, 30) {
		t.Errorf("DateOf = %v, want 2026-10-30", got)
	}
}

func TestLoadLocation(t *testing.T) {
	for _, name := range []string{"UTC", "America/Mexico_City", "America/Argentina/Buenos_Aires", "America/Buenos_Aires", "America/Panama"} {
		if _, err := LoadLocation(name); err != nil {
			t.Errorf("LoadLocation(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "Local", "Mars/Base", "america/mexico_city", "../etc/passwd", "-06:00", "America/" + strings.Repeat("x", 64)} {
		if _, err := LoadLocation(name); err == nil {
			t.Errorf("LoadLocation(%q): want an error", name)
		}
	}
}
