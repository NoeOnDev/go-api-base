package branches_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type hours struct {
	Weekday  int    `json:"weekday"`
	OpensAt  string `json:"opens_at"`
	ClosesAt string `json:"closes_at"`
}

type branch struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	TimeZone string  `json:"time_zone"`
	Hours    []hours `json:"hours"`
}

type day struct {
	Date      string `json:"date"`
	Exception bool   `json:"exception"`
	Note      string `json:"note"`
	Openings  []struct {
		OpensAt  string `json:"opens_at"`
		ClosesAt string `json:"closes_at"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	} `json:"openings"`
}

// A clinic in Bogotá, open from Monday to Friday 9:00-13:00 and 15:00-19:00.
const clinicHours = `{"hours":[
	{"weekday":1,"opens_at":"09:00","closes_at":"13:00"},{"weekday":1,"opens_at":"15:00","closes_at":"19:00"},
	{"weekday":2,"opens_at":"09:00","closes_at":"13:00"},{"weekday":3,"opens_at":"09:00","closes_at":"13:00"},
	{"weekday":4,"opens_at":"09:00","closes_at":"13:00"},{"weekday":5,"opens_at":"09:00","closes_at":"13:00"}]}`

func TestBranchesCRUD(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	created := testutil.Decode[branch](t,
		testutil.Do(t, api, admin, http.MethodPost, "/branches", `{"name":" Downtown ","time_zone":"America/Bogota"}`),
		http.StatusCreated)
	if created.Name != "Downtown" || created.TimeZone != "America/Bogota" || created.Hours == nil || len(created.Hours) != 0 {
		t.Fatalf("unexpected branch: %+v", created)
	}
	path := fmt.Sprintf("/branches/%d", created.ID)

	// The week replaces the previous one, sorted by day and hour.
	got := testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPut, path+"/hours", clinicHours), http.StatusOK)
	if len(got.Hours) != 6 || got.Hours[0] != (hours{1, "09:00", "13:00"}) || got.Hours[1] != (hours{1, "15:00", "19:00"}) {
		t.Errorf("hours = %+v", got.Hours)
	}
	got = testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPut, path+"/hours",
		`{"hours":[{"weekday":6,"opens_at":"10:00","closes_at":"14:00"}]}`), http.StatusOK)
	if len(got.Hours) != 1 || got.Hours[0] != (hours{6, "10:00", "14:00"}) {
		t.Errorf("replaced hours = %+v", got.Hours)
	}

	// Reads are public.
	got = testutil.Decode[branch](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusOK)
	if got.ID != created.ID || len(got.Hours) != 1 {
		t.Errorf("GET = %+v", got)
	}
	list := testutil.Decode[struct {
		Data []branch `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, "/branches", ""), http.StatusOK)
	if list.Meta.Total != 1 || len(list.Data) != 1 || len(list.Data[0].Hours) != 1 {
		t.Errorf("list = %+v", list)
	}

	// Changing the zone keeps the hours of the clock.
	got = testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPut, path, `{"name":"Downtown","time_zone":"America/Panama"}`), http.StatusOK)
	if got.TimeZone != "America/Panama" || len(got.Hours) != 1 {
		t.Errorf("updated = %+v", got)
	}

	if w := testutil.Do(t, api, admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	for _, p := range []string{path, path + "/schedule", path + "/status", path + "/exceptions"} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, p, ""), http.StatusNotFound)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPut, path+"/hours", clinicHours), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPut, path+"/exceptions/2026-12-25", `{"closed":true}`), http.StatusNotFound)
}

func TestBranchesPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, user := testutil.Login(t, pool, "user@example.com", auth.RoleUser)

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/branches", `{"name":"Downtown","time_zone":"America/Bogota"}`},
		{http.MethodPut, "/branches/1", `{"name":"Downtown","time_zone":"America/Bogota"}`},
		{http.MethodDelete, "/branches/1", ""},
		{http.MethodPut, "/branches/1/hours", clinicHours},
		{http.MethodPut, "/branches/1/exceptions/2026-12-25", `{"closed":true}`},
		{http.MethodDelete, "/branches/1/exceptions/2026-12-25", ""},
	} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, r.method, r.path, r.body), http.StatusUnauthorized)
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, user, r.method, r.path, r.body), http.StatusForbidden)
	}
}

func TestBranchesValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	b := testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPost, "/branches", `{"name":"Downtown","time_zone":"America/Bogota"}`), http.StatusCreated)
	path := fmt.Sprintf("/branches/%d", b.ID)

	for _, tt := range []struct {
		method, path, body string
		fields             []string
	}{
		{http.MethodPost, "/branches", `{"name":"","time_zone":"Bogota"}`, []string{"name", "time_zone"}},
		{http.MethodPut, path + "/hours", `{"hours":[{"opens_at":"9:00","closes_at":"13:00"}]}`, []string{"hours.0.weekday", "hours.0.opens_at"}},
		{http.MethodPut, path + "/hours", `{"hours":[{"weekday":7,"opens_at":"09:00"}]}`, []string{"hours.0.weekday", "hours.0.closes_at"}},
		{http.MethodPut, path + "/hours", `{"hours":[{"weekday":1,"opens_at":"09:00","closes_at":"09:00"}]}`, []string{"hours.0.closes_at"}},
		{http.MethodPut, path + "/hours", `{"hours":[{"weekday":1,"opens_at":"09:00","closes_at":"14:00"},{"weekday":1,"opens_at":"13:00","closes_at":"19:00"}]}`, []string{"hours.1"}},
		// Saturday night overlaps Sunday morning.
		{http.MethodPut, path + "/hours", `{"hours":[{"weekday":0,"opens_at":"01:00","closes_at":"05:00"},{"weekday":6,"opens_at":"22:00","closes_at":"03:00"}]}`, []string{"hours.1"}},
		{http.MethodPut, path + "/exceptions/2026-12-25", `{"opens_at":"10:00"}`, []string{"closes_at"}},
		{http.MethodPut, path + "/exceptions/2026-12-25", `{"closed":true,"opens_at":"10:00","closes_at":"12:00"}`, []string{"opens_at", "closes_at"}},
		{http.MethodPut, path + "/exceptions/25-12-2026", `{"closed":true}`, []string{"date"}},
	} {
		resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, tt.method, tt.path, tt.body), http.StatusUnprocessableEntity)
		if len(resp.Error.Fields) != len(tt.fields) {
			t.Errorf("%s %s: fields %v, want %v", tt.path, tt.body, resp.Error.Fields, tt.fields)
		}
		for _, f := range tt.fields {
			if resp.Error.Fields[f] == "" {
				t.Errorf("%s %s: want an error on %s: %v", tt.path, tt.body, f, resp.Error.Fields)
			}
		}
	}

	for _, query := range []string{"?from=31/10/2026", "?from=2026-11-10&to=2026-11-01", "?from=2026-01-01&to=2026-12-31"} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path+"/schedule"+query, ""), http.StatusUnprocessableEntity)
	}
}

func TestBranchSchedule(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	b := testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPost, "/branches", `{"name":"Clinic","time_zone":"America/Bogota"}`), http.StatusCreated)
	path := fmt.Sprintf("/branches/%d", b.ID)
	testutil.Decode[branch](t, testutil.Do(t, api, admin, http.MethodPut, path+"/hours", clinicHours), http.StatusOK)

	// A holiday, and a Tuesday with other hours.
	testutil.Decode[map[string]any](t, testutil.Do(t, api, admin, http.MethodPut, path+"/exceptions/2026-11-02", `{"closed":true,"note":" All Saints "}`), http.StatusOK)
	testutil.Decode[map[string]any](t, testutil.Do(t, api, admin, http.MethodPut, path+"/exceptions/2026-11-03", `{"opens_at":"10:00","closes_at":"12:00"}`), http.StatusOK)

	schedule := testutil.Decode[struct {
		TimeZone string `json:"time_zone"`
		Data     []day  `json:"data"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, path+"/schedule?from=2026-11-01&to=2026-11-04", ""), http.StatusOK)
	if schedule.TimeZone != "America/Bogota" || len(schedule.Data) != 4 {
		t.Fatalf("schedule = %+v", schedule)
	}
	sunday, holiday, tuesday, wednesday := schedule.Data[0], schedule.Data[1], schedule.Data[2], schedule.Data[3]
	if len(sunday.Openings) != 0 || sunday.Exception {
		t.Errorf("Sunday = %+v", sunday)
	}
	if !holiday.Exception || holiday.Note != "All Saints" || len(holiday.Openings) != 0 {
		t.Errorf("holiday = %+v", holiday)
	}
	if len(tuesday.Openings) != 1 || tuesday.Openings[0].OpensAt != "10:00" || tuesday.Openings[0].StartsAt != "2026-11-03T15:00:00Z" {
		t.Errorf("Tuesday = %+v", tuesday)
	}
	if len(wednesday.Openings) != 1 || wednesday.Openings[0].EndsAt != "2026-11-04T18:00:00Z" {
		t.Errorf("Wednesday = %+v", wednesday)
	}

	exceptions := testutil.Decode[struct {
		Data []struct {
			Date   string `json:"date"`
			Closed bool   `json:"closed"`
			Hours  *hours `json:"hours"`
		} `json:"data"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, path+"/exceptions?from=2026-11-01&to=2026-11-30", ""), http.StatusOK)
	if len(exceptions.Data) != 2 || !exceptions.Data[0].Closed || exceptions.Data[1].Hours == nil || exceptions.Data[1].Hours.OpensAt != "10:00" {
		t.Errorf("exceptions = %+v", exceptions)
	}

	// Deleting the exception brings back the weekly hours.
	if w := testutil.Do(t, api, admin, http.MethodDelete, path+"/exceptions/2026-11-02", ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE exception: status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodDelete, path+"/exceptions/2026-11-02", ""), http.StatusNotFound)
	schedule.Data = nil
	schedule = testutil.Decode[struct {
		TimeZone string `json:"time_zone"`
		Data     []day  `json:"data"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, path+"/schedule?from=2026-11-02&to=2026-11-02", ""), http.StatusOK)
	if len(schedule.Data) != 1 || len(schedule.Data[0].Openings) != 2 {
		t.Errorf("Monday without the holiday = %+v", schedule.Data)
	}

	// Without from, it starts today in Bogotá, and covers a week.
	bogota, _ := civil.LoadLocation("America/Bogota")
	schedule = testutil.Decode[struct {
		TimeZone string `json:"time_zone"`
		Data     []day  `json:"data"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, path+"/schedule", ""), http.StatusOK)
	if len(schedule.Data) != 7 || schedule.Data[0].Date != civil.DateOf(time.Now().In(bogota)).String() {
		t.Errorf("default schedule starts on %v with %d days", schedule.Data[0].Date, len(schedule.Data))
	}

	status := testutil.Decode[struct {
		Open        bool    `json:"open"`
		NextOpensAt *string `json:"next_opens_at"`
	}](t, testutil.Do(t, api, nil, http.MethodGet, path+"/status", ""), http.StatusOK)
	if status.NextOpensAt == nil {
		t.Errorf("status without the next opening: %+v", status)
	}
}
