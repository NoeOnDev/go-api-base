package db

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/NoeOnDev/go-api-base/migrations"
)

// Migrate aplica las migraciones pendientes que vienen embebidas en el binario.
// goose guarda en la tabla goose_db_version qué migraciones ya se aplicaron,
// así que correrlo en cada arranque es seguro: si no hay pendientes, no hace nada.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	// goose trabaja con database/sql; esta conexión reutiliza el pool de pgx.
	// Cerrarla no cierra el pool.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("preparar migraciones: %w", err)
	}

	results, err := provider.Up(ctx)
	for _, r := range results {
		logger.Info("migración aplicada", "file", filepath.Base(r.Source.Path), "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("aplicar migraciones: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("leer versión del esquema: %w", err)
	}
	logger.Info("esquema actualizado", "version", version, "applied", len(results))
	return nil
}
