package civil_test

import (
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestPostgreSQLRoundTrip(t *testing.T) {
	pool := testutil.NewDB(t)

	d := civil.NewDate(2026, time.October, 31)
	tm := civil.NewTime(9, 30)
	dt := civil.NewDate(2026, time.November, 5).At(civil.NewTime(10, 0))

	var (
		gotD  civil.Date
		gotT  civil.Time
		gotDT civil.DateTime
		text  string
	)
	err := pool.QueryRow(t.Context(), "SELECT $1::date, $2::time, $3::timestamp, $1::date::text || ' ' || $2::time::text || ' ' || $3::timestamp::text",
		d, tm, dt).Scan(&gotD, &gotT, &gotDT, &text)
	if err != nil {
		t.Fatal(err)
	}
	if gotD != d || gotT != tm || gotDT != dt {
		t.Errorf("read %v, %v, %v; want %v, %v, %v", gotD, gotT, gotDT, d, tm, dt)
	}
	if text != "2026-10-31 09:30:00 2026-11-05 10:00:00" {
		t.Errorf("PostgreSQL stored %q", text)
	}

	// The zero values are NULL, both ways.
	var nulls [3]bool
	if err := pool.QueryRow(t.Context(), "SELECT $1::date IS NULL, $2::time IS NULL, $3::timestamp IS NULL",
		civil.Date{}, civil.Time{}, civil.DateTime{}).Scan(&nulls[0], &nulls[1], &nulls[2]); err != nil {
		t.Fatal(err)
	}
	if nulls != [3]bool{true, true, true} {
		t.Errorf("zero values stored as NULL: %v", nulls)
	}
	if err := pool.QueryRow(t.Context(), "SELECT NULL::date, NULL::time, NULL::timestamp").Scan(&gotD, &gotT, &gotDT); err != nil {
		t.Fatal(err)
	}
	if !gotD.IsZero() || !gotT.IsZero() || !gotDT.IsZero() {
		t.Errorf("NULL read as %v, %v, %v", gotD, gotT, gotDT)
	}

	// Seconds do not fit, and an invalid value is not stored.
	if err := pool.QueryRow(t.Context(), "SELECT '09:30:15'::time").Scan(&gotT); err == nil {
		t.Error("want an error reading a time with seconds")
	}
	if _, err := pool.Exec(t.Context(), "SELECT $1::date", civil.NewDate(2026, time.February, 30)); err == nil {
		t.Error("want an error storing an invalid date")
	}
}
