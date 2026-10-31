// Package jobs runs background jobs with River, a queue stored in PostgreSQL (see docs/jobs.md).
//
// The jobs survive a restart, are retried if they fail and can be scheduled or repeated every
// so often. The workers run inside the same process as the API: it is still a single binary to
// deploy.
package jobs

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
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
	})
	if err != nil {
		return nil, fmt.Errorf("create the River client: %w", err)
	}
	return client, nil
}
