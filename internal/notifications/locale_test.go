package notifications_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/notifications"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// El texto de una notificación no se guarda: el evento en vivo sale en el idioma del usuario, y
// la lista, en el de quien la pide.
func TestNotificationMessageLanguage(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	svc := notifications.NewService(store.New(pool), broker, nil)
	srv := testutil.NewServer(t, pool, testutil.WithBroker(broker))
	api := testutil.NewAPI(pool, testutil.WithBroker(broker))
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Alice","locale":"es-MX"}`), http.StatusOK)
	events := testutil.OpenEvents(t, srv, cookie, "")

	if err := svc.Notify(t.Context(), alice.ID, auth.NotifyEmailChanged, map[string]string{"email": "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	if ev := events.Next(t, 2*time.Second); !strings.Contains(ev.Data, "El email de tu cuenta ahora es new@example.com.") {
		t.Errorf("el evento debería ir en el idioma de Alice: %s", ev.Data)
	}

	// Un tipo sin texto en los catálogos (uno propio del proyecto) llega con message vacío.
	if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": 7}); err != nil {
		t.Fatal(err)
	}

	for lang, want := range map[string]string{
		"en":    "Your account email is now new@example.com.",
		"es-MX": "El email de tu cuenta ahora es new@example.com.",
	} {
		w := testutil.DoWithLanguage(t, api, lang, cookie, http.MethodGet, "/notifications", "")
		list := testutil.Decode[listResponse](t, w, http.StatusOK)
		if len(list.Data) != 2 || list.Data[0].Message != "" || list.Data[1].Message != want {
			t.Errorf("%s: %+v", lang, list.Data)
		}
	}
}
