package shifts_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type shift struct {
	ID              int64  `json:"id"`
	UserID          int64  `json:"user_id"`
	TimeZone        string `json:"time_zone"`
	LocalStartsAt   string `json:"local_starts_at"`
	LocalEndsAt     string `json:"local_ends_at"`
	StartsAt        string `json:"starts_at"`
	EndsAt          string `json:"ends_at"`
	DurationMinutes int    `json:"duration_minutes"`
}

type entry struct {
	ID       int64   `json:"id"`
	ShiftID  *int64  `json:"shift_id"`
	ClockIn  string  `json:"clock_in"`
	ClockOut *string `json:"clock_out"`
}

func shiftBody(userID int64, starts, ends, zone string) string {
	body := fmt.Sprintf(`{"user_id":%d,"starts_at":%q,"ends_at":%q`, userID, starts, ends)
	if zone != "" {
		body += fmt.Sprintf(`,"time_zone":%q`, zone)
	}
	return body + "}"
}

func setTimeZone(t *testing.T, pool *pgxpool.Pool, userID int64, zone string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "UPDATE users SET time_zone = $1 WHERE id = $2", zone, userID); err != nil {
		t.Fatal(err)
	}
}

func TestShifts(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	maria, mariaCookie := testutil.Login(t, pool, "maria@example.com", auth.RoleUser)
	bobUser, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	setTimeZone(t, pool, maria.ID, "America/New_York")

	// A night shift on the night the clocks jump: it lasts 7 real hours. Without time_zone, María's.
	w := testutil.Do(t, api, admin, http.MethodPost, "/shifts", shiftBody(maria.ID, "2026-03-07T22:00", "2026-03-08T06:00", ""))
	night := testutil.Decode[shift](t, w, http.StatusCreated)
	if night.TimeZone != "America/New_York" || night.StartsAt != "2026-03-08T03:00:00Z" || night.EndsAt != "2026-03-08T10:00:00Z" || night.DurationMinutes != 420 {
		t.Fatalf("night shift = %+v", night)
	}

	// Another shift that overlaps it, for the same person, answers 409; for someone else it is fine.
	w = testutil.Do(t, api, admin, http.MethodPost, "/shifts", shiftBody(maria.ID, "2026-03-08T05:00", "2026-03-08T09:00", ""))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "shift_overlap" {
		t.Errorf("overlap: %+v", resp.Error)
	}
	day := testutil.Decode[shift](t, testutil.Do(t, api, admin, http.MethodPost, "/shifts", shiftBody(maria.ID, "2026-03-09T09:00", "2026-03-09T17:00", "America/Chicago")), http.StatusCreated)
	if day.TimeZone != "America/Chicago" || day.StartsAt != "2026-03-09T14:00:00Z" {
		t.Errorf("shift in Chicago = %+v", day)
	}

	// Each one sees theirs; the admin, everyone's or those of one user.
	path := fmt.Sprintf("/shifts/%d", night.ID)
	testutil.Decode[shift](t, testutil.Do(t, api, mariaCookie, http.MethodGet, path, ""), http.StatusOK)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, http.MethodGet, path, ""), http.StatusNotFound)
	list := func(cookie *http.Cookie, query string) int {
		t.Helper()
		return len(testutil.Decode[struct {
			Data []shift `json:"data"`
		}](t, testutil.Do(t, api, cookie, http.MethodGet, "/shifts"+query, ""), http.StatusOK).Data)
	}
	march := "?from=2026-03-07&to=2026-03-10"
	if list(mariaCookie, march) != 2 || list(bob, march) != 0 || list(admin, march) != 2 || list(admin, march+fmt.Sprintf("&user_id=%d", maria.ID)) != 2 {
		t.Errorf("lists: María %d, Bob %d, admin %d", list(mariaCookie, march), list(bob, march), list(admin, march))
	}
	// Bob cannot see María's by asking for them.
	if list(bob, march+fmt.Sprintf("&user_id=%d", maria.ID)) != 0 {
		t.Error("Bob sees María's shifts")
	}

	// Updating and deleting, only the admin.
	updated := testutil.Decode[shift](t, testutil.Do(t, api, admin, http.MethodPut, path, shiftBody(maria.ID, "2026-03-07T23:00", "2026-03-08T06:00", "")), http.StatusOK)
	if updated.DurationMinutes != 360 {
		t.Errorf("updated = %+v", updated)
	}
	// Another user_id reassigns it, in their zone if the body does not bring one.
	setTimeZone(t, pool, bobUser.ID, "Asia/Tokyo")
	moved := testutil.Decode[shift](t, testutil.Do(t, api, admin, http.MethodPut, path, shiftBody(bobUser.ID, "2026-03-07T23:00", "2026-03-08T06:00", "")), http.StatusOK)
	if moved.UserID != bobUser.ID || moved.TimeZone != "Asia/Tokyo" || moved.StartsAt != "2026-03-07T14:00:00Z" {
		t.Errorf("reassigned = %+v", moved)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, mariaCookie, http.MethodGet, path, ""), http.StatusNotFound)
	testutil.Decode[shift](t, testutil.Do(t, api, bob, http.MethodGet, path, ""), http.StatusOK)
	// Reassigned back to María, it cannot overlap one of hers.
	w = testutil.Do(t, api, admin, http.MethodPut, path, shiftBody(maria.ID, "2026-03-09T10:00", "2026-03-09T12:00", "America/Chicago"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "shift_overlap" {
		t.Errorf("reassigning onto an overlap: %+v", resp.Error)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, mariaCookie, http.MethodDelete, path, ""), http.StatusForbidden)
	if w := testutil.Do(t, api, admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodDelete, path, ""), http.StatusNotFound)
}

func TestShiftValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	maria, _ := testutil.Login(t, pool, "maria@example.com", auth.RoleUser)

	for body, field := range map[string]string{
		shiftBody(maria.ID, "2026-11-02T22:00", "2026-11-02T21:00", ""):                 "ends_at",
		shiftBody(maria.ID, "2026-11-02T08:00", "2026-11-03T09:00", ""):                 "ends_at", // 25 hours
		shiftBody(maria.ID, "2026-11-02 08:00", "2026-11-02T16:00", ""):                 "starts_at",
		shiftBody(maria.ID, "2026-11-02T08:00", "2026-11-02T16:00", "Mars/Base"):        "time_zone",
		shiftBody(maria.ID+100, "2026-11-02T08:00", "2026-11-02T16:00", ""):             "user_id",
		`{"starts_at":"2026-11-02T08:00","ends_at":"2026-11-02T16:00"}`:                 "user_id",
		fmt.Sprintf(`{"user_id":%d,"starts_at":"2026-11-02T08:00"}`, maria.ID):          "ends_at",
		shiftBody(maria.ID, "2026-03-08T02:15", "2026-03-08T03:00", "America/New_York"): "ends_at", // 02:15 does not exist
	} {
		w := testutil.Do(t, api, admin, http.MethodPost, "/shifts", body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields[field] == "" {
			t.Errorf("%s: want an error on %s: %+v", body, field, resp.Error)
		}
	}
}

func TestTimeEntries(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	maria, mariaCookie := testutil.Login(t, pool, "maria@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	// A shift going on now (in UTC, María's zone), so the entry belongs to it.
	now := time.Now().UTC()
	current := testutil.Decode[shift](t, testutil.Do(t, api, admin, http.MethodPost, "/shifts", shiftBody(maria.ID,
		now.Add(-time.Hour).Format("2006-01-02T15:04"), now.Add(2*time.Hour).Format("2006-01-02T15:04"), "UTC")), http.StatusCreated)

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, mariaCookie, http.MethodPost, "/time-entries/clock-out", ""), http.StatusConflict)
	in := testutil.Decode[entry](t, testutil.Do(t, api, mariaCookie, http.MethodPost, "/time-entries/clock-in", ""), http.StatusCreated)
	if in.ShiftID == nil || *in.ShiftID != current.ID || in.ClockOut != nil {
		t.Errorf("clock in = %+v", in)
	}
	if resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, mariaCookie, http.MethodPost, "/time-entries/clock-in", ""), http.StatusConflict); resp.Error.Code != "already_clocked_in" {
		t.Errorf("clocking in twice: %+v", resp.Error)
	}

	// A past entry that crosses midnight, in Bogotá: 22:00 to 06:00.
	if _, err := pool.Exec(t.Context(), `INSERT INTO time_entries (user_id, clock_in, clock_out)
		VALUES ($1, '2026-11-02 22:00-05', '2026-11-03 06:00-05')`, maria.ID); err != nil {
		t.Fatal(err)
	}
	setTimeZone(t, pool, maria.ID, "America/Bogota")
	summary := testutil.Decode[struct {
		TimeZone     string `json:"time_zone"`
		TotalMinutes int    `json:"total_minutes"`
		Data         []struct {
			Date    string `json:"date"`
			Minutes int    `json:"minutes"`
		} `json:"data"`
	}](t, testutil.Do(t, api, mariaCookie, http.MethodGet, "/time-entries/summary?from=2026-11-02&to=2026-11-03", ""), http.StatusOK)
	if summary.TimeZone != "America/Bogota" || summary.TotalMinutes != 480 || summary.Data[0].Minutes != 120 || summary.Data[1].Minutes != 360 {
		t.Errorf("summary = %+v", summary)
	}

	// The admin sees María's with user_id; Bob only his own (none).
	entries := func(cookie *http.Cookie, query string) int {
		t.Helper()
		return len(testutil.Decode[struct {
			Data []entry `json:"data"`
		}](t, testutil.Do(t, api, cookie, http.MethodGet, "/time-entries"+query, ""), http.StatusOK).Data)
	}
	nov := "?from=2026-11-02&to=2026-11-03"
	if entries(mariaCookie, nov) != 1 || entries(admin, nov+fmt.Sprintf("&user_id=%d", maria.ID)) != 1 || entries(bob, nov+fmt.Sprintf("&user_id=%d", maria.ID)) != 0 {
		t.Error("the entries are not limited to their owner")
	}

	out := testutil.Decode[entry](t, testutil.Do(t, api, mariaCookie, http.MethodPost, "/time-entries/clock-out", ""), http.StatusOK)
	if out.ID != in.ID || out.ClockOut == nil {
		t.Errorf("clock out = %+v", out)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/time-entries/clock-in", ""), http.StatusUnauthorized)
}
