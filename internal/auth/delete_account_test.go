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

const deleteBody = `{"password":"test-password"}`

func TestDeleteAccount(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	testutil.Do(t, api, cookie, http.MethodPost, "/notes", `{"title":"A note"}`)
	if _, err := pool.Exec(t.Context(), "INSERT INTO notifications (user_id, kind) VALUES ($1, 'x')", u.ID); err != nil {
		t.Fatal(err)
	}

	// With the wrong password nothing happens.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodDelete, "/auth/me", `{"password":"other"}`), http.StatusUnprocessableEntity)
	if countRows(t, pool, "users", u.ID) != 1 {
		t.Fatal("the account should not have been deleted")
	}

	w := testutil.Do(t, api, cookie, http.MethodDelete, "/auth/me", deleteBody)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete the account: status = %d; body: %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.MaxAge >= 0 {
			t.Error("the session cookie should be deleted")
		}
	}

	// All of it is gone, and nothing of Bob's.
	for _, table := range []string{"users", "sessions", "notes", "notifications"} {
		if n := countRows(t, pool, table, u.ID); n != 0 {
			t.Errorf("%d rows left in %s", n, table)
		}
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, bob, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("Bob's account is left alone: status = %d", w.Code)
	}

	// The last email arrives.
	if msg, ok := mails.Last("alice@example.com"); !ok || !strings.Contains(msg.Body, "was deleted") {
		t.Errorf("account deleted email: %+v", mails.Messages())
	}
}

func TestDeleteLastAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, first := testutil.Login(t, pool, "admin1@example.com", auth.RoleAdmin)

	// The only admin cannot be deleted.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, first, http.MethodDelete, "/auth/me", deleteBody), http.StatusConflict)

	// With another admin, it can.
	testutil.Login(t, pool, "admin2@example.com", auth.RoleAdmin)
	if w := testutil.Do(t, api, first, http.MethodDelete, "/auth/me", deleteBody); w.Code != http.StatusNoContent {
		t.Errorf("with another admin: status = %d; body: %s", w.Code, w.Body)
	}
}

// A project table that references the user without CASCADE blocks the deletion: 409, without
// deleting anything halfway.
func TestDeleteAccountBlockedByRestrict(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

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
		t.Error("the account and its session should stay intact")
	}
}

// Deleting the account cuts its GET /events streams.
func TestDeleteAccountCutsStreams(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
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
		t.Fatalf("delete the account: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("the stream should be cut when the account is deleted")
	}
}
