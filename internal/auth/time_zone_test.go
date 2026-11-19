package auth_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func registerWithZone(zone string) string {
	return fmt.Sprintf(`{"name":"Ana","email":"ana@example.com","password":"a-long-password","time_zone":%q}`, zone)
}

func TestTimeZoneInRegisterAndProfile(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	// Only IANA names.
	for _, zone := range []string{"Mars/Base", "Local", "", "-05:00"} {
		w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerWithZone(zone))
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["time_zone"] == "" {
			t.Errorf("%q: want an error on time_zone: %+v", zone, resp.Error)
		}
	}

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerWithZone("America/Bogota"))
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.TimeZone == nil || *u.TimeZone != "America/Bogota" {
		t.Fatalf("registered user: %+v", u)
	}
	cookie := sessionCookie(t, w)

	// Without the field, the profile keeps the zone; with it, it changes.
	u = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana María"}`), http.StatusOK)
	if u.TimeZone == nil || *u.TimeZone != "America/Bogota" || u.Name != "Ana María" {
		t.Errorf("profile without time_zone: %+v", u)
	}
	u = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana","time_zone":"America/Argentina/Buenos_Aires"}`), http.StatusOK)
	if u.TimeZone == nil || *u.TimeZone != "America/Argentina/Buenos_Aires" {
		t.Errorf("profile with time_zone: %+v", u)
	}
	w = testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana","time_zone":"Buenos Aires"}`)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["time_zone"] == "" {
		t.Errorf("want an error on time_zone: %+v", resp.Error)
	}
	// Empty, it goes back to the application's (null).
	u = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana","time_zone":""}`), http.StatusOK)
	if u.TimeZone != nil {
		t.Errorf("profile with an empty time_zone: %+v", u)
	}

	// Without a zone, the user has none (null): the application's applies.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/register", `{"name":"Bob","email":"bob@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.TimeZone != nil {
		t.Errorf("user without a zone: %+v", u)
	}
}

func TestUserLocation(t *testing.T) {
	pool := testutil.NewDB(t)
	panama, err := civil.LoadLocation("America/Panama")
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewService(pool, auth.Options{TimeZone: panama})
	api := authn.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, auth.MustUser(r.Context()).Location())
	}))
	location := func(cookie *http.Cookie) string {
		t.Helper()
		return testutil.Do(t, api, cookie, http.MethodGet, "/", "").Body.String()
	}

	// Without a zone of their own, the application's.
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	if got := location(cookie); got != "America/Panama" {
		t.Errorf("Location() = %s, want the application's", got)
	}

	// With one, theirs. One that no longer loads falls back to the application's.
	for zone, want := range map[string]string{"America/Bogota": "America/Bogota", "Gone/Zone": "America/Panama"} {
		if _, err := pool.Exec(t.Context(), "UPDATE users SET time_zone = $1 WHERE id = $2", zone, alice.ID); err != nil {
			t.Fatal(err)
		}
		if got := location(cookie); got != want {
			t.Errorf("with %s: Location() = %s, want %s", zone, got, want)
		}
	}

	if (auth.User{}).Location() != time.UTC {
		t.Error("a User without a session is in UTC")
	}
}
