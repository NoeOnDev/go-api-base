// Package db opens the PostgreSQL connection and applies the migrations.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultMaxConns is the pool size when the URL does not set it. pgx uses 4 (or one per core),
// which falls short on a small VPS: River holds one connection to listen for new jobs (LISTEN)
// and shares the rest with the requests and its workers. With 10 there is still plenty of room
// in PostgreSQL, which allows 100 by default.
const defaultMaxConns = 10

// defaultStatementTimeout is how long a statement can run before PostgreSQL cancels it, when the
// URL does not set statement_timeout. Without it, a slow query keeps its connection even after
// the request that started it gave up (WriteTimeout is 30 s): a few of them use up the pool.
// Whatever legitimately takes longer (a report, a job) raises it in its own transaction with
// SET LOCAL statement_timeout. The migrations do not have it (see Migrate).
const defaultStatementTimeout = "10s"

// Open creates a connection pool and checks that the database answers.
// The pool reuses connections across requests and is safe to use from several goroutines.
// Its size can be set in the URL, for example: ?pool_max_conns=20 (otherwise defaultMaxConns),
// and so can the statement limit: ?statement_timeout=30000 in milliseconds, or 0 to turn it off
// (otherwise defaultStatementTimeout).
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(url)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the database: %w", err)
	}

	return pool, nil
}

// poolConfig reads the URL and, if it does not set pool_max_conns, uses at least
// defaultMaxConns. The parameter is looked up in the text because pgx does not say whether it
// came in the URL: that way it works both with a URL (postgres://…?pool_max_conns=20) and with
// "key=value".
//
// If the URL does not set statement_timeout, each new connection runs SET statement_timeout. A
// statement_timeout in the URL travels as a startup parameter instead, which PgBouncer rejects
// unless it is configured to accept it; SET works with it too.
func poolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = max(cfg.MaxConns, defaultMaxConns)
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET statement_timeout = '"+defaultStatementTimeout+"'")
			return err
		}
	}
	return cfg, nil
}
