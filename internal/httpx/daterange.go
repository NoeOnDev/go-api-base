package httpx

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// DateRange is a range of days of the calendar, both included (?from=2026-11-01&to=2026-11-07).
// Turn it into instants of someone's zone with civil.DayRange. See docs/time.md.
type DateRange struct {
	From civil.Date
	To   civil.Date
}

// Days returns how many days the range covers: 1 if From and To are the same day.
func (d DateRange) Days() int { return d.To.DaysSince(d.From) + 1 }

// ParseDateRange reads ?from= and ?to= (2006-01-02) from the URL. Without from, it is today (the
// day of whoever asks, or of the place: civil.DateOf(time.Now().In(loc))). Without to, the range
// covers defaultDays. It covers at most maxDays: a bigger range asks the database for too much.
// If they are invalid, it returns a validation error with the matching field.
func ParseDateRange(r *http.Request, today civil.Date, defaultDays, maxDays int) (DateRange, error) {
	q := r.URL.Query()
	fields := Fields{}
	rng := DateRange{From: today}
	if v := q.Get("from"); v != "" {
		d, err := civil.ParseDate(v)
		if err != nil {
			fields["from"] = i18n.M("validation.date")
		}
		rng.From = d
	}
	rng.To = rng.From.AddDays(defaultDays - 1)
	if v := q.Get("to"); v != "" {
		d, err := civil.ParseDate(v)
		if err != nil {
			fields["to"] = i18n.M("validation.date")
		}
		rng.To = d
	}
	if len(fields) == 0 {
		switch {
		case rng.To.Before(rng.From):
			fields["to"] = i18n.M("validation.before_start")
		case rng.Days() > maxDays:
			fields["to"] = i18n.M("validation.range_too_long", "max", maxDays)
		}
	}
	if len(fields) > 0 {
		return DateRange{}, Validation(fields)
	}
	return rng, nil
}
