package branches

import (
	"fmt"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// maxWeeklyHours is the most ranges a week can have: four per day is already a lot.
const maxWeeklyHours = 28

// branchInput is the body to create or replace a branch.
type branchInput struct {
	Name     string `json:"name"`
	TimeZone string `json:"time_zone"`
}

func (in *branchInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.TimeZone = strings.TrimSpace(in.TimeZone)
}

func (in branchInput) validate() error {
	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, 120); !msg.IsZero() {
		fields["name"] = msg
	}
	if msg := validate.TimeZone(in.TimeZone); !msg.IsZero() {
		fields["time_zone"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// hoursInput is the body of PUT /branches/{id}/hours: the whole week.
type hoursInput struct {
	Hours []weeklyHoursInput `json:"hours"`
}

type weeklyHoursInput struct {
	// Weekday is a pointer to tell "not sent" apart from 0 (Sunday).
	Weekday  *int       `json:"weekday"`
	OpensAt  civil.Time `json:"opens_at"`
	ClosesAt civil.Time `json:"closes_at"`
}

// validate checks every range and that none overlaps another. The errors of a range go in
// "hours.<index>.<field>".
func (in hoursInput) validate() ([]WeeklyHours, error) {
	if len(in.Hours) > maxWeeklyHours {
		return nil, httpx.Validation(httpx.Fields{"hours": i18n.M("validation.too_many_items", "max", maxWeeklyHours)})
	}
	fields := httpx.Fields{}
	weekly := make([]WeeklyHours, 0, len(in.Hours))
	for i, h := range in.Hours {
		field := func(name string) string { return fmt.Sprintf("hours.%d.%s", i, name) }
		if h.Weekday == nil || *h.Weekday < 0 || *h.Weekday > 6 {
			fields[field("weekday")] = i18n.M("validation.integer_range", "min", 0, "max", 6)
		}
		valid := true
		for name, msg := range map[string]i18n.Message{
			"opens_at":  validate.RequiredCivil(h.OpensAt),
			"closes_at": validate.RequiredCivil(h.ClosesAt),
		} {
			if !msg.IsZero() {
				fields[field(name)] = msg
				valid = false
			}
		}
		if valid && h.OpensAt == h.ClosesAt {
			fields[field("closes_at")] = i18n.M("validation.same_as_start")
		}
		if h.Weekday != nil {
			weekly = append(weekly, WeeklyHours{Weekday: time.Weekday(*h.Weekday), Hours: Hours{OpensAt: h.OpensAt, ClosesAt: h.ClosesAt}})
		}
	}
	if len(fields) > 0 {
		return nil, httpx.Validation(fields)
	}

	for i := range weekly {
		for j := range i {
			if overlaps(weekly[i], weekly[j]) {
				fields[fmt.Sprintf("hours.%d", i)] = i18n.M("validation.overlapping_hours")
			}
		}
	}
	if len(fields) > 0 {
		return nil, httpx.Validation(fields)
	}
	return weekly, nil
}

// exceptionInput is the body of PUT /branches/{id}/exceptions/{date}: closed that day, or other
// hours.
type exceptionInput struct {
	Closed   bool       `json:"closed"`
	OpensAt  civil.Time `json:"opens_at"`
	ClosesAt civil.Time `json:"closes_at"`
	Note     string     `json:"note"`
}

func (in *exceptionInput) normalize() {
	in.Note = strings.TrimSpace(in.Note)
}

func (in exceptionInput) validate() error {
	fields := httpx.Fields{}
	if msg := validate.MaxLen(in.Note, 200); !msg.IsZero() {
		fields["note"] = msg
	}
	if in.Closed {
		// Closed takes no hours.
		for name, v := range map[string]civil.Time{"opens_at": in.OpensAt, "closes_at": in.ClosesAt} {
			if !v.IsZero() {
				fields[name] = i18n.M("validation.closed_without_hours")
			}
		}
	} else {
		if msg := validate.RequiredCivil(in.OpensAt); !msg.IsZero() {
			fields["opens_at"] = msg
		}
		if msg := validate.RequiredCivil(in.ClosesAt); !msg.IsZero() {
			fields["closes_at"] = msg
		} else if in.OpensAt == in.ClosesAt {
			fields["closes_at"] = i18n.M("validation.same_as_start")
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}
