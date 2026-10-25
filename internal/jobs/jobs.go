// Package jobs ejecuta trabajos en segundo plano con River, una cola guardada en PostgreSQL
// (ver docs/jobs.md).
//
// Los trabajos sobreviven a un reinicio, se reintentan si fallan y se pueden programar o repetir
// cada cierto tiempo. Los workers corren dentro del mismo proceso que la API: sigue siendo un
// solo binario que desplegar.
package jobs

import (
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Client es el cliente de River: encola trabajos (Insert, InsertTx) y los ejecuta.
type Client = river.Client[pgx.Tx]

// Colas. Cada una tiene su propio límite de workers: un trabajo lento en una cola
// no frena a las demás.
const (
	// QueueEmail tiene pocos workers porque cada uno abre una conexión SMTP, y los
	// proveedores rechazan muchas conexiones simultáneas.
	QueueEmail = "email"
	// emailWorkers es cuántos correos se envían a la vez.
	emailWorkers = 4
)

// Config configura el cliente.
type Config struct {
	// MaxWorkers es cuántos trabajos de la cola default corren a la vez.
	MaxWorkers int
}

// New crea el cliente con todos los workers registrados (ver workers.go). Usa el mismo
// pool que la aplicación. Para ejecutar trabajos hay que llamar a Start; sin Start,
// el cliente igual sirve para encolarlos.
func New(pool *pgxpool.Pool, logger *slog.Logger, cfg Config, deps Deps) (*Client, error) {
	workers := river.NewWorkers()
	if err := registerWorkers(workers, deps); err != nil {
		return nil, fmt.Errorf("registrar workers: %w", err)
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger: logger,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: max(1, cfg.MaxWorkers)},
			QueueEmail:         {MaxWorkers: emailWorkers},
		},
		Workers:      workers,
		PeriodicJobs: periodicJobs(),
	})
	if err != nil {
		return nil, fmt.Errorf("crear cliente de River: %w", err)
	}
	return client, nil
}
