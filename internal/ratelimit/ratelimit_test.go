package ratelimit_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestLimiter(t *testing.T) {
	pool := testutil.NewDB(t)
	l := ratelimit.New(store.New(pool), "login", time.Hour, 3)
	allow := func(key string) bool {
		t.Helper()
		ok, err := l.Allow(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// elapse simulates that that much time passed: it moves back the moment the attempts refill.
	elapse := func(d time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE rate_limits SET tat = tat - $1::interval", d); err != nil {
			t.Fatal(err)
		}
	}

	for i := range 3 {
		if !allow("alice@example.com") {
			t.Fatalf("attempt %d rejected: 3 in a row are allowed", i+1)
		}
	}
	if allow("alice@example.com") {
		t.Error("the fourth attempt in a row should be rejected")
	}
	if !allow("bob@example.com") {
		t.Error("another key has its own attempts")
	}

	// A rejected attempt spends nothing: after every, one attempt refills, and only one.
	elapse(time.Hour)
	if !allow("alice@example.com") {
		t.Error("after every, one attempt should refill")
	}
	if allow("alice@example.com") {
		t.Error("it refills one attempt at a time")
	}

	// Another limiter with the same key counts separately.
	other := ratelimit.New(store.New(pool), "email", time.Hour, 1)
	if ok, err := other.Allow(t.Context(), "alice@example.com"); err != nil || !ok {
		t.Errorf("another limiter: %v, %v", ok, err)
	}

	// Reset gives back all the attempts.
	if err := l.Reset(t.Context(), "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if !allow("alice@example.com") {
			t.Fatalf("after Reset, attempt %d rejected", i+1)
		}
	}

	// In the database, the keys are hashed: no email is left.
	rows, err := pool.Query(t.Context(), "SELECT key FROM rate_limits")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		if len(key) != 32 || bytes.Contains(key, []byte("@example.com")) {
			t.Errorf("key stored without hashing: %q", key)
		}
	}
}

// Several instances spend attempts of the same key at once: exactly burst are allowed.
func TestLimiterConcurrent(t *testing.T) {
	pool := testutil.NewDB(t)
	l := ratelimit.New(store.New(pool), "login", time.Hour, 5)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ok, err := l.Allow(t.Context(), "alice@example.com")
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 5 {
		t.Errorf("%d simultaneous attempts were allowed, want 5", n)
	}
}

func TestLimiterMiddleware(t *testing.T) {
	pool := testutil.NewDB(t)
	l := ratelimit.New(store.New(pool), "account", 12*time.Second, 1)
	h := l.Middleware(func(r *http.Request) string { return r.Header.Get("X-User") })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	do := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("1"); w.Code != http.StatusNoContent {
		t.Fatalf("first attempt: status = %d", w.Code)
	}
	w := do("1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "12" {
		t.Errorf("second attempt: status = %d, Retry-After = %q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := do("2"); w.Code != http.StatusNoContent {
		t.Errorf("another user: status = %d", w.Code)
	}
}
