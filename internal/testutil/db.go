// Package testutil has helpers for the integration tests.
package testutil

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/server"
)

// NewDB connects to the test database (TEST_DATABASE_URL), applies the migrations and empties
// every table, so each test starts from scratch.
//
// If TEST_DATABASE_URL is not set, the test is skipped instead of failing: that way the unit
// tests still run on a machine without a database.
//
// Since it empties every table, it refuses a database whose name does not end in "_test" or
// that is the same as DATABASE_URL (see checkTestDatabase).
//
// The packages share the test database, so they must run one at a time: go test -p 1 ./...
// (which is what make test does). Within a package, the tests that use the database must not
// call t.Parallel().
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if requireTestDatabase(os.Getenv) {
			t.Fatal("TEST_DATABASE_URL is required in CI: without it, the integration tests do not run")
		}
		t.Skip("TEST_DATABASE_URL is not set: skipping the integration test")
	}
	if err := checkTestDatabase(url, os.Getenv("DATABASE_URL")); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var name string
		err := row.Scan(&name)
		return pgx.Identifier{name}.Sanitize(), err
	})
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	if len(tables) > 0 {
		sql := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("vaciar tablas: %v", err)
		}
	}

	return pool
}

// Password is the password of the users that Login creates.
const Password = "test-password"

// Login creates a user with the given role, starts a session for it and returns the user and the
// cookie to send in the requests: r.AddCookie(cookie). Its password is Password.
//
// It takes the same options as NewAPI: if the test API changes the cookie settings (for
// example, WithConfig with CookieSecure), pass the same ones to Login so the cookie matches.
func Login(t *testing.T, pool *pgxpool.Pool, email, role string, opts ...Option) (auth.User, *http.Cookie) {
	t.Helper()

	var d server.Deps
	for _, opt := range opts {
		opt(&d)
	}
	service := auth.NewService(pool, auth.Options{
		SessionTTL:   d.Config.SessionTTL,
		CookieSecure: d.Config.CookieSecure,
	})
	u, err := service.CreateUser(t.Context(), auth.RegisterInput{
		Name:     "Test User",
		Email:    email,
		Password: Password,
	}, role)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}

	w := httptest.NewRecorder()
	if err := service.StartSession(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil), u.ID); err != nil {
		t.Fatalf("start the session of %s: %v", email, err)
	}

	// In production the cookie has the __Host- prefix (see auth.CookieName).
	for _, c := range w.Result().Cookies() {
		if strings.TrimPrefix(c.Name, "__Host-") == auth.CookieName {
			return u, c
		}
	}
	t.Fatal("no session cookie was created")
	return auth.User{}, nil
}

// requireTestDatabase reports whether a missing TEST_DATABASE_URL is an error. In CI it is:
// otherwise CI would report everything "ok" without testing anything against the database.
// GitHub Actions (and almost every CI service) sets CI=true.
func requireTestDatabase(getenv func(string) string) bool {
	ci, _ := strconv.ParseBool(getenv("CI"))
	return ci
}

// checkTestDatabase keeps the tests from deleting real data because of a configuration mistake
// (for example, copying DATABASE_URL into TEST_DATABASE_URL). It requires the test database to
// be named "<something>_test" and not to be the database the application uses.
func checkTestDatabase(testURL, appURL string) error {
	testCfg, err := pgx.ParseConfig(testURL)
	if err != nil {
		return fmt.Errorf("invalid TEST_DATABASE_URL: %w", err)
	}

	if !strings.HasSuffix(testCfg.Database, "_test") {
		return fmt.Errorf("TEST_DATABASE_URL points to the database %q: for safety, the test database must end in \"_test\" "+
			"(the tests empty all its tables)", testCfg.Database)
	}

	if appURL == "" {
		return nil
	}
	appCfg, err := pgx.ParseConfig(appURL)
	if err != nil {
		return nil // the app URL is not the tests' business; config.Load validates it
	}
	if testCfg.Host == appCfg.Host && testCfg.Port == appCfg.Port && testCfg.Database == appCfg.Database {
		return fmt.Errorf("TEST_DATABASE_URL and DATABASE_URL point to the same database (%s): the tests would empty it", testCfg.Database)
	}
	return nil
}
