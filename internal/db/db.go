// Package db abre la conexión a PostgreSQL y aplica las migraciones.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultMaxConns es el tamaño del pool cuando la URL no lo indica. pgx usa 4 (o uno por núcleo),
// que en un VPS chico queda corto: River ocupa una conexión fija para escuchar los trabajos nuevos
// (LISTEN) y comparte el resto con las peticiones y sus workers. Con 10 sigue sobrando lugar en
// PostgreSQL, que admite 100 por defecto.
const defaultMaxConns = 10

// Open crea un pool de conexiones y comprueba que la base responde.
// El pool reutiliza conexiones entre peticiones y es seguro para usar desde varias goroutines.
// Su tamaño se puede ajustar en la URL, por ejemplo: ?pool_max_conns=20 (si no, defaultMaxConns).
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(url)
	if err != nil {
		return nil, fmt.Errorf("crear pool: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
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

// poolConfig lee la URL y, si no indica pool_max_conns, usa al menos defaultMaxConns.
// Se busca el parámetro en el texto porque pgx no informa si vino en la URL: así funciona
// tanto con una URL (postgres://…?pool_max_conns=20) como con "clave=valor".
func poolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = max(cfg.MaxConns, defaultMaxConns)
	}
	return cfg, nil
}
