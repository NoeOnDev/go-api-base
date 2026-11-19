// Package jobs runs background jobs with River, a queue stored in PostgreSQL (see docs/jobs.md).
//
// The jobs survive a restart, are retried if they fail and can be scheduled or repeated every
// so often. The workers run inside the same process as the API: it is still a single binary to
// deploy.
package jobs

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// Client is the River client: it queues jobs (Insert, InsertTx) and runs them.
type Client = river.Client[pgx.Tx]

// Queues. Each one has its own worker limit: a slow job in one queue does not hold back the
// others.
const (
	// QueueEmail has few workers because each one opens an SMTP connection, and providers reject
	// many simultaneous connections.
	QueueEmail = "email"
	// emailWorkers is how many emails are sent at once.
	emailWorkers = 4
)

// Config configures the client.
type Config struct {
	// MaxWorkers is how many jobs of the default queue run at once.
	MaxWorkers int
}

// New creates the client with every worker registered (see workers.go). It uses the same pool
// as the application. To run jobs, call Start; without Start, the client still works to queue
// them.
func New(pool *pgxpool.Pool, logger *slog.Logger, cfg Config, deps Deps) (*Client, error) {
	if deps.TimeZone == nil {
		deps.TimeZone = time.UTC
	}
	workers := river.NewWorkers()
	if err := registerWorkers(workers, deps); err != nil {
		return nil, fmt.Errorf("register workers: %w", err)
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: logger,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: max(1, cfg.MaxWorkers)},
			QueueEmail:         {MaxWorkers: emailWorkers},
		},
		Workers:      workers,
		PeriodicJobs: periodicJobs(deps),
		ErrorHandler: errorLogger{logger: cmp.Or(logger, slog.Default())},
	})
	if err != nil {
		return nil, fmt.Errorf("create the River client: %w", err)
	}
	return client, nil
}

// errorLogger logs the jobs that fail. River logs them at info, among everything else: here a
// failed attempt is a warning, and the last one an error, because after it the job is discarded
// and what it had to do (send an email) will not happen.
type errorLogger struct{ logger *slog.Logger }

func (l errorLogger) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	l.log(ctx, job, "error", err)
	return nil
}

func (l errorLogger) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	l.log(ctx, job, "panic", panicVal, "stack", trace)
	return nil
}

func (l errorLogger) log(ctx context.Context, job *rivertype.JobRow, args ...any) {
	args = append(args, "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempt, "max_attempts", job.MaxAttempts)
	if job.Attempt >= job.MaxAttempts {
		l.logger.ErrorContext(ctx, "job failed on its last attempt; it is discarded", args...)
		return
	}
	l.logger.WarnContext(ctx, "job failed; it will be retried", args...)
}
