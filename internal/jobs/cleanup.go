package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// CleanupArgs is the job that deletes what is no longer useful, so the tables do not grow
// without bound. It runs every hour (see periodicJobs):
//   - the expired sessions and tokens (the queries already ignore them, by expires_at), and the
//     idle sessions if there is an idle expiry;
//   - the notifications read more than notificationRetention ago. Unread ones are left alone;
//   - the keys of the rate limits that already refilled all their attempts (internal/ratelimit);
//   - the real-time messages stored because they did not fit in a NOTIFY (internal/realtime):
//     the other instances read them right away, so they are deleted after realtimeMessageRetention.
type CleanupArgs struct{}

const (
	// notificationRetention is how long a notification is kept after it is read.
	notificationRetention = 90 * 24 * time.Hour
	// realtimeMessageRetention is how long a large real-time message is kept.
	realtimeMessageRetention = time.Minute
)

func (CleanupArgs) Kind() string { return "cleanup" }

// InsertOpts keeps two cleanups from running in the same hour, even if several are queued.
func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q      *store.Queries
	logger *slog.Logger
	idle   time.Duration // idle expiry of the sessions (0: off)
}

func (w *cleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {
	var inactiveBefore time.Time
	if w.idle > 0 {
		inactiveBefore = time.Now().Add(-w.idle)
	}
	sessions, err := w.q.DeleteExpiredSessions(ctx, inactiveBefore)
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	tokens, err := w.q.DeleteExpiredUserTokens(ctx)
	if err != nil {
		return fmt.Errorf("delete expired tokens: %w", err)
	}
	notifications, err := w.q.DeleteReadNotificationsBefore(ctx, time.Now().Add(-notificationRetention))
	if err != nil {
		return fmt.Errorf("delete old notifications: %w", err)
	}
	limits, err := w.q.DeleteExpiredRateLimits(ctx)
	if err != nil {
		return fmt.Errorf("delete refilled rate limits: %w", err)
	}
	messages, err := w.q.DeleteRealtimeMessagesBefore(ctx, time.Now().Add(-realtimeMessageRetention))
	if err != nil {
		return fmt.Errorf("delete real-time messages: %w", err)
	}
	if sessions+tokens+notifications+limits+messages > 0 {
		w.logger.Info("cleanup", "sessions", sessions, "tokens", tokens, "notifications", notifications,
			"rate_limits", limits, "realtime_messages", messages)
	}
	return nil
}
