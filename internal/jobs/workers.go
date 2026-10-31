package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/appointments"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps are the dependencies the workers need.
type Deps struct {
	Queries *store.Queries
	Logger  *slog.Logger
	// Mailer is the real sender (SMTP or log) the worker sends the queued emails with.
	Mailer mail.Sender
	// Box decrypts the queued emails (see SendEmailArgs).
	Box *secretbox.Box
	// SessionIdleTimeout: the cleanup also deletes the sessions idle for longer than this. 0, only
	// the expired ones.
	SessionIdleTimeout time.Duration
	// TimeZone is the application's (APP_TIME_ZONE), for the periodic jobs at an hour of the clock
	// (DailyAt, WeeklyAt). If nil, UTC.
	TimeZone *time.Location
	// Notifier sends the notifications of the jobs, like the appointment reminder
	// (internal/notifications implements it).
	Notifier Notifier
}

// Notifier sends a notification to a user (see internal/notifications).
type Notifier interface {
	Notify(ctx context.Context, userID int64, kind string, data any) error
}

// registerWorkers registers every worker. Each new job is added here, like the routes in
// server/routes.go.
func registerWorkers(workers *river.Workers, deps Deps) error {
	if err := river.AddWorkerSafely(workers, &cleanupWorker{q: deps.Queries, logger: deps.Logger, idle: deps.SessionIdleTimeout}); err != nil {
		return err
	}
	if err := river.AddWorkerSafely(workers, &sendEmailWorker{sender: deps.Mailer, box: deps.Box}); err != nil {
		return err
	}
	// Example appointments (internal/appointments): the reminder the day before.
	if err := river.AddWorkerSafely(workers, appointments.NewReminderWorker(deps.Queries, deps.Notifier)); err != nil {
		return err
	}
	return nil
}

// periodicJobs are the jobs that queue themselves every so often (the equivalent of Laravel's
// scheduler). Only one instance queues them, the one River elects as leader. For a fixed hour of
// the clock (every day at 8:00), use DailyAt or WeeklyAt with deps.TimeZone.
func periodicJobs(deps Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
			&river.PeriodicJobOpts{ID: "cleanup", RunOnStart: true},
		),
	}
}
