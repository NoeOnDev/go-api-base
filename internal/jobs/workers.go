package jobs

import (
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps son las dependencias que necesitan los workers.
type Deps struct {
	Queries *store.Queries
	Logger  *slog.Logger
}

// registerWorkers registra todos los workers. Cada trabajo nuevo se agrega acá,
// como las rutas en server/routes.go.
func registerWorkers(workers *river.Workers, deps Deps) error {
	if err := river.AddWorkerSafely(workers, &cleanupWorker{q: deps.Queries, logger: deps.Logger}); err != nil {
		return err
	}
	return nil
}

// periodicJobs son los trabajos que se encolan solos cada cierto tiempo (el equivalente
// al scheduler de Laravel). Solo los encola una instancia, la que River elige como líder.
func periodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
			&river.PeriodicJobOpts{ID: "cleanup", RunOnStart: true},
		),
	}
}
