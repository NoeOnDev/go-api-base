package appointments

import (
	"net/http"
	"strconv"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// Durations of an appointment, in minutes: from 15 to 240, in steps of 15 (like the slots).
const (
	defaultDuration = 30
	minDuration     = 15
	maxDuration     = 240
)

// bookInput is the body of POST /appointments.
type bookInput struct {
	BranchID int64 `json:"branch_id"`
	// StartsAt is the hour of the branch's clock, as the availability gave it: "2026-11-05T10:00".
	StartsAt civil.DateTime `json:"starts_at"`
	// DurationMinutes is optional: 30 by default.
	DurationMinutes int `json:"duration_minutes"`
}

func (in *bookInput) normalize() {
	if in.DurationMinutes == 0 {
		in.DurationMinutes = defaultDuration
	}
}

func (in bookInput) validate() error {
	fields := httpx.Fields{}
	if in.BranchID < 1 {
		fields["branch_id"] = i18n.M("validation.required")
	}
	if msg := validate.RequiredCivil(in.StartsAt); !msg.IsZero() {
		fields["starts_at"] = msg
	}
	if msg := validateDuration(in.DurationMinutes); !msg.IsZero() {
		fields["duration_minutes"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

func validateDuration(minutes int) i18n.Message {
	if minutes < minDuration || minutes > maxDuration || minutes%int(slotStep/time.Minute) != 0 {
		return i18n.M("validation.duration_minutes", "min", minDuration, "max", maxDuration, "step", int(slotStep/time.Minute))
	}
	return i18n.Message{}
}

// parseDuration reads ?duration= (minutes), 30 by default.
func parseDuration(r *http.Request) (time.Duration, error) {
	minutes := defaultDuration
	if v := r.URL.Query().Get("duration"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = 0
		}
		minutes = n
	}
	if msg := validateDuration(minutes); !msg.IsZero() {
		return 0, httpx.Validation(httpx.Fields{"duration": msg})
	}
	return time.Duration(minutes) * time.Minute, nil
}

// parseDate reads ?date= (2006-01-02), today by default.
func parseDate(r *http.Request, today civil.Date) (civil.Date, error) {
	v := r.URL.Query().Get("date")
	if v == "" {
		return today, nil
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		return civil.Date{}, httpx.Validation(httpx.Fields{"date": i18n.M("validation.date")})
	}
	return d, nil
}
