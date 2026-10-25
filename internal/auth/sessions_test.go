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

// loginFrom inicia sesión por la API desde un "dispositivo": un navegador y una IP.
func loginFrom(t *testing.T, api http.Handler, email, userAgent, ip string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"contraseña-de-prueba"}`, email)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", userAgent)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	return sessionCookie(t, w)
}

func listSessions(t *testing.T, api http.Handler, cookie *http.Cookie) []sessionItem {
	t.Helper()
	return testutil.Decode[sessionList](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/sessions", ""), http.StatusOK).Data
}

// otherSession devuelve la sesión de la lista que no es la actual.
func otherSession(t *testing.T, sessions []sessionItem) sessionItem {
	t.Helper()
	for _, s := range sessions {
		if !s.Current {
			return s
		}
	}
	t.Fatalf("no hay otra sesión: %+v", sessions)
	return sessionItem{}
}

func TestListSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	laptop := loginFrom(t, api, "ana@ejemplo.com", "Firefox en la laptop", "198.51.100.1")
	loginFrom(t, api, "ana@ejemplo.com", "Safari en el teléfono", "198.51.100.2")
	loginFrom(t, api, "luis@ejemplo.com", "Chrome de Luis", "198.51.100.3")

	sessions := listSessions(t, api, laptop)
	// Las dos de Ana por la API, más la de testutil.Login. Ninguna de Luis.
	if len(sessions) != 3 {
		t.Fatalf("sesiones de Ana: %+v", sessions)
	}
	byAgent := map[string]sessionItem{}
	for _, s := range sessions {
		byAgent[s.UserAgent] = s
		if strings.Contains(s.UserAgent, "Luis") {
			t.Errorf("aparece una sesión de otro usuario: %+v", s)
		}
	}
	if s := byAgent["Firefox en la laptop"]; !s.Current || s.IP != "198.51.100.1" {
		t.Errorf("la sesión actual: %+v", s)
	}
	if s := byAgent["Safari en el teléfono"]; s.Current || s.IP != "198.51.100.2" {
		t.Errorf("la otra sesión: %+v", s)
	}
}

func TestRevokeSession(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	phone := loginFrom(t, api, "ana@ejemplo.com", "Safari en el teléfono", "198.51.100.2")

	sessions := listSessions(t, api, ana)
	phoneID := otherSession(t, sessions).ID
	var currentID int64
	for _, s := range sessions {
		if s.Current {
			currentID = s.ID
		}
	}

	// La sesión de otro usuario no existe para Ana.
	luisID := listSessions(t, api, luis)[0].ID
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, ana, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", luisID), ""), http.StatusNotFound)
	// La actual se cierra con logout.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, ana, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", currentID), ""), http.StatusUnprocessableEntity)

	if w := testutil.Do(t, api, ana, http.MethodDelete, fmt.Sprintf("/auth/sessions/%d", phoneID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("cerrar la sesión del teléfono: status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, ana, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la sesión actual debería seguir: status = %d", w.Code)
	}
	if w := testutil.Do(t, api, luis, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la sesión de Luis no se toca: status = %d", w.Code)
	}
}

// Cerrar una sesión corta en el acto sus streams de GET /events.
func TestRevokeSessionCutsItsStream(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, laptop := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

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
		t.Fatalf("cerrar la sesión: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("el stream de la sesión cerrada debería cortarse")
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, laptop := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	phone := loginFrom(t, api, "ana@ejemplo.com", "Safari", "198.51.100.2")
	tablet := loginFrom(t, api, "ana@ejemplo.com", "Chrome", "198.51.100.3")

	// Pide la contraseña.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, laptop, http.MethodPost, "/auth/sessions/revoke-others", `{"password":"incorrecta"}`), http.StatusUnprocessableEntity)
	if w := testutil.Do(t, api, phone, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Fatalf("con la contraseña incorrecta no se cierra nada: status = %d", w.Code)
	}

	if w := testutil.Do(t, api, laptop, http.MethodPost, "/auth/sessions/revoke-others", `{"password":"contraseña-de-prueba"}`); w.Code != http.StatusNoContent {
		t.Fatalf("revoke-others: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	for _, c := range []*http.Cookie{phone, tablet} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, c, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	}
	if sessions := listSessions(t, api, laptop); len(sessions) != 1 || !sessions[0].Current {
		t.Errorf("debería quedar solo la actual: %+v", sessions)
	}
}

// La última actividad se guarda como mucho cada 5 minutos, no en cada petición.
func TestLastSeenIsThrottled(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	setLastSeen := func(ago time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE sessions SET last_seen_at = now() - $1::interval WHERE user_id = $2", fmt.Sprintf("%d seconds", int(ago.Seconds())), u.ID); err != nil {
			t.Fatal(err)
		}
	}

	setLastSeen(2 * time.Minute)
	testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", "")
	if ago := lastSeenAgo(t, pool, u.ID); ago < time.Minute {
		t.Errorf("con 2 minutos no debería actualizarse: hace %v", ago)
	}

	setLastSeen(10 * time.Minute)
	testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", "")
	if ago := lastSeenAgo(t, pool, u.ID); ago > time.Minute {
		t.Errorf("con 10 minutos debería actualizarse: hace %v", ago)
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
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	setLastSeen := func(ago string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE sessions SET last_seen_at = now() - $1::interval WHERE user_id = $2", ago, u.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Sin vencimiento por inactividad (por defecto), una sesión quieta hace una hora sigue sirviendo.
	setLastSeen("1 hour")
	if w := testutil.Do(t, testutil.NewAPI(pool), cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Fatalf("sin vencimiento: status = %d", w.Code)
	}

	idle := testutil.WithConfig(func(c *config.Config) { c.SessionIdleTimeout = 30 * time.Minute })
	api := testutil.NewAPI(pool, idle)

	// Con 30 minutos: activa hace 10, sirve; quieta hace una hora, no.
	setLastSeen("10 minutes")
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("activa hace 10 minutos: status = %d", w.Code)
	}
	setLastSeen("1 hour")
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
}
