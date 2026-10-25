package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// CleanupArgs es el trabajo que borra lo que ya no sirve, para que las tablas no crezcan sin
// límite. Corre cada hora (ver periodicJobs):
//   - las sesiones y los tokens vencidos (las consultas ya los ignoran, por expires_at), y las
//     sesiones inactivas si hay vencimiento por inactividad;
//   - las notificaciones leídas hace más de notificationRetention. Las no leídas no se tocan.
type CleanupArgs struct{}

// notificationRetention es cuánto se guarda una notificación después de leída.
const notificationRetention = 90 * 24 * time.Hour

func (CleanupArgs) Kind() string { return "cleanup" }

// InsertOpts hace que no haya dos limpiezas en la misma hora, aunque se encolen varias.
func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q      *store.Queries
	logger *slog.Logger
	idle   time.Duration // vencimiento por inactividad de las sesiones (0: desactivado)
}

func (w *cleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {
	var inactiveBefore time.Time
	if w.idle > 0 {
		inactiveBefore = time.Now().Add(-w.idle)
	}
	sessions, err := w.q.DeleteExpiredSessions(ctx, inactiveBefore)
	if err != nil {
		return fmt.Errorf("borrar sesiones vencidas: %w", err)
	}
	tokens, err := w.q.DeleteExpiredUserTokens(ctx)
	if err != nil {
		return fmt.Errorf("borrar tokens vencidos: %w", err)
	}
	notifications, err := w.q.DeleteReadNotificationsBefore(ctx, time.Now().Add(-notificationRetention))
	if err != nil {
		return fmt.Errorf("borrar notificaciones viejas: %w", err)
	}
	if sessions+tokens+notifications > 0 {
		w.logger.Info("limpieza", "sesiones", sessions, "tokens", tokens, "notificaciones", notifications)
	}
	return nil
}
