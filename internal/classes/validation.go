package classes

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// Durations of a class, in minutes.
const (
	minDuration = 5
	maxDuration = 720
)

// classInput is the body to create or replace a class.
type classInput struct {
	Name     string `json:"name"`
	TimeZone string `json:"time_zone"`
	// Weekdays go from 0 (Sunday) to 6 (Saturday).
	Weekdays        []int      `json:"weekdays"`
	StartTime       civil.Time `json:"start_time"`
	DurationMinutes int        `json:"duration_minutes"`
	StartsOn        civil.Date `json:"starts_on"`
	// EndsOn is optional: without it, the class repeats with no end.
	EndsOn civil.Date `json:"ends_on"`
}

func (in *classInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.TimeZone = strings.TrimSpace(in.TimeZone)
	slices.Sort(in.Weekdays)
	in.Weekdays = slices.Compact(in.Weekdays)
}

func (in classInput) validate() error {
	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, 120); !msg.IsZero() {
		fields["name"] = msg
	}
	if msg := validate.TimeZone(in.TimeZone); !msg.IsZero() {
		fields["time_zone"] = msg
	}
	if len(in.Weekdays) == 0 {
		fields["weekdays"] = i18n.M("validation.required")
	}
	for i, d := range in.Weekdays {
		if d < 0 || d > 6 {
			fields[fmt.Sprintf("weekdays.%d", i)] = i18n.M("validation.integer_range", "min", 0, "max", 6)
		}
	}
	if msg := validate.RequiredCivil(in.StartTime); !msg.IsZero() {
		fields["start_time"] = msg
	}
	if in.DurationMinutes < minDuration || in.DurationMinutes > maxDuration {
		fields["duration_minutes"] = i18n.M("validation.integer_range", "min", minDuration, "max", maxDuration)
	}
	if msg := validate.RequiredCivil(in.StartsOn); !msg.IsZero() {
		fields["starts_on"] = msg
	}
	if msg := validate.Civil(in.EndsOn); !msg.IsZero() {
		fields["ends_on"] = msg
	} else if in.EndsOn.IsValid() && in.StartsOn.IsValid() && in.EndsOn.Before(in.StartsOn) {
		fields["ends_on"] = i18n.M("validation.before_start")
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func (in classInput) weekdays() []int16 {
	days := make([]int16, len(in.Weekdays))
	for i, d := range in.Weekdays {
		days[i] = int16(d)
	}
	return days
}

// changeInput is the body of PUT /classes/{id}/sessions/{date}: {canceled: true}, or {starts_at} to
// move it, with an optional note.
type changeInput struct {
	Canceled bool `json:"canceled"`
	// StartsAt is the new hour of the clock of the class: "2026-10-29T19:00" (another day, too).
	StartsAt civil.DateTime `json:"starts_at"`
	Note     string         `json:"note"`
}

func (in *changeInput) normalize() {
	in.Note = strings.TrimSpace(in.Note)
}

func (in changeInput) validate() error {
	fields := httpx.Fields{}
	switch {
	case in.Canceled && !in.StartsAt.IsZero():
		fields["starts_at"] = i18n.M("validation.canceled_without_time")
	case !in.Canceled:
		if msg := validate.RequiredCivil(in.StartsAt); !msg.IsZero() {
			fields["starts_at"] = msg
		}
	}
	if msg := validate.MaxLen(in.Note, 200); !msg.IsZero() {
		fields["note"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// rule turns a stored class into its rule.
func rule(weekdays []int16, start civil.Time, minutes int32, startsOn, endsOn civil.Date) Rule {
	r := Rule{StartTime: start, Duration: time.Duration(minutes) * time.Minute, StartsOn: startsOn, EndsOn: endsOn}
	for _, d := range weekdays {
		r.Weekdays = append(r.Weekdays, time.Weekday(d))
	}
	return r
}
