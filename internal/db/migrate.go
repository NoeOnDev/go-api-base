package db

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/NoeOnDev/go-api-base/migrations"
)

// Migrate applies the pending migrations embedded in the binary.
// goose records in the goose_db_version table which migrations were applied, so running it on
// every start is safe: with nothing pending, it does nothing.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	// goose works with database/sql. It gets its own connections, with the pool's configuration
	// but without statement_timeout: a migration can take longer than a request (an index on a
	// large table). The pool sets it in AfterConnect, which these connections do not have, and a
	// statement_timeout in the URL is removed here.
	connConfig := pool.Config().ConnConfig.Copy()
	delete(connConfig.RuntimeParams, "statement_timeout")
	sqlDB := stdlib.OpenDB(*connConfig)
	defer sqlDB.Close()

	// The lock (a PostgreSQL advisory lock) keeps two processes from migrating at the same time,
	// for example two instances starting together or the tests of several packages in parallel.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("prepare the migration lock: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("prepare the migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	for _, r := range results {
		logger.Info("migration applied", "file", filepath.Base(r.Source.Path), "duration", r.Duration.String())
	}
	if err != nil {
		return fmt.Errorf("apply the migrations: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}
	logger.Info("schema up to date", "version", version, "applied", len(results))
	return nil
}
