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
	// elapse simula que pasó ese tiempo: corre hacia atrás el momento en que se recargan los intentos.
	elapse := func(d time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE rate_limits SET tat = tat - $1::interval", d); err != nil {
			t.Fatal(err)
		}
	}

	for i := range 3 {
		if !allow("ana@ejemplo.com") {
			t.Fatalf("intento %d rechazado: se permiten 3 seguidos", i+1)
		}
	}
	if allow("ana@ejemplo.com") {
		t.Error("el cuarto intento seguido debería rechazarse")
	}
	if !allow("luis@ejemplo.com") {
		t.Error("otra clave tiene sus propios intentos")
	}

	// Un intento rechazado no gasta nada: pasado every se recarga uno, y uno solo.
	elapse(time.Hour)
	if !allow("ana@ejemplo.com") {
		t.Error("pasado every debería recargarse un intento")
	}
	if allow("ana@ejemplo.com") {
		t.Error("se recarga de a un intento")
	}

	// Otro limitador con la misma clave cuenta por separado.
	other := ratelimit.New(store.New(pool), "email", time.Hour, 1)
	if ok, err := other.Allow(t.Context(), "ana@ejemplo.com"); err != nil || !ok {
		t.Errorf("otro limitador: %v, %v", ok, err)
	}

	// Reset le devuelve todos los intentos.
	if err := l.Reset(t.Context(), "ana@ejemplo.com"); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if !allow("ana@ejemplo.com") {
			t.Fatalf("después de Reset, intento %d rechazado", i+1)
		}
	}

	// En la base, las claves van hasheadas: no queda ningún email.
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
		if len(key) != 32 || bytes.Contains(key, []byte("@ejemplo.com")) {
			t.Errorf("clave guardada sin hashear: %q", key)
		}
	}
}

// Varias instancias gastan intentos de la misma clave a la vez: se permiten exactamente burst.
func TestLimiterConcurrent(t *testing.T) {
	pool := testutil.NewDB(t)
	l := ratelimit.New(store.New(pool), "login", time.Hour, 5)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ok, err := l.Allow(t.Context(), "ana@ejemplo.com")
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
		t.Errorf("se permitieron %d intentos simultáneos, se esperaban 5", n)
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
		t.Fatalf("primer intento: status = %d", w.Code)
	}
	w := do("1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "12" {
		t.Errorf("segundo intento: status = %d, Retry-After = %q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := do("2"); w.Code != http.StatusNoContent {
		t.Errorf("otro usuario: status = %d", w.Code)
	}
}
