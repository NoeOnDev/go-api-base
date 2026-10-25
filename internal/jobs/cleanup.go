package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// CleanupArgs es el trabajo que borra las sesiones y los tokens vencidos. Corre cada hora
// (ver periodicJobs). Los vencidos ya no sirven, porque las consultas filtran por expires_at;
// borrarlos solo evita que las tablas crezcan sin límite.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "cleanup" }

// InsertOpts hace que no haya dos limpiezas en la misma hora, aunque se encolen varias.
func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q      *store.Queries
	logger *slog.Logger
}

func (w *cleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {
	sessions, err := w.q.DeleteExpiredSessions(ctx)
	if err != nil {
		return fmt.Errorf("borrar sesiones vencidas: %w", err)
	}
	tokens, err := w.q.DeleteExpiredUserTokens(ctx)
	if err != nil {
		return fmt.Errorf("borrar tokens vencidos: %w", err)
	}
	if sessions+tokens > 0 {
		w.logger.Info("limpieza", "sesiones", sessions, "tokens", tokens)
	}
	return nil
}
