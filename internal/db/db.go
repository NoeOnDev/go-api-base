// Package db abre la conexión a PostgreSQL y aplica las migraciones.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open crea un pool de conexiones y comprueba que la base responde.
// El pool reutiliza conexiones entre peticiones y es seguro para usar desde varias goroutines.
// Su tamaño se puede ajustar en la URL, por ejemplo: ?pool_max_conns=10.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("crear pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("conectar a la base de datos: %w", err)
	}

	return pool, nil
}
