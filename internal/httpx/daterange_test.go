package httpx

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
)

func TestParseDateRange(t *testing.T) {
	today := civil.NewDate(2026, time.October, 31)
	for query, want := range map[string]string{
		"":                               "2026-10-31 2026-11-06",
		"?from=2026-11-02":               "2026-11-02 2026-11-08",
		"?from=2026-11-02&to=2026-11-02": "2026-11-02 2026-11-02",
		"?to=2026-11-30":                 "2026-10-31 2026-11-30",
		"?from=2026-11-01&to=2026-12-01": "2026-11-01 2026-12-01",
	} {
		rng, err := ParseDateRange(httptest.NewRequest("GET", "/x"+query, nil), today, 7, 31)
		if err != nil {
			t.Errorf("%q: %v", query, err)
			continue
		}
		if got := rng.From.String() + " " + rng.To.String(); got != want {
			t.Errorf("%q = %s, want %s", query, got, want)
		}
	}

	for query, field := range map[string]string{
		"?from=31/10/2026":               "from",
		"?to=2026-02-30":                 "to",
		"?from=2026-11-02&to=2026-11-01": "to",
		"?from=2026-11-01&to=2026-12-02": "to",
	} {
		_, err := ParseDateRange(httptest.NewRequest("GET", "/x"+query, nil), today, 7, 31)
		var e *Error
		if !errors.As(err, &e) || e.Fields[field].IsZero() {
			t.Errorf("%q: error = %v, want one on %s", query, err, field)
		}
	}
}
