package main

import (
	"bytes"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestSeed(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := config.Config{}
	// What the seed creates, by table. An example that is deleted takes its lines from here.
	want := map[string]int{
		"users":        2,
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
