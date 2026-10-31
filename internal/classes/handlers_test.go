package classes_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type class struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	TimeZone        string  `json:"time_zone"`
	Weekdays        []int   `json:"weekdays"`
	StartTime       string  `json:"start_time"`
	DurationMinutes int     `json:"duration_minutes"`
	StartsOn        string  `json:"starts_on"`
	EndsOn          *string `json:"ends_on"`
}

type session struct {
	ClassID       int64  `json:"class_id"`
	ClassName     string `json:"class_name"`
	Date          string `json:"date"`
	LocalStartsAt string `json:"local_starts_at"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
	Canceled      bool   `json:"canceled"`
	Moved         bool   `json:"moved"`
	Note          string `json:"note"`
}

type sessions struct {
	TimeZone string    `json:"time_zone"`
	Data     []session `json:"data"`
}

// Yoga on Mondays and Wednesdays at 18:00 in New York, from October 19 to November 11, 2026.
const yoga = `{"name":"Yoga","time_zone":"America/New_York","weekdays":[3,1,1],"start_time":"18:00",
	"duration_minutes":60,"starts_on":"2026-10-19","ends_on":"2026-11-11"}`

func TestClasses(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	created := testutil.Decode[class](t, testutil.Do(t, api, admin, http.MethodPost, "/classes", yoga), http.StatusCreated)
	if created.Name != "Yoga" || len(created.Weekdays) != 2 || created.Weekdays[0] != 1 || created.StartTime != "18:00" || created.EndsOn == nil || *created.EndsOn != "2026-11-11" {
		t.Fatalf("created = %+v", created)
	}
	path := fmt.Sprintf("/classes/%d", created.ID)

	// The sessions keep 18:00 of New York: 22:00 UTC before November 1, 23:00 UTC after.
	got := testutil.Decode[sessions](t, testutil.Do(t, api, nil, http.MethodGet, path+"/sessions?from=2026-10-26&to=2026-11-08", ""), http.StatusOK)
	if got.TimeZone != "America/New_York" || len(got.Data) != 4 {
		t.Fatalf("sessions = %+v", got)
	}
	if got.Data[1].StartsAt != "2026-10-28T22:00:00Z" || got.Data[2].StartsAt != "2026-11-02T23:00:00Z" || got.Data[2].LocalStartsAt != "2026-11-02T18:00" {
		t.Errorf("sessions around the change = %+v", got.Data)
	}

	// Cancel one and move another; the session must be one of the rule.
	cancel := testutil.Decode[session](t, testutil.Do(t, api, admin, http.MethodPut, path+"/sessions/2026-10-26", `{"canceled":true,"note":"Holiday"}`), http.StatusOK)
	if !cancel.Canceled || cancel.Note != "Holiday" {
		t.Errorf("canceled = %+v", cancel)
	}
	moved := testutil.Decode[session](t, testutil.Do(t, api, admin, http.MethodPut, path+"/sessions/2026-10-28", `{"starts_at":"2026-10-29T19:00"}`), http.StatusOK)
	if !moved.Moved || moved.Date != "2026-10-28" || moved.StartsAt != "2026-10-29T23:00:00Z" {
		t.Errorf("moved = %+v", moved)
	}
	w := testutil.Do(t, api, admin, http.MethodPut, path+"/sessions/2026-10-27", `{"canceled":true}`)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["date"] == "" {
		t.Errorf("a Tuesday: %+v", resp.Error)
	}

	got = testutil.Decode[sessions](t, testutil.Do(t, api, nil, http.MethodGet, path+"/sessions?from=2026-10-26&to=2026-10-29", ""), http.StatusOK)
	if len(got.Data) != 2 || !got.Data[0].Canceled || got.Data[1].LocalStartsAt != "2026-10-29T19:00" {
		t.Errorf("sessions with changes = %+v", got.Data)
	}
	// Moved out of a range, it is no longer in it.
	if got := testutil.Decode[sessions](t, testutil.Do(t, api, nil, http.MethodGet, path+"/sessions?from=2026-10-28&to=2026-10-28", ""), http.StatusOK); len(got.Data) != 0 {
		t.Errorf("October 28 = %+v", got.Data)
	}

	// Removing the change brings the session back to the rule.
	if w := testutil.Do(t, api, admin, http.MethodDelete, path+"/sessions/2026-10-28", ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE change: status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodDelete, path+"/sessions/2026-10-28", ""), http.StatusNotFound)

	// The agenda of every class, sorted by when they start.
	testutil.Decode[class](t, testutil.Do(t, api, admin, http.MethodPost, "/classes", `{"name":"Spinning","time_zone":"America/Bogota",
		"weekdays":[2],"start_time":"07:00","duration_minutes":45,"starts_on":"2026-10-01"}`), http.StatusCreated)
	agenda := testutil.Decode[sessions](t, testutil.Do(t, api, nil, http.MethodGet, "/classes/sessions?from=2026-10-26&to=2026-10-28", ""), http.StatusOK)
	var names []string
	for _, s := range agenda.Data {
		names = append(names, s.ClassName+" "+s.Date)
	}
	want := []string{"Yoga 2026-10-26", "Spinning 2026-10-27", "Yoga 2026-10-28"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("agenda = %v, want %v", names, want)
	}

	// Update and delete.
	updated := testutil.Decode[class](t, testutil.Do(t, api, admin, http.MethodPut, path, `{"name":"Yoga","time_zone":"America/New_York","weekdays":[1],
		"start_time":"07:30","duration_minutes":60,"starts_on":"2026-10-19"}`), http.StatusOK)
	if updated.EndsOn != nil || updated.StartTime != "07:30" {
		t.Errorf("updated = %+v", updated)
	}
	if w := testutil.Do(t, api, admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path+"/sessions", ""), http.StatusNotFound)
}

func TestClassValidationAndPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	_, user := testutil.Login(t, pool, "user@example.com", auth.RoleUser)

	for body, fields := range map[string][]string{
		`{"name":"","time_zone":"NY","weekdays":[],"duration_minutes":0}`:                                                                             {"name", "time_zone", "weekdays", "start_time", "duration_minutes", "starts_on"},
		`{"name":"Yoga","time_zone":"UTC","weekdays":[1,9],"start_time":"6pm","duration_minutes":60,"starts_on":"2026-11-02","ends_on":"2026-11-01"}`: {"weekdays.1", "start_time", "ends_on"},
		`{"name":"Yoga","time_zone":"UTC","weekdays":[1],"start_time":"18:00","duration_minutes":60,"starts_on":"02/11/2026"}`:                        {"starts_on"},
	} {
		resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPost, "/classes", body), http.StatusUnprocessableEntity)
		if len(resp.Error.Fields) != len(fields) {
			t.Errorf("%s: fields %v, want %v", body, resp.Error.Fields, fields)
		}
		for _, f := range fields {
			if resp.Error.Fields[f] == "" {
				t.Errorf("%s: want an error on %s: %v", body, f, resp.Error.Fields)
			}
		}
	}

	c := testutil.Decode[class](t, testutil.Do(t, api, admin, http.MethodPost, "/classes", yoga), http.StatusCreated)
	path := fmt.Sprintf("/classes/%d", c.ID)
	for body, field := range map[string]string{
		`{}`: "starts_at",
		`{"canceled":true,"starts_at":"2026-10-29T19:00"}`: "starts_at",
		`{"starts_at":"2026-10-29 19:00"}`:                 "starts_at",
	} {
		resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPut, path+"/sessions/2026-10-26", body), http.StatusUnprocessableEntity)
		if resp.Error.Fields[field] == "" {
			t.Errorf("%s: want an error on %s: %v", body, field, resp.Error.Fields)
		}
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path+"/sessions?from=2026-01-01&to=2026-12-31", ""), http.StatusUnprocessableEntity)

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/classes", yoga},
		{http.MethodPut, path, yoga},
		{http.MethodDelete, path, ""},
		{http.MethodPut, path + "/sessions/2026-10-26", `{"canceled":true}`},
		{http.MethodDelete, path + "/sessions/2026-10-26", ""},
	} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, r.method, r.path, r.body), http.StatusUnauthorized)
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, user, r.method, r.path, r.body), http.StatusForbidden)
	}
}
