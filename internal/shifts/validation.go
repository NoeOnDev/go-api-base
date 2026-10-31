package shifts

import (
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// maxShift is the longest shift.
const maxShift = 24 * time.Hour

// shiftInput is the body to create or replace a shift.
type shiftInput struct {
	UserID int64 `json:"user_id"`
	// StartsAt and EndsAt are hours of the clock of the place: "2026-11-02T22:00" to
	// "2026-11-03T06:00" is a night shift.
	StartsAt civil.DateTime `json:"starts_at"`
	EndsAt   civil.DateTime `json:"ends_at"`
	// TimeZone is the zone of the place; if it is missing, the worker's (or the application's).
	TimeZone *string `json:"time_zone"`
	Note     string  `json:"note"`
}

func (in *shiftInput) normalize() {
	in.Note = strings.TrimSpace(in.Note)
	if in.TimeZone != nil {
		tz := strings.TrimSpace(*in.TimeZone)
		in.TimeZone = &tz
	}
}

func (in shiftInput) validate() error {
	fields := httpx.Fields{}
	if in.UserID < 1 {
		fields["user_id"] = i18n.M("validation.required")
	}
	if msg := validate.RequiredCivil(in.StartsAt); !msg.IsZero() {
		fields["starts_at"] = msg
	}
	if msg := validate.RequiredCivil(in.EndsAt); !msg.IsZero() {
		fields["ends_at"] = msg
	} else if in.StartsAt.IsValid() && !in.EndsAt.After(in.StartsAt) {
		fields["ends_at"] = i18n.M("validation.before_start")
	}
	if in.TimeZone != nil {
		if msg := validate.TimeZone(*in.TimeZone); !msg.IsZero() {
			fields["time_zone"] = msg
		}
	}
	if msg := validate.MaxLen(in.Note, 500); !msg.IsZero() {
		fields["note"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// instants turns the hours of the shift into instants of loc, and checks how long it lasts.
func (in shiftInput) instants(loc *time.Location) (start, end time.Time, err error) {
	start, end = in.StartsAt.In(loc).UTC(), in.EndsAt.In(loc).UTC()
	switch {
	case !end.After(start):
		// It can happen only with the hour that does not exist, which moves forward: 02:15 to 03:00 on
		// the day of the jump is 03:15 to 03:00.
		return start, end, httpx.Validation(httpx.Fields{"ends_at": i18n.M("validation.before_start")})
	case end.Sub(start) > maxShift:
		return start, end, httpx.Validation(httpx.Fields{"ends_at": i18n.M("validation.max_hours", "max", int(maxShift/time.Hour))})
	}
	return start, end, nil
}
