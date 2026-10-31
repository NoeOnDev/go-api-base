// Package appointments is the example of appointments: a user books a time at a branch, within its
// business hours (internal/branches), and gets a reminder the day before. Two bookings of the same
// time cannot both succeed, not even at the same instant on different instances of the API: an
// EXCLUDE constraint of PostgreSQL stops the second one.
//
// An appointment keeps the hour of the branch's clock and its zone, besides the instants (see
// "Modeling schedules" in docs/time.md). Each user sees their own appointments, like notes; an
// administrator sees them all and the agenda of a branch.
package appointments

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/branches"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// statusBooked is an appointment that holds its slot; the other status is "canceled".
const statusBooked = "booked"

// Limits of the ranges of days of GET /appointments.
const (
	listDays    = 30
	maxListDays = 366
)

// Handler groups the appointment endpoints.
type Handler struct {
	db       *pgxpool.Pool
	q        *store.Queries
	jobs     *river.Client[pgx.Tx]
	branches *branches.Handler
}

// NewHandler creates the handler. jobs queues the reminders; without it (in some tests) there are
// none. branches gives the business hours.
func NewHandler(pool *pgxpool.Pool, q *store.Queries, jobs *river.Client[pgx.Tx], b *branches.Handler) *Handler {
	return &Handler{db: pool, q: q, jobs: jobs, branches: b}
}

type appointmentResponse struct {
	ID         int64  `json:"id"`
	BranchID   int64  `json:"branch_id"`
	BranchName string `json:"branch_name"`
	UserID     int64  `json:"user_id"`
	// LocalStartsAt and TimeZone are the hour of the branch's clock: "10:00, Bogotá time".
	LocalStartsAt civil.DateTime `json:"local_starts_at"`
	TimeZone      string         `json:"time_zone"`
	// StartsAt and EndsAt are the instants, to show them in the viewer's zone.
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

func toResponse(a store.Appointment, branchName string) appointmentResponse {
	return appointmentResponse{
		ID: a.ID, BranchID: a.BranchID, BranchName: branchName, UserID: a.UserID,
		LocalStartsAt: a.LocalStartsAt, TimeZone: a.TimeZone, StartsAt: a.StartsAt, EndsAt: a.EndsAt,
		DurationMinutes: int(a.EndsAt.Sub(a.StartsAt) / time.Minute), Status: a.Status, CreatedAt: a.CreatedAt,
	}
}

var (
	errNotFound        = httpx.NotFound("appointment_not_found")
	errBranchNotFound  = httpx.NotFound("branch_not_found")
	errSlotTaken       = httpx.Conflict("slot_taken", "starts_at")
	errSlotUnavailable = httpx.Validation(httpx.Fields{"starts_at": i18n.M("validation.slot_unavailable")})
)

// branchOf finds the branch of the route ({id}) and loads its zone.
func (h *Handler) branchOf(r *http.Request) (store.Branch, *time.Location, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return store.Branch{}, nil, err
	}
	b, err := h.q.GetBranch(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Branch{}, nil, errBranchNotFound
	}
	if err != nil {
		return store.Branch{}, nil, fmt.Errorf("get branch %d: %w", id, err)
	}
	loc, err := civil.LoadLocation(b.TimeZone)
	if err != nil {
		return store.Branch{}, nil, fmt.Errorf("zone of branch %d: %w", b.ID, err)
	}
	return b, loc, nil
}

// freeSlots returns the slots of date at the branch. With booked false, it ignores the appointments
// already booked (to tell "outside the hours" from "taken").
func (h *Handler) freeSlots(r *http.Request, b store.Branch, loc *time.Location, date civil.Date, duration time.Duration, booked bool) ([]Slot, error) {
	// From the day before: an opening that crosses midnight can have slots on date.
	days, _, err := h.branches.Schedule(r.Context(), b, date.AddDays(-1), date)
	if err != nil {
		return nil, err
	}
	var openings []branches.Opening
	for _, d := range days {
		openings = append(openings, d.Openings...)
	}

	var taken []span
	if booked {
		start, end := civil.DayRange(date, date, loc)
		rows, err := h.q.ListBookedRanges(r.Context(), store.ListBookedRangesParams{
			BranchID: b.ID, Start: start, End: end.Add(duration),
		})
		if err != nil {
			return nil, fmt.Errorf("list the booked appointments of branch %d: %w", b.ID, err)
		}
		for _, row := range rows {
			taken = append(taken, span{start: row.StartsAt, end: row.EndsAt})
		}
	}
	return slots(openings, date, duration, time.Now(), taken, loc), nil
}

// availability answers GET /branches/{id}/availability?date=&duration=: the free slots of a day
// (today of the branch by default) for an appointment of duration minutes (30 by default).
func (h *Handler) availability(w http.ResponseWriter, r *http.Request) error {
	b, loc, err := h.branchOf(r)
	if err != nil {
		return err
	}
	date, err := parseDate(r, civil.DateOf(time.Now().In(loc)))
	if err != nil {
		return err
	}
	duration, err := parseDuration(r)
	if err != nil {
		return err
	}
	free, err := h.freeSlots(r, b, loc, date, duration, true)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, map[string]any{
		"date": date, "time_zone": b.TimeZone, "duration_minutes": int(duration / time.Minute), "slots": free,
	})
	return nil
}

// book answers POST /appointments with {branch_id, starts_at, duration_minutes?}. starts_at is the
// hour of the branch's clock and must be one of the slots of its availability: that covers the
// business hours, the past, the steps of 15 minutes and the hours that do not exist when the clocks
// change. If someone else took it first, 409 slot_taken.
func (h *Handler) book(w http.ResponseWriter, r *http.Request) error {
	var in bookInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	b, err := h.q.GetBranch(r.Context(), in.BranchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Validation(httpx.Fields{"branch_id": i18n.M("validation.unknown_branch")})
	}
	if err != nil {
		return fmt.Errorf("get branch %d: %w", in.BranchID, err)
	}
	loc, err := civil.LoadLocation(b.TimeZone)
	if err != nil {
		return fmt.Errorf("zone of branch %d: %w", b.ID, err)
	}

	duration := time.Duration(in.DurationMinutes) * time.Minute
	candidates, err := h.freeSlots(r, b, loc, in.StartsAt.Date(), duration, false)
	if err != nil {
		return err
	}
	var slot *Slot
	for i, s := range candidates {
		if s.LocalStartsAt == in.StartsAt {
			slot = &candidates[i]
			break
		}
	}
	if slot == nil {
		return errSlotUnavailable
	}

	user := auth.MustUser(r.Context())
	var a store.Appointment
	err = pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
		var err error
		a, err = h.q.WithTx(tx).CreateAppointment(r.Context(), store.CreateAppointmentParams{
			BranchID: b.ID, UserID: user.ID, LocalStartsAt: slot.LocalStartsAt, TimeZone: b.TimeZone,
			StartsAt: slot.StartsAt, EndsAt: slot.EndsAt,
		})
		if _, ok := db.IsExclusionViolation(err); ok {
			return errSlotTaken
		}
		if err != nil {
			return fmt.Errorf("create appointment: %w", err)
		}
		// The reminder is queued in the same transaction: if the booking fails, there is none.
		if remindAt := a.StartsAt.Add(-reminderBefore); h.jobs != nil && remindAt.After(time.Now()) {
			args := ReminderArgs{AppointmentID: a.ID, StartsAt: a.StartsAt}
			if _, err := h.jobs.InsertTx(r.Context(), tx, args, &river.InsertOpts{ScheduledAt: remindAt}); err != nil {
				return fmt.Errorf("queue the reminder of appointment %d: %w", a.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	w.Header().Set("Location", fmt.Sprintf("/appointments/%d", a.ID))
	httpx.Write(w, http.StatusCreated, toResponse(a, b.Name))
	return nil
}

// list answers GET /appointments?from=&to=&limit=&offset=: the user's appointments that start on
// those days of their zone (from today, 30 days, by default), canceled ones included.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	user := auth.MustUser(r.Context())
	loc := user.Location()
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	rng, err := httpx.ParseDateRange(r, civil.DateOf(time.Now().In(loc)), listDays, maxListDays)
	if err != nil {
		return err
	}
	start, end := civil.DayRange(rng.From, rng.To, loc)

	rows, err := h.q.ListUserAppointments(r.Context(), store.ListUserAppointmentsParams{
		UserID: user.ID, Start: start, End: end, Limit: page.Limit, Offset: page.Offset,
	})
	if err != nil {
		return fmt.Errorf("list appointments: %w", err)
	}
	total, err := h.q.CountUserAppointments(r.Context(), store.CountUserAppointmentsParams{UserID: user.ID, Start: start, End: end})
	if err != nil {
		return fmt.Errorf("count appointments: %w", err)
	}

	data := make([]appointmentResponse, len(rows))
	for i, row := range rows {
		data[i] = toResponse(row.Appointment, row.BranchName)
	}
	httpx.Write(w, http.StatusOK, httpx.List[appointmentResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// find returns the appointment of the route, if it is the user's or the user is an admin. Someone
// else's answers 404, not 403, so as not to reveal that it exists.
func (h *Handler) find(r *http.Request) (store.GetAppointmentRow, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return store.GetAppointmentRow{}, err
	}
	row, err := h.q.GetAppointment(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.GetAppointmentRow{}, errNotFound
	}
	if err != nil {
		return store.GetAppointmentRow{}, fmt.Errorf("get appointment %d: %w", id, err)
	}
	if user := auth.MustUser(r.Context()); row.Appointment.UserID != user.ID && !user.IsAdmin() {
		return store.GetAppointmentRow{}, errNotFound
	}
	return row, nil
}

// get answers GET /appointments/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	row, err := h.find(r)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, toResponse(row.Appointment, row.BranchName))
	return nil
}

// cancel answers POST /appointments/{id}/cancel: the slot is free again, and the reminder (already
// queued) finds it canceled and does nothing. Canceling it again changes nothing.
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	row, err := h.find(r)
	if err != nil {
		return err
	}
	if err := h.q.CancelAppointment(r.Context(), row.Appointment.ID); err != nil {
		return fmt.Errorf("cancel appointment %d: %w", row.Appointment.ID, err)
	}
	if row, err = h.find(r); err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, toResponse(row.Appointment, row.BranchName))
	return nil
}

// agenda answers GET /branches/{id}/appointments?date=: the appointments of a day of the branch
// (today by default), canceled ones included. Only for administrators.
func (h *Handler) agenda(w http.ResponseWriter, r *http.Request) error {
	b, loc, err := h.branchOf(r)
	if err != nil {
		return err
	}
	date, err := parseDate(r, civil.DateOf(time.Now().In(loc)))
	if err != nil {
		return err
	}
	start, end := civil.DayRange(date, date, loc)
	rows, err := h.q.ListBranchAppointments(r.Context(), store.ListBranchAppointmentsParams{BranchID: b.ID, Start: start, End: end})
	if err != nil {
		return fmt.Errorf("list the appointments of branch %d: %w", b.ID, err)
	}
	data := make([]appointmentResponse, len(rows))
	for i, row := range rows {
		data[i] = toResponse(row.Appointment, row.BranchName)
	}
	httpx.Write(w, http.StatusOK, map[string]any{"date": date, "time_zone": b.TimeZone, "data": data})
	return nil
}
