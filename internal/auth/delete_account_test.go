package auth_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const deleteBody = `{"password":"contraseña-de-prueba"}`

func TestDeleteAccount(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	testutil.Do(t, api, cookie, http.MethodPost, "/notes", `{"title":"Una nota"}`)
	if _, err := pool.Exec(t.Context(), "INSERT INTO notifications (user_id, kind) VALUES ($1, 'x')", u.ID); err != nil {
		t.Fatal(err)
	}

	// Con la contraseña incorrecta no pasa nada.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodDelete, "/auth/me", `{"password":"otra"}`), http.StatusUnprocessableEntity)
	if countRows(t, pool, "users", u.ID) != 1 {
		t.Fatal("la cuenta no debería haberse borrado")
	}

	w := testutil.Do(t, api, cookie, http.MethodDelete, "/auth/me", deleteBody)
	if w.Code != http.StatusNoContent {
		t.Fatalf("borrar la cuenta: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.MaxAge >= 0 {
			t.Error("la cookie de sesión debería borrarse")
		}
	}

	// Se fue todo lo suyo, y nada de Luis.
	for _, table := range []string{"users", "sessions", "notes", "notifications"} {
		if n := countRows(t, pool, table, u.ID); n != 0 {
			t.Errorf("quedaron %d filas en %s", n, table)
		}
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, luis, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la cuenta de Luis no se toca: status = %d", w.Code)
	}

	// Le llega el último correo.
	if msg, ok := mails.Last("ana@ejemplo.com"); !ok || !strings.Contains(msg.Body, "was deleted") {
		t.Errorf("correo de cuenta eliminada: %+v", mails.Messages())
	}
}

func TestDeleteLastAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, first := testutil.Login(t, pool, "admin1@ejemplo.com", auth.RoleAdmin)

	// El único admin no puede borrarse.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, first, http.MethodDelete, "/auth/me", deleteBody), http.StatusConflict)

	// Con otro admin, sí.
	testutil.Login(t, pool, "admin2@ejemplo.com", auth.RoleAdmin)
	if w := testutil.Do(t, api, first, http.MethodDelete, "/auth/me", deleteBody); w.Code != http.StatusNoContent {
		t.Errorf("con otro admin: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

// Una tabla de un proyecto que referencia al usuario sin CASCADE impide borrarlo: 409, sin
// borrar nada a medias.
func TestDeleteAccountBlockedByRestrict(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE test_invoices (id bigint PRIMARY KEY, user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT)")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS test_invoices") })
	exec("INSERT INTO test_invoices VALUES (1, $1)", u.ID)

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodDelete, "/auth/me", deleteBody), http.StatusConflict)
	if countRows(t, pool, "users", u.ID) != 1 || countRows(t, pool, "sessions", u.ID) != 1 {
		t.Error("la cuenta y su sesión deberían seguir intactas")
	}
}

// Borrar la cuenta corta sus streams de GET /events.
func TestDeleteAccountCutsStreams(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	events := testutil.OpenEvents(t, srv, cookie, "")

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+"/auth/me", strings.NewReader(deleteBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("borrar la cuenta: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("el stream debería cortarse al borrar la cuenta")
	}
}
