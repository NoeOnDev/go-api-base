package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
)

// runFresh recreates a local database from the migrations (make fresh): it deletes everything in
// it and applies them again. It is the equivalent of Laravel's migrate:fresh, and what an edited
// migration needs: goose does not apply again one it already applied.
//
//	api fresh        the development database (DATABASE_URL)
//	api fresh -test  the test database (TEST_DATABASE_URL)
//
// It refuses to run in production.
func runFresh(args []string) error {
	fs := flag.NewFlagSet("fresh", flag.ContinueOnError)
	test := fs.Bool("test", false, "recreate the test database (TEST_DATABASE_URL) instead of the development one")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if cfg.IsProduction() {
		return errors.New("fresh deletes the whole database: it does not run with APP_ENV=production")
	}
	url := cfg.DatabaseURL
	if *test {
		if url = os.Getenv("TEST_DATABASE_URL"); url == "" {
			return errors.New("TEST_DATABASE_URL is not set")
		}
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	name := pool.Config().ConnConfig.Database
	if *test && !strings.HasSuffix(name, "_test") {
		return fmt.Errorf("TEST_DATABASE_URL points to the database %q: the test database must end in \"_test\"", name)
	}

	if err := fresh(ctx, pool); err != nil {
		return err
	}
	fmt.Printf("Database %s recreated from the migrations.\n", name)
	return nil
}

// fresh deletes everything in the database and applies the migrations again.
func fresh(ctx context.Context, pool *pgxpool.Pool) error {
	// The public schema with all it has: tables, types, and the extensions the migrations install.
	for _, sql := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("empty the database (%s): %w", sql, err)
		}
	}
	return db.Migrate(ctx, pool, slog.New(slog.DiscardHandler))
}
