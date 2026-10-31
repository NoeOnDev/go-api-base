package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/mail"
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

	// Without a zone, the user has none (null): the application's applies.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/register", `{"name":"Bob","email":"bob@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.TimeZone != nil {
		t.Errorf("user without a zone: %+v", u)
	}
}

func TestTimeZoneInInvitations(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	// The admin can choose it, and accepting does not replace it.
	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", `{"name":"Bob","email":"bob@example.com","role":"user","time_zone":"America/Panama"}`)
	if bob := testutil.Decode[auth.AdminUser](t, w, http.StatusCreated); bob.TimeZone == nil || *bob.TimeZone != "America/Panama" {
		t.Errorf("invited user: %+v", bob)
	}
	msg, _ := mailer.Last("bob@example.com")
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", fmt.Sprintf(`{"token":%q,"password":"a-long-password","time_zone":"America/Bogota"}`, testutil.TokenFromEmail(t, msg)))
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.TimeZone == nil || *u.TimeZone != "America/Panama" {
		t.Errorf("accepted user: %+v", u)
	}

	// Without one, the browser's is kept when accepting.
	_, token := invite(t, api, mailer, admin, "Carol", "carol@example.com", auth.RoleUser)
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", fmt.Sprintf(`{"token":%q,"password":"a-long-password","time_zone":"Mars/Base"}`, token))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["time_zone"] == "" {
		t.Errorf("want an error on time_zone: %+v", resp.Error)
	}
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", fmt.Sprintf(`{"token":%q,"password":"a-long-password","time_zone":"America/Bogota"}`, token))
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.TimeZone == nil || *u.TimeZone != "America/Bogota" {
		t.Errorf("accepted user: %+v", u)
	}

	w = testutil.Do(t, api, admin, http.MethodPost, "/admin/users", `{"name":"Dan","email":"dan@example.com","role":"user","time_zone":"UTC-5"}`)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["time_zone"] == "" {
		t.Errorf("want an error on time_zone: %+v", resp.Error)
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

func TestLockedNoticeInTheUserZone(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	alice, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session, testutil.Password)
	if _, err := pool.Exec(t.Context(), "UPDATE users SET time_zone = 'America/Bogota', locale = 'es-MX' WHERE id = $1", alice.ID); err != nil {
		t.Fatal(err)
	}

	addr := &ips{}
	pending := loginFromIP(t, api, addr.next())
	before := time.Now()
	for range 5 {
		challengeFromIP(t, api, addr.next(), pending, codeBody("000000"))
	}

	msg, ok := mails.Last("alice@example.com")
	if !ok {
		t.Fatal("no notice was sent")
	}
	bogota, _ := civil.LoadLocation("America/Bogota")
	// The minute may change between the attempt and this check.
	var found bool
	for _, at := range []time.Time{before, time.Now()} {
		found = found || strings.Contains(msg.Body, "Fecha: "+at.In(bogota).Format("02/01/2006 15:04")+" (America/Bogota)")
	}
	if !found {
		t.Errorf("the notice does not have the date in Bogotá and in es-MX:\n%s", msg.Body)
	}
}
