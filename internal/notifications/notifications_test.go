package notifications_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/notifications"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type listResponse struct {
	Data []notifications.Response `json:"data"`
	Meta struct {
		Total int64 `json:"total"`
	} `json:"meta"`
	Unread int64 `json:"unread"`
}

func TestNotificationsOwnership(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	svc := notifications.NewService(store.New(pool), broker, nil)
	api := testutil.NewAPI(pool, testutil.WithBroker(broker))
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	bob, bobCookie := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	for i := range 2 {
		if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Notify(t.Context(), bob.ID, "task.assigned", map[string]int{"task_id": 9}); err != nil {
		t.Fatal(err)
	}

	// Cada uno ve solo las suyas, de la más nueva a la más vieja.
	list := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(list.Data) != 2 || list.Meta.Total != 2 || list.Unread != 2 || string(list.Data[0].Data) != `{"task_id":2}` {
		t.Fatalf("listado de Alice: %+v", list)
	}
	bobList := testutil.Decode[listResponse](t, testutil.Do(t, api, bobCookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(bobList.Data) != 1 {
		t.Fatalf("listado de Bob: %+v", bobList)
	}

	// La de otro usuario responde 404, como en notes.
	path := fmt.Sprintf("/notifications/%d/read", bobList.Data[0].ID)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodPost, path, ""), http.StatusNotFound)

	// Marcar una como leída.
	path = fmt.Sprintf("/notifications/%d/read", list.Data[0].ID)
	read := testutil.Decode[notifications.Response](t, testutil.Do(t, api, aliceCookie, http.MethodPost, path, ""), http.StatusOK)
	if read.ReadAt == nil {
		t.Error("read_at debería tener la fecha")
	}
	unread := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications?unread=true", ""), http.StatusOK)
	if len(unread.Data) != 1 || unread.Unread != 1 {
		t.Errorf("sin leer: %+v", unread)
	}

	// Marcar todas.
	if w := testutil.Do(t, api, aliceCookie, http.MethodPost, "/notifications/read-all", ""); w.Code != http.StatusNoContent {
		t.Fatalf("read-all: status = %d", w.Code)
	}
	if l := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""), http.StatusOK); l.Unread != 0 {
		t.Errorf("después de read-all quedan %d sin leer", l.Unread)
	}
	// Las de Bob no se tocaron.
	if l := testutil.Decode[listResponse](t, testutil.Do(t, api, bobCookie, http.MethodGet, "/notifications", ""), http.StatusOK); l.Unread != 1 {
		t.Errorf("las de Bob no deberían cambiar: %+v", l)
	}

	// Errores.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications?unread=si", ""), http.StatusUnprocessableEntity)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, "/notifications", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""); w.Header().Get("Cache-Control") != "no-store" {
		t.Error("las notificaciones no deberían guardarse en caché")
	}
}

// do envía una petición al servidor real con la sesión.
func do(t *testing.T, srv *httptest.Server, cookie *http.Cookie, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNotificationsArriveLive(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	svc := notifications.NewService(store.New(pool), broker, nil)
	srv := testutil.NewServer(t, pool, testutil.WithBroker(broker))
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bobCookie := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	aliceEvents := testutil.OpenEvents(t, srv, aliceCookie, "")
	bobEvents := testutil.OpenEvents(t, srv, bobCookie, "")

	if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": 1}); err != nil {
		t.Fatal(err)
	}
	ev := aliceEvents.Next(t, 2*time.Second)
	var n notifications.Response
	if err := json.Unmarshal([]byte(ev.Data), &n); err != nil || ev.Type != notifications.EventNotification || n.Kind != "task.assigned" {
		t.Fatalf("evento: %+v (%v)", ev, err)
	}
	bobEvents.Nothing(t, 100*time.Millisecond)

	// Marcarla como leída avisa a las demás pestañas, para actualizar el contador.
	if resp := do(t, srv, aliceCookie, http.MethodPost, fmt.Sprintf("/notifications/%d/read", n.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("read: status = %d", resp.StatusCode)
	}
	if ev := aliceEvents.Next(t, 2*time.Second); ev.Type != notifications.EventRead || ev.Data != fmt.Sprintf(`{"id":%d}`, n.ID) {
		t.Errorf("evento de leída: %+v", ev)
	}
}

func TestSecurityChangesNotify(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	events := testutil.OpenEvents(t, srv, cookie, "")

	// Cambiar la contraseña desde el perfil avisa en vivo y queda guardado.
	body := `{"current_password":"test-password","password":"a-new-password"}`
	if resp := do(t, srv, cookie, http.MethodPut, "/auth/password", body); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cambio de contraseña: status = %d", resp.StatusCode)
	}
	ev := events.Next(t, 2*time.Second)
	if ev.Type != notifications.EventNotification || !strings.Contains(ev.Data, auth.NotifyPasswordChanged) || !strings.Contains(ev.Data, "password was changed") {
		t.Errorf("evento: %+v", ev)
	}

	resp := do(t, srv, cookie, http.MethodGet, "/notifications", "")
	var list listResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	// El texto se arma al leer (no se guarda): en data no queda ningún mensaje.
	if len(list.Data) != 1 || list.Data[0].Kind != auth.NotifyPasswordChanged ||
		!strings.Contains(list.Data[0].Message, "password was changed") || string(list.Data[0].Data) != "{}" {
		t.Errorf("notificaciones guardadas: %+v", list)
	}
}

func TestTwoFactorChangesNotify(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", `{"password":"test-password"}`); w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d", w.Code)
	}

	list := testutil.Decode[listResponse](t, testutil.Do(t, api, cookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(list.Data) != 2 || list.Data[0].Kind != auth.NotifyTwoFactorDisabled || list.Data[1].Kind != auth.NotifyTwoFactorEnabled {
		t.Errorf("notificaciones: %+v", list.Data)
	}
}
