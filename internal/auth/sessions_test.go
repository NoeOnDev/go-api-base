package auth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type sessionItem struct {
	ID         int64     `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

type sessionList struct {
	Data []sessionItem `json:"data"`
}

// loginFrom signs in through the API from a "device": a browser and an IP.
func loginFrom(t *testing.T, api http.Handler, email, userAgent, ip string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"test-password"}`, email)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", userAgent)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d; body: %s", w.Code, w.Body)
	}
	return sessionCookie(t, w)
}

func listSessions(t *testing.T, api http.Handler, cookie *http.Cookie) []sessionItem {
	t.Helper()
	return testutil.Decode[sessionList](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/sessions", ""), http.StatusOK).Data
}

// otherSession returns the session of the list that is not the current one.
func otherSession(t *testing.T, sessions []sessionItem) sessionItem {
	t.Helper()
	for _, s := range sessions {
		if !s.Current {
			return s
		}
	}
	t.Fatalf("there is no other session: %+v", sessions)
	return sessionItem{}
}

func TestListSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	laptop := loginFrom(t, api, "alice@example.com", "Firefox on the laptop", "198.51.100.1")
	loginFrom(t, api, "alice@example.com", "Safari on the phone", "198.51.100.2")
	loginFrom(t, api, "bob@example.com", "Bob's Chrome", "198.51.100.3")

	sessions := listSessions(t, api, laptop)
	// Alice's two through the API, plus the testutil.Login one. None of Bob's.
	if len(sessions) != 3 {
		t.Fatalf("Alice's sessions: %+v", sessions)
	}
	byAgent := map[string]sessionItem{}
	for _, s := range sessions {
		byAgent[s.UserAgent] = s
		if strings.Contains(s.UserAgent, "Bob") {
			t.Errorf("a session of another user shows up: %+v", s)
		}
	}
	if s := byAgent["Firefox on the laptop"]; !s.Current || s.IP != "198.51.100.1" {
		t.Errorf("the current session: %+v", s)
	}
	if s := byAgent["Safari on the phone"]; s.Current || s.IP != "198.51.100.2" {
		t.Errorf("the other session: %+v", s)
	}
}

func TestRevokeSession(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	phone := loginFrom(t, api, "alice@example.com", "Safari on the phone", "198.51.100.2")

	sessions := listSessions(t, api, alice)
	phoneID := otherSession(t, sessions).ID
	var currentID int64
	for _, s := range sessions {
		if s.Current {
			currentID = s.ID
		}
	}

	// Another user's session does not exist for Alice.
	bobID := listSessions(t, api, bob)[0].ID
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", bobID), ""), http.StatusNotFound)
	// The current one is closed with logout.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", currentID), ""), http.StatusUnprocessableEntity)

	if w := testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", phoneID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("close the phone session: status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, alice, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the current session should stay: status = %d", w.Code)
	}
	if w := testutil.Do(t, api, bob, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("Bob's session is left alone: status = %d", w.Code)
	}
}

// Closing a session cuts its GET /events streams right away.
func TestRevokeSessionCutsItsStream(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, laptop := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	send := func(cookie *http.Cookie, method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	var phone *http.Cookie
	for _, c := range send(nil, http.MethodPost, "/auth/login", loginBody).Cookies() {
		if c.Name == auth.CookieName {
			phone = c
		}
	}
	events := testutil.OpenEvents(t, srv, phone, "")

	var list sessionList
	if err := json.NewDecoder(send(laptop, http.MethodGet, "/auth/sessions", "").Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if resp := send(laptop, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", otherSession(t, list.Data).ID), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("close the session: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("the stream of the closed session should be cut")
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, laptop := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	phone := loginFrom(t, api, "alice@example.com", "Safari", "198.51.100.2")
	tablet := loginFrom(t, api, "alice@example.com", "Chrome", "198.51.100.3")

	// It asks for the password.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, laptop, http.MethodPost, "/auth/sessions/revoke-others", `{"password":"wrong-password"}`), http.StatusUnprocessableEntity)
	if w := testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Fatalf("with the wrong password nothing is closed: status = %d", w.Code)
	}

	if w := testutil.Do(t, api, laptop, http.MethodPost, "/auth/sessions/revoke-others", `{"password":"test-password"}`); w.Code != http.StatusNoContent {
		t.Fatalf("revoke-others: status = %d; body: %s", w.Code, w.Body)
	}
	for _, c := range []*http.Cookie{phone, tablet} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, c, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	}
	if sessions := listSessions(t, api, laptop); len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("only the current one should be left: %+v", sessions)
	}
}

// The last activity is saved at most every 5 minutes, not on every request.
func TestLastSeenIsThrottled(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	setLastSeen := func(ago time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE sessions SET last_seen_at = now() - $1::interval WHERE user_id = $2", fmt.Sprintf("%d seconds", int(ago.Seconds())), u.ID); err != nil {
			t.Fatal(err)
		}
	}

	setLastSeen(2 * time.Minute)
	testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", "")
	if ago := lastSeenAgo(t, pool, u.ID); ago < time.Minute {
		t.Errorf("with 2 minutes it should not be updated: %v ago", ago)
	}

	setLastSeen(10 * time.Minute)
	testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", "")
	if ago := lastSeenAgo(t, pool, u.ID); ago > time.Minute {
		t.Errorf("with 10 minutes it should be updated: %v ago", ago)
	}
}

func lastSeenAgo(t *testing.T, pool *pgxpool.Pool, userID int64) time.Duration {
	t.Helper()
	var seconds float64
	if err := pool.QueryRow(t.Context(), "SELECT extract(epoch FROM now() - last_seen_at) FROM sessions WHERE user_id = $1", userID).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	return time.Duration(seconds * float64(time.Second))
}

func TestSessionIdleTimeout(t *testing.T) {
	pool := testutil.NewDB(t)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	setLastSeen := func(ago string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE sessions SET last_seen_at = now() - $1::interval WHERE user_id = $2", ago, u.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Without an idle expiry (the default), a session idle for an hour still works.
	setLastSeen("1 hour")
	if w := testutil.Do(t, testutil.NewAPI(pool), cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Fatalf("without an expiry: status = %d", w.Code)
	}

	idle := testutil.WithConfig(func(c *config.Config) { c.SessionIdleTimeout = 30 * time.Minute })
	api := testutil.NewAPI(pool, idle)

	// With 30 minutes: active 10 minutes ago, it works; idle for an hour, it does not.
	setLastSeen("10 minutes")
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("active 10 minutes ago: status = %d", w.Code)
	}
	setLastSeen("1 hour")
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
}

// With an idle expiry, the list does not show the sessions that expired that way, even if the
// periodic cleanup has not deleted them yet: they can no longer be used to sign in.
func TestListSessionsHidesIdleSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	newSession(t, pool, u.ID) // another device

	api := testutil.NewAPI(pool)
	phoneID := otherSession(t, listSessions(t, api, cookie)).ID
	if _, err := pool.Exec(t.Context(), "UPDATE sessions SET last_seen_at = now() - interval '1 hour' WHERE id = $1", phoneID); err != nil {
		t.Fatal(err)
	}

	// Without an idle expiry (the default), the idle session stays in the list.
	if sessions := listSessions(t, api, cookie); len(sessions) != 2 {
		t.Errorf("without an expiry: %+v", sessions)
	}

	// With 30 minutes, only the current one is left.
	idle := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.SessionIdleTimeout = 30 * time.Minute }))
	if sessions := listSessions(t, idle, cookie); len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("with an expiry: %+v", sessions)
	}
}

// Signing in closes the session that browser already had (its cookie is replaced, so it would
// be left as a ghost device), but not those of other devices.
func TestStartSessionReplacesBrowserSession(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, old := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	phone := newSession(t, pool, u.ID) // another device

	// Login in the same browser, which still sends the old cookie.
	w := testutil.Do(t, api, old, http.MethodPost, "/auth/login", loginBody)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d; body: %s", w.Code, w.Body)
	}
	current := sessionCookie(t, w)

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, old, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if sessions := listSessions(t, api, current); len(sessions) != 2 {
		t.Errorf("want the new session and the phone one: %+v", sessions)
	}
	if w := testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the phone session should stay: status = %d", w.Code)
	}

	// Change of account in the same browser: registering Bob closes that session of Alice's.
	w = testutil.Do(t, api, current, http.MethodPost, "/auth/register", `{"name":"Bob","email":"bob@example.com","password":"a-long-password"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status = %d; body: %s", w.Code, w.Body)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, current, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the phone session should stay: status = %d", w.Code)
	}
}
