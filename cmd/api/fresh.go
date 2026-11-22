package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
)

// runFresh recreates a local database from the migrations (make fresh): it deletes everything in
// it and applies them again. It is the equivalent of Laravel's migrate:fresh, and what an edited
// migration needs: goose does not apply again one it already applied.
//
//	api fresh        the development database (DATABASE_URL), which it also seeds (see seed.go)
//	api fresh -test  the test database (TEST_DATABASE_URL)
//
// It refuses to run in production, and against a database that is not in this machine.
func runFresh(args []string) error {
	fs := flag.NewFlagSet("fresh", flag.ContinueOnError)
	test := fs.Bool("test", false, "recreate the test database (TEST_DATABASE_URL) instead of the development one")
	if err := parseFlags(fs, args); err != nil {
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
	var password string
	if *test {
		if url = config.Env("TEST_DATABASE_URL"); url == "" {
			return errors.New("TEST_DATABASE_URL is not set")
		}
	} else if password, err = readPassword("SEED_PASSWORD"); err != nil {
		// Before deleting anything: without the password of the seed, the database would be left
		// empty.
		return err
	}

	ctx := context.Background()
	pool, err := openDevDatabase(ctx, "fresh", url)
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
	if *test {
		return nil
	}
	return seed(ctx, pool, cfg, password, os.Stdout)
}

// openDevDatabase connects to the database of a development command, after checking that it is one
// of this machine (see requireLocalDatabase).
func openDevDatabase(ctx context.Context, command, url string) (*pgxpool.Pool, error) {
	conn, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("the address of the database is not valid: %w", err)
	}
	if err := requireLocalDatabase(command, conn); err != nil {
		return nil, err
	}
	return db.Open(ctx, url)
}

// requireLocalDatabase refuses to run a development command (fresh, seed) against a database of
// another machine: a .env that points to the staging one, with APP_ENV left in development, would
// delete it, or leave in it accounts with a password everyone on the project knows.
//
// A development database on another host (a container named "db") is allowed by naming that host
// in DEV_DATABASE_HOST: it is said on purpose, and only for that one.
func requireLocalDatabase(command string, conn *pgx.ConnConfig) error {
	hosts := []string{conn.Host}
	for _, f := range conn.Fallbacks {
		hosts = append(hosts, f.Host)
	}
	allowed := config.Env("DEV_DATABASE_HOST")
	for _, host := range hosts {
		if isLocalHost(host) || (allowed != "" && host == allowed) {
			continue
		}
		return fmt.Errorf("%s is for a development database, and this one is on the host %q, which is not this machine. "+
			"If it is a development database (a container), set DEV_DATABASE_HOST=%s", command, host, host)
	}
	return nil
}

// isLocalHost reports whether a host of a connection is this machine: localhost, a loopback
// address, or the directory of a Unix socket.
func isLocalHost(host string) bool {
	if host == "" || host == "localhost" || strings.HasPrefix(host, "/") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
