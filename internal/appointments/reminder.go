package appointments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// reminderBefore is how long before the appointment the reminder arrives.
const reminderBefore = 24 * time.Hour

// NotifyReminder is the kind of the reminder notification ("notifications.appointment_reminder" in
// the catalogs).
const NotifyReminder = "appointment_reminder"

// ReminderArgs is the job that reminds a user of their appointment, queued when booking for 24 hours
// before it (see docs/jobs.md). StartsAt is the instant it was queued for: if the appointment
// changed, the job does nothing.
type ReminderArgs struct {
	AppointmentID int64     `json:"appointment_id"`
	StartsAt      time.Time `json:"starts_at"`
}

// Kind is the job's name in the database. Do not change it after deploying.
func (ReminderArgs) Kind() string { return "appointment_reminder" }

// Notifier sends a notification to a user (internal/notifications implements it).
type Notifier interface {
	Notify(ctx context.Context, userID int64, kind string, data any) error
}

// ReminderWorker sends the reminder as a notification: stored, and live if the user is connected.
// internal/jobs registers it (see registerWorkers).
type ReminderWorker struct {
	river.WorkerDefaults[ReminderArgs]
	q        *store.Queries
	notifier Notifier
}

// NewReminderWorker creates the worker.
func NewReminderWorker(q *store.Queries, notifier Notifier) *ReminderWorker {
	return &ReminderWorker{q: q, notifier: notifier}
}

// Work sends the reminder, if the appointment is still booked at the same time. It can run twice
// (a retry), so it checks first instead of trusting the arguments.
func (w *ReminderWorker) Work(ctx context.Context, job *river.Job[ReminderArgs]) error {
	if w.notifier == nil {
		return errors.New("appointment reminder: no notifier")
	}
	row, err := w.q.GetAppointment(ctx, job.Args.AppointmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted with the user's account
	}
	if err != nil {
		return fmt.Errorf("find appointment %d: %w", job.Args.AppointmentID, err)
	}
	a := row.Appointment
	if a.Status != statusBooked || !a.StartsAt.Equal(job.Args.StartsAt) {
		return nil
	}
	// The day and hour of the branch's clock: the appointment is there. The text writes them in the
	// reader's format ({date:date}, {time:time}); the frontend has starts_at to show it in another
	// zone.
	return w.notifier.Notify(ctx, a.UserID, NotifyReminder, map[string]any{
		"appointment_id": a.ID,
		"branch":         row.BranchName,
		"date":           a.LocalStartsAt.Date().String(),
		"time":           a.LocalStartsAt.Time().String(),
		"time_zone":      a.TimeZone,
		"starts_at":      a.StartsAt,
	})
}
