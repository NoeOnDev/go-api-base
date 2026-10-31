// Package shifts is the example of work shifts: an administrator assigns shifts to the users (with
// the hours of the clock of the place, and crossing midnight if they end the next day), each user
// sees theirs, records when they clock in and out, and gets the minutes worked per day of their
// clock. A person cannot have two shifts that overlap: an EXCLUDE constraint of PostgreSQL stops it.
//
// A shift keeps its hours of the clock and its zone, besides the instants (see "Modeling schedules"
// in docs/time.md); the summary per day is in schedule.go.
package shifts

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Limits of the ranges of days.
const (
	listDays    = 7
	maxListDays = 31
)

// Handler groups the shift and time entry endpoints.
type Handler struct {
	q *store.Queries
	// appZone is APP_TIME_ZONE: the zone of the workers without their own.
	appZone *time.Location
}

// NewHandler creates the handler. appZone is the zone of the users without their own (nil: UTC).
func NewHandler(q *store.Queries, appZone *time.Location) *Handler {
	if appZone == nil {
		appZone = time.UTC
	}
	return &Handler{q: q, appZone: appZone}
}

type shiftResponse struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
	// The hours of the clock of the place, and its zone.
	TimeZone      string         `json:"time_zone"`
	LocalStartsAt civil.DateTime `json:"local_starts_at"`
	LocalEndsAt   civil.DateTime `json:"local_ends_at"`
	// The instants; DurationMinutes is the real time between them.
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Note            string    `json:"note"`
	CreatedAt       time.Time `json:"created_at"`
}

func toShift(s store.Shift) shiftResponse {
	return shiftResponse{
		ID: s.ID, UserID: s.UserID, TimeZone: s.TimeZone, LocalStartsAt: s.LocalStartsAt, LocalEndsAt: s.LocalEndsAt,
		StartsAt: s.StartsAt, EndsAt: s.EndsAt, DurationMinutes: int(s.EndsAt.Sub(s.StartsAt) / time.Minute),
		Note: s.Note, CreatedAt: s.CreatedAt,
	}
}

type entryResponse struct {
	ID       int64      `json:"id"`
	UserID   int64      `json:"user_id"`
	ShiftID  *int64     `json:"shift_id"`
	ClockIn  time.Time  `json:"clock_in"`
	ClockOut *time.Time `json:"clock_out"`
}

func toEntry(e store.TimeEntry) entryResponse {
	return entryResponse{ID: e.ID, UserID: e.UserID, ShiftID: e.ShiftID, ClockIn: e.ClockIn, ClockOut: e.ClockOut}
}

var (
	errNotFound      = httpx.NotFound("shift_not_found")
	errOverlap       = httpx.Conflict("shift_overlap", "starts_at")
	errClockedIn     = httpx.Conflict("already_clocked_in", "")
	errNotClockedIn  = httpx.Conflict("not_clocked_in", "")
	errUnknownWorker = httpx.Validation(httpx.Fields{"user_id": i18n.M("validation.unknown_user")})
)

// workerZone returns the zone of a user: their own, or the application's. errUnknownWorker if the
// user does not exist.
func (h *Handler) workerZone(r *http.Request, userID int64) (*time.Location, error) {
	tz, err := h.q.GetWorkerTimeZone(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errUnknownWorker
	}
	if err != nil {
		return nil, fmt.Errorf("find the zone of user %d: %w", userID, err)
	}
	if tz == nil {
		return h.appZone, nil
	}
	loc, err := civil.LoadLocation(*tz)
	if err != nil {
		return h.appZone, nil
	}
	return loc, nil
}

// readShift decodes and validates the body of a shift, and computes its instants.
func (h *Handler) readShift(w http.ResponseWriter, r *http.Request) (shiftInput, *time.Location, time.Time, time.Time, error) {
	var in shiftInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return in, nil, time.Time{}, time.Time{}, err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return in, nil, time.Time{}, time.Time{}, err
	}
	loc, err := h.workerZone(r, in.UserID)
	if err != nil {
		return in, nil, time.Time{}, time.Time{}, err
	}
	if in.TimeZone != nil {
		if loc, err = civil.LoadLocation(*in.TimeZone); err != nil {
			return in, nil, time.Time{}, time.Time{}, err // validated already
		}
	}
	start, end, err := in.instants(loc)
	return in, loc, start, end, err
}

// create answers POST /shifts with {user_id, starts_at, ends_at, time_zone?, note?}. Only for
// administrators.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	in, loc, start, end, err := h.readShift(w, r)
	if err != nil {
		return err
	}
	s, err := h.q.CreateShift(r.Context(), store.CreateShiftParams{
		UserID: in.UserID, TimeZone: loc.String(), LocalStartsAt: in.StartsAt, LocalEndsAt: in.EndsAt,
		StartsAt: start, EndsAt: end, Note: in.Note,
	})
	if _, ok := db.IsExclusionViolation(err); ok {
		return errOverlap
	}
	if err != nil {
		return fmt.Errorf("create shift: %w", err)
	}
	w.Header().Set("Location", fmt.Sprintf("/shifts/%d", s.ID))
	httpx.Write(w, http.StatusCreated, toShift(s))
	return nil
}

// update answers PUT /shifts/{id}, with the same body. Only for administrators.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	in, loc, start, end, err := h.readShift(w, r)
	if err != nil {
		return err
	}
	s, err := h.q.UpdateShift(r.Context(), store.UpdateShiftParams{
		ID: id, TimeZone: loc.String(), LocalStartsAt: in.StartsAt, LocalEndsAt: in.EndsAt,
		StartsAt: start, EndsAt: end, Note: in.Note,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if _, ok := db.IsExclusionViolation(err); ok {
		return errOverlap
	}
	if err != nil {
		return fmt.Errorf("update shift %d: %w", id, err)
	}
	httpx.Write(w, http.StatusOK, toShift(s))
	return nil
}

// delete answers DELETE /shifts/{id}. Only for administrators.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := h.q.DeleteShift(r.Context(), id)
	if err != nil {
		return fmt.Errorf("delete shift %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// get answers GET /shifts/{id}: the worker's, or any for an administrator. Someone else's answers 404.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	s, err := h.q.GetShift(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("get shift %d: %w", id, err)
	}
	if user := auth.MustUser(r.Context()); s.UserID != user.ID && !user.IsAdmin() {
		return errNotFound
	}
	httpx.Write(w, http.StatusOK, toShift(s))
	return nil
}

// subject returns whose data a request asks for: ?user_id= for an administrator (all of them, nil,
// if it is missing and all is true), always the user themself for the rest.
func subject(r *http.Request, all bool) (*int64, error) {
	user := auth.MustUser(r.Context())
	v := r.URL.Query().Get("user_id")
	if !user.IsAdmin() || (v == "" && !all) {
		return &user.ID, nil
	}
	if v == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 1 {
		return nil, httpx.Validation(httpx.Fields{"user_id": i18n.M("validation.positive_integer")})
	}
	return &id, nil
}

// list answers GET /shifts?user_id=&from=&to=: the shifts that overlap those days of the zone of
// whoever asks (from today, a week, by default). An administrator sees everyone's, or those of
// user_id; a user, only theirs.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	userID, err := subject(r, true)
	if err != nil {
		return err
	}
	loc := auth.MustUser(r.Context()).Location()
	rng, err := httpx.ParseDateRange(r, civil.DateOf(time.Now().In(loc)), listDays, maxListDays)
	if err != nil {
		return err
	}
	start, end := civil.DayRange(rng.From, rng.To, loc)
	shifts, err := h.q.ListShifts(r.Context(), store.ListShiftsParams{UserID: userID, Start: start, End: end})
	if err != nil {
		return fmt.Errorf("list shifts: %w", err)
	}
	data := make([]shiftResponse, len(shifts))
	for i, s := range shifts {
		data[i] = toShift(s)
	}
	httpx.Write(w, http.StatusOK, map[string][]shiftResponse{"data": data})
	return nil
}

// clockIn answers POST /time-entries/clock-in: the user starts working now. The entry belongs to
// the shift going on (or starting within the hour), if there is one. If they already clocked in,
// 409 already_clocked_in.
func (h *Handler) clockIn(w http.ResponseWriter, r *http.Request) error {
	user := auth.MustUser(r.Context())
	var shiftID *int64
	if id, err := h.q.CurrentShift(r.Context(), user.ID); err == nil {
		shiftID = &id
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find the current shift: %w", err)
	}

	e, err := h.q.ClockIn(r.Context(), store.ClockInParams{UserID: user.ID, ShiftID: shiftID})
	if _, ok := db.IsUniqueViolation(err); ok {
		return errClockedIn
	}
	if err != nil {
		return fmt.Errorf("clock in: %w", err)
	}
	httpx.Write(w, http.StatusCreated, toEntry(e))
	return nil
}

// clockOut answers POST /time-entries/clock-out: the user stops working now. Without an open
// entry, 409 not_clocked_in.
func (h *Handler) clockOut(w http.ResponseWriter, r *http.Request) error {
	e, err := h.q.ClockOut(r.Context(), auth.MustUser(r.Context()).ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotClockedIn
	}
	if err != nil {
		return fmt.Errorf("clock out: %w", err)
	}
	httpx.Write(w, http.StatusOK, toEntry(e))
	return nil
}

// entries returns the time entries of whoever the request asks for (see subject), in the days of
// ?from=&to= of the worker's zone, with that zone.
func (h *Handler) entries(r *http.Request) ([]store.TimeEntry, httpx.DateRange, *time.Location, error) {
	userID, err := subject(r, false)
	if err != nil {
		return nil, httpx.DateRange{}, nil, err
	}
	loc, err := h.workerZone(r, *userID)
	if err != nil {
		return nil, httpx.DateRange{}, nil, err
	}
	rng, err := httpx.ParseDateRange(r, civil.DateOf(time.Now().In(loc)), listDays, maxListDays)
	if err != nil {
		return nil, httpx.DateRange{}, nil, err
	}
	start, end := civil.DayRange(rng.From, rng.To, loc)
	entries, err := h.q.ListTimeEntries(r.Context(), store.ListTimeEntriesParams{UserID: *userID, Start: start, End: end})
	if err != nil {
		return nil, httpx.DateRange{}, nil, fmt.Errorf("list time entries: %w", err)
	}
	return entries, rng, loc, nil
}

// listEntries answers GET /time-entries?user_id=&from=&to=: the entries of the user (or of user_id,
// for an administrator) in those days of the worker's clock (from today, a week, by default).
func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) error {
	entries, _, _, err := h.entries(r)
	if err != nil {
		return err
	}
	data := make([]entryResponse, len(entries))
	for i, e := range entries {
		data[i] = toEntry(e)
	}
	httpx.Write(w, http.StatusOK, map[string][]entryResponse{"data": data})
	return nil
}

// summary answers GET /time-entries/summary?user_id=&from=&to=: the minutes worked on each day of the
// worker's clock. An entry that crosses midnight counts on both days.
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) error {
	entries, rng, loc, err := h.entries(r)
	if err != nil {
		return err
	}
	worked := make([]Worked, len(entries))
	for i, e := range entries {
		worked[i] = Worked{Start: e.ClockIn}
		if e.ClockOut != nil {
			worked[i].End = *e.ClockOut
		}
	}
	days := SplitByDay(worked, rng.From, rng.To, loc, time.Now())
	total := 0
	for _, d := range days {
		total += d.Minutes
	}
	httpx.Write(w, http.StatusOK, map[string]any{"time_zone": loc.String(), "total_minutes": total, "data": days})
	return nil
}
