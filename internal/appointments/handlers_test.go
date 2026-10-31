package appointments_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"

	"github.com/NoeOnDev/go-api-base/internal/appointments"
	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type appointment struct {
	ID              int64  `json:"id"`
	BranchID        int64  `json:"branch_id"`
	BranchName      string `json:"branch_name"`
	UserID          int64  `json:"user_id"`
	LocalStartsAt   string `json:"local_starts_at"`
	TimeZone        string `json:"time_zone"`
	StartsAt        string `json:"starts_at"`
	EndsAt          string `json:"ends_at"`
	DurationMinutes int    `json:"duration_minutes"`
	Status          string `json:"status"`
}

type availability struct {
	Date     string `json:"date"`
	TimeZone string `json:"time_zone"`
	Slots    []struct {
		LocalStartsAt string `json:"local_starts_at"`
		StartsAt      string `json:"starts_at"`
	} `json:"slots"`
}

type fixture struct {
	api                http.Handler
	pool               *pgxpool.Pool
	admin, alice, bob  *http.Cookie
	aliceID            int64
	branchID           int64
	date               civil.Date // three days from now in Bogotá
	bogota             *time.Location
	branchPath, slotAt string
}

// setup creates a clinic in Bogotá open every day from 9:00 to 17:00, and an API that queues the
// jobs with a client without workers (to check them with rivertest).
func setup(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewDB(t)
	jobs, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{api: testutil.NewAPI(pool, testutil.WithJobs(jobs)), pool: pool}
	_, f.admin = testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, f.bob = testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	f.alice, f.aliceID = aliceCookie, alice.ID

	b := testutil.Decode[struct {
		ID int64 `json:"id"`
	}](t, testutil.Do(t, f.api, f.admin, http.MethodPost, "/branches", `{"name":"Clinic","time_zone":"America/Bogota"}`), http.StatusCreated)
	f.branchID = b.ID
	f.branchPath = fmt.Sprintf("/branches/%d", b.ID)
	var hours []string
	for d := range 7 {
		hours = append(hours, fmt.Sprintf(`{"weekday":%d,"opens_at":"09:00","closes_at":"17:00"}`, d))
	}
	testutil.Decode[map[string]any](t, testutil.Do(t, f.api, f.admin, http.MethodPut, f.branchPath+"/hours", `{"hours":[`+strings.Join(hours, ",")+`]}`), http.StatusOK)

	f.bogota, _ = civil.LoadLocation("America/Bogota")
	f.date = civil.DateOf(time.Now().In(f.bogota)).AddDays(3)
	f.slotAt = f.date.String() + "T10:00"
	return f
}

func TestBookAndCancel(t *testing.T) {
	f := setup(t)

	// The availability is public: 9:00 to 16:30 every 15 minutes, for 30 minutes.
	free := testutil.Decode[availability](t, testutil.Do(t, f.api, nil, http.MethodGet, f.branchPath+"/availability?date="+f.date.String(), ""), http.StatusOK)
	if free.TimeZone != "America/Bogota" || len(free.Slots) != 31 || free.Slots[0].LocalStartsAt != f.date.String()+"T09:00" {
		t.Fatalf("availability = %d slots, first %+v", len(free.Slots), free.Slots[0])
	}

	w := testutil.Do(t, f.api, f.alice, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.slotAt))
	a := testutil.Decode[appointment](t, w, http.StatusCreated)
	wantStart := f.date.At(civil.NewTime(10, 0)).In(f.bogota).UTC().Format(time.RFC3339)
	if a.LocalStartsAt != f.slotAt || a.TimeZone != "America/Bogota" || a.StartsAt != wantStart || a.DurationMinutes != 30 ||
		a.Status != "booked" || a.BranchName != "Clinic" || a.UserID != f.aliceID {
		t.Fatalf("booked = %+v", a)
	}
	if loc := w.Header().Get("Location"); loc != fmt.Sprintf("/appointments/%d", a.ID) {
		t.Errorf("Location = %q", loc)
	}

	// The reminder is queued for 24 hours before.
	job := rivertest.RequireInserted(t.Context(), t, riverpgxv5.New(f.pool), &appointments.ReminderArgs{}, nil)
	if job.Args.AppointmentID != a.ID || !job.ScheduledAt.Equal(job.Args.StartsAt.Add(-24*time.Hour)) {
		t.Errorf("reminder = %+v, scheduled at %s", job.Args, job.ScheduledAt)
	}

	// The same slot, or one that overlaps it, is taken.
	for _, at := range []string{f.slotAt, f.date.String() + "T09:45", f.date.String() + "T10:15"} {
		w := testutil.Do(t, f.api, f.bob, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, at))
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "slot_taken" {
			t.Errorf("%s: %+v", at, resp.Error)
		}
	}
	free = testutil.Decode[availability](t, testutil.Do(t, f.api, nil, http.MethodGet, f.branchPath+"/availability?date="+f.date.String(), ""), http.StatusOK)
	if len(free.Slots) != 28 {
		t.Errorf("availability after booking = %d slots, want 28", len(free.Slots))
	}

	// Someone else's appointment does not exist for them; the admin sees it.
	path := fmt.Sprintf("/appointments/%d", a.ID)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.bob, http.MethodGet, path, ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.bob, http.MethodPost, path+"/cancel", ""), http.StatusNotFound)
	testutil.Decode[appointment](t, testutil.Do(t, f.api, f.admin, http.MethodGet, path, ""), http.StatusOK)
	testutil.Decode[appointment](t, testutil.Do(t, f.api, f.alice, http.MethodGet, path, ""), http.StatusOK)

	// A branch with appointments cannot be deleted.
	if resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.admin, http.MethodDelete, f.branchPath, ""), http.StatusConflict); resp.Error.Code != "branch_in_use" {
		t.Errorf("delete branch: %+v", resp.Error)
	}

	// Canceling frees the slot, and doing it again changes nothing.
	for range 2 {
		if c := testutil.Decode[appointment](t, testutil.Do(t, f.api, f.alice, http.MethodPost, path+"/cancel", ""), http.StatusOK); c.Status != "canceled" {
			t.Errorf("canceled = %+v", c)
		}
	}
	testutil.Decode[appointment](t, testutil.Do(t, f.api, f.bob, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.slotAt)), http.StatusCreated)

	// The agenda of the day, only for admins, with the canceled one.
	agenda := testutil.Decode[struct {
		Data []appointment `json:"data"`
	}](t, testutil.Do(t, f.api, f.admin, http.MethodGet, f.branchPath+"/appointments?date="+f.date.String(), ""), http.StatusOK)
	if len(agenda.Data) != 2 || agenda.Data[0].Status != "canceled" || agenda.Data[1].Status != "booked" {
		t.Errorf("agenda = %+v", agenda.Data)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.alice, http.MethodGet, f.branchPath+"/appointments", ""), http.StatusForbidden)
}

func TestBookingValidation(t *testing.T) {
	f := setup(t)
	yesterday := civil.DateOf(time.Now().In(f.bogota)).AddDays(-1)

	for _, tt := range []struct {
		body  string
		field string
	}{
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.date.String()+"T18:00"), "starts_at"},    // closed
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.date.String()+"T10:05"), "starts_at"},    // not a slot
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.date.String()+"T16:45"), "starts_at"},    // ends after closing
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, yesterday.String()+"T10:00"), "starts_at"}, // past
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":"05/11/2026 10:00"}`, f.branchID), "starts_at"},              // format
		{fmt.Sprintf(`{"branch_id":%d}`, f.branchID), "starts_at"},                                             // missing
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q,"duration_minutes":20}`, f.branchID, f.slotAt), "duration_minutes"},
		{fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID+100, f.slotAt), "branch_id"},
		{fmt.Sprintf(`{"starts_at":%q}`, f.slotAt), "branch_id"},
	} {
		w := testutil.Do(t, f.api, f.alice, http.MethodPost, "/appointments", tt.body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", tt.body, tt.field, resp.Error)
		}
	}

	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, nil, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.slotAt)), http.StatusUnauthorized)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, nil, http.MethodGet, "/appointments", ""), http.StatusUnauthorized)
	for _, q := range []string{"?date=05-11-2026", "?duration=7"} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, nil, http.MethodGet, f.branchPath+"/availability"+q, ""), http.StatusUnprocessableEntity)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, nil, http.MethodGet, "/branches/999/availability", ""), http.StatusNotFound)
}

// TestConcurrentBookings sends the same slot at once: only one booking succeeds.
func TestConcurrentBookings(t *testing.T) {
	f := setup(t)
	codes := make(chan int, 5)
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Go(func() {
			ip := fmt.Sprintf("192.0.2.%d", 10+i)
			w := testutil.DoFromIP(t, f.api, ip, f.alice, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.slotAt))
			codes <- w.Code
		})
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("status = %d", code)
		}
	}
	if created != 1 {
		t.Errorf("%d bookings of the same slot succeeded, want 1", created)
	}
}

func TestListInTheUserZone(t *testing.T) {
	f := setup(t)
	testutil.Decode[appointment](t, testutil.Do(t, f.api, f.alice, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.date.String()+"T16:00")), http.StatusCreated)

	list := func(query string) int {
		t.Helper()
		l := testutil.Decode[struct {
			Data []appointment `json:"data"`
			Meta struct {
				Total int64 `json:"total"`
			} `json:"meta"`
		}](t, testutil.Do(t, f.api, f.alice, http.MethodGet, "/appointments"+query, ""), http.StatusOK)
		return int(l.Meta.Total)
	}
	day := "?from=" + f.date.String() + "&to=" + f.date.String()
	next := "?from=" + f.date.AddDays(1).String() + "&to=" + f.date.AddDays(1).String()

	// By default, from today and for 30 days; and the date is that of the user's zone.
	if list("") != 1 || list(day) != 1 || list(next) != 0 {
		t.Errorf("in Bogotá: default %d, that day %d, the next %d", list(""), list(day), list(next))
	}
	// 16:00 in Bogotá is 06:00 of the next day in Tokyo.
	if _, err := f.pool.Exec(t.Context(), "UPDATE users SET time_zone = 'Asia/Tokyo' WHERE id = $1", f.aliceID); err != nil {
		t.Fatal(err)
	}
	if list(day) != 0 || list(next) != 1 {
		t.Errorf("in Tokyo: that day %d, the next %d", list(day), list(next))
	}
	// Bob has none.
	if l := testutil.Decode[struct {
		Data []appointment `json:"data"`
	}](t, testutil.Do(t, f.api, f.bob, http.MethodGet, "/appointments", ""), http.StatusOK); len(l.Data) != 0 {
		t.Errorf("Bob sees %+v", l.Data)
	}
}

type notification struct {
	userID int64
	kind   string
	data   map[string]any
}

type recorder struct{ sent []notification }

func (r *recorder) Notify(_ context.Context, userID int64, kind string, data any) error {
	r.sent = append(r.sent, notification{userID, kind, data.(map[string]any)})
	return nil
}

func TestReminderWorker(t *testing.T) {
	f := setup(t)
	a := testutil.Decode[appointment](t, testutil.Do(t, f.api, f.alice, http.MethodPost, "/appointments", fmt.Sprintf(`{"branch_id":%d,"starts_at":%q}`, f.branchID, f.slotAt)), http.StatusCreated)
	startsAt, _ := time.Parse(time.RFC3339, a.StartsAt)

	notifier := &recorder{}
	worker := rivertest.NewWorker(t, riverpgxv5.New(f.pool), &river.Config{}, appointments.NewReminderWorker(store.New(f.pool), notifier))
	work := func(args appointments.ReminderArgs) {
		t.Helper()
		// rivertest inserts the job in a transaction; the worker reads with its own queries.
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		if _, err := worker.Work(t.Context(), t, tx, args, nil); err != nil {
			t.Fatal(err)
		}
	}

	work(appointments.ReminderArgs{AppointmentID: a.ID, StartsAt: startsAt})
	if len(notifier.sent) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(notifier.sent))
	}
	n := notifier.sent[0]
	if n.userID != f.aliceID || n.kind != appointments.NotifyReminder || n.data["branch"] != "Clinic" || n.data["date"] != a.LocalStartsAt[:10] || n.data["time"] != "10:00" || n.data["time_zone"] != "America/Bogota" {
		t.Errorf("notification = %+v", n)
	}

	// An appointment at another time (the arguments are old), canceled or deleted: nothing.
	work(appointments.ReminderArgs{AppointmentID: a.ID, StartsAt: startsAt.Add(time.Hour)})
	testutil.Decode[appointment](t, testutil.Do(t, f.api, f.alice, http.MethodPost, fmt.Sprintf("/appointments/%d/cancel", a.ID), ""), http.StatusOK)
	work(appointments.ReminderArgs{AppointmentID: a.ID, StartsAt: startsAt})
	work(appointments.ReminderArgs{AppointmentID: a.ID + 100, StartsAt: startsAt})
	if len(notifier.sent) != 1 {
		t.Errorf("sent %d notifications, want only the first one", len(notifier.sent))
	}
}
