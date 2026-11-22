package main

import (
	"bytes"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestSeed(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := config.Config{}
	// What the seed creates, by table. An example that is deleted takes its lines from here.
	want := map[string]int{
		"users":        3, // the third one, with the role of the catalog
		"roles":        3, // user, admin and the one of the catalog
		"categories":   3,
		"products":     8,
		"notes":        3,
		"branches":     1,
		"branch_hours": 6,
	}
	counts := func() map[string]int {
		t.Helper()
		got := map[string]int{}
		for table := range want {
			var n int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			got[table] = n
		}
		return got
	}

	var out bytes.Buffer
	if err := seed(t.Context(), pool, cfg, testutil.Password, &out); err != nil {
		t.Fatal(err)
	}
	if first := counts(); !maps.Equal(first, want) {
		t.Errorf("seeded %v, want %v", first, want)
	}
	if !strings.Contains(out.String(), seedAdminEmail) || strings.Contains(out.String(), testutil.Password) {
		t.Errorf("output: %s", out.String())
	}

	// Running it again adds nothing, and leaves what is there.
	out.Reset()
	if err := seed(t.Context(), pool, cfg, "another-password-for-the-second-run", &out); err != nil {
		t.Fatal(err)
	}
	if second := counts(); !maps.Equal(second, want) {
		t.Errorf("a second run changed the data: %v, want %v", second, want)
	}
	if got := strings.Count(out.String(), "already there"); got != len(seeders) {
		t.Errorf("the second run should find everything there:\n%s", out.String())
	}

	// The accounts work, with the password of the first run, and their email is verified: they can
	// use the API with REQUIRE_EMAIL_VERIFICATION too.
	api := testutil.NewAPI(pool)
	for email, role := range map[string]string{seedAdminEmail: auth.RoleAdmin, seedUserEmail: auth.RoleUser} {
		body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, testutil.Password)
		user := testutil.Decode[auth.User](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/login", body), http.StatusOK)
		if user.Role != role || !user.EmailVerified() {
			t.Errorf("%s: role = %q, verified = %v; want %q and verified", email, user.Role, user.EmailVerified(), role)
		}
	}

	// A password the API would not accept is not accepted here either.
	testutil.NewDB(t)
	if err := seed(t.Context(), pool, cfg, "password", &out); err == nil || !strings.Contains(err.Error(), "too common") {
		t.Errorf("seed with a common password: %v", err)
	}
}

// fresh leaves the database as the migrations create it, whatever it had.
func TestFresh(t *testing.T) {
	pool := testutil.NewDB(t)
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	// What an older version of a migration left behind, and a row of the tests.
	if _, err := pool.Exec(t.Context(), "CREATE TABLE left_behind (id bigint PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	if err := fresh(t.Context(), pool); err != nil {
		t.Fatal(err)
	}

	var leftBehind bool
	var users, roles int
	err := pool.QueryRow(t.Context(), `SELECT to_regclass('public.left_behind') IS NOT NULL,
		(SELECT count(*) FROM users), (SELECT count(*) FROM roles)`).Scan(&leftBehind, &users, &roles)
	if err != nil {
		t.Fatal(err)
	}
	if leftBehind || users != 0 || roles != 2 {
		t.Errorf("after fresh: left_behind = %v, %d users, %d roles; want a schema with only the two roles of the template", leftBehind, users, roles)
	}
}

// A seed of the catalog that was cut in the middle (its role or its account were not created, or its
// categories were deleted) is completed by the next one, instead of failing or saying it is all
// there.
func TestSeedCatalogIsCompleted(t *testing.T) {
	pool := testutil.NewDB(t)
	var out bytes.Buffer
	if err := seed(t.Context(), pool, config.Config{}, testutil.Password, &out); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"DELETE FROM users WHERE email = '" + seedManagerEmail + "'",
		"DELETE FROM products",
		"DELETE FROM categories",
	} {
		if _, err := pool.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}

	if err := seed(t.Context(), pool, config.Config{}, testutil.Password, &out); err != nil {
		t.Fatalf("the second seed: %v", err)
	}
	var categories, managers, roles int
	err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM categories),
		(SELECT count(*) FROM users WHERE role = $1), (SELECT count(*) FROM roles WHERE key = $1)`, seedManagerRole).Scan(&categories, &managers, &roles)
	if err != nil {
		t.Fatal(err)
	}
	if categories != 3 || managers != 1 || roles != 1 {
		t.Errorf("after the second seed: %d categories, %d managers, %d roles; want 3, 1 and 1", categories, managers, roles)
	}
}

// fresh and seed are for a database of this machine: with a .env that points somewhere else, they
// would delete a database that is not a development one.
func TestDevelopmentCommandsNeedALocalDatabase(t *testing.T) {
	for url, local := range map[string]bool{
		"postgres://app:x@localhost:5432/app":        true,
		"postgres://app:x@127.0.0.1/app":             true,
		"postgres://app:x@[::1]/app":                 true,
		"postgres:///app?host=/var/run/postgresql":   true,
		"postgres://app:x@db.example.com/app":        false,
		"postgres://app:x@10.0.0.5/app":              false,
		"postgres://app:x@localhost,db.example/app":  false, // one of its hosts is not
		"postgres://app:x@db/app":                    false,
		"postgres://app:x@localhost.example.com/app": false,
	} {
		conn, err := pgx.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		if err := requireLocalDatabase("fresh", conn); (err == nil) != local {
			t.Errorf("%s: local = %v, want %v (%v)", url, err == nil, local, err)
		}
	}

	// A development database in a container is allowed by naming its host, and only that one.
	t.Setenv("DEV_DATABASE_HOST", "db")
	for url, allowed := range map[string]bool{"postgres://app:x@db/app": true, "postgres://app:x@db.example.com/app": false} {
		conn, err := pgx.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		if err := requireLocalDatabase("seed", conn); (err == nil) != allowed {
			t.Errorf("with DEV_DATABASE_HOST=db, %s: allowed = %v, want %v", url, err == nil, allowed)
		}
	}
}

// A password in a .env can be in quotes, which make keeps: they are not part of it.
func TestPasswordFromTheEnvironment(t *testing.T) {
	t.Setenv("SEED_PASSWORD", `"a password with spaces"`)
	if got, err := readPassword("SEED_PASSWORD"); err != nil || got != "a password with spaces" {
		t.Errorf("readPassword = %q, %v", got, err)
	}
}
