package notifications_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// El escenario de una sesión robada: el atacante deja un stream abierto, y el dueño recupera
// su contraseña. El stream del atacante se corta en el acto, antes del aviso de seguridad.
func TestStolenSessionStreamIsCutOnPasswordReset(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	srv := testutil.NewServer(t, pool, testutil.WithMailer(mails))
	_, owner := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	// Otra sesión de Ana, iniciada por quien le robó la contraseña.
	resp := do(t, srv, owner, http.MethodPost, "/auth/login", `{"email":"ana@ejemplo.com","password":"contraseña-de-prueba"}`)
	var stolen *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			stolen = c
		}
	}
	if stolen == nil {
		t.Fatal("no se creó la segunda sesión")
	}
	attacker := testutil.OpenEvents(t, srv, stolen, "")

	// El dueño recupera la contraseña.
	do(t, srv, owner, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	reset := fmt.Sprintf(`{"token":%q,"password":"una-clave-nueva"}`, testutil.TokenFromEmail(t, msg))
	if resp := do(t, srv, owner, http.MethodPost, "/auth/password/reset", reset); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: status = %d", resp.StatusCode)
	}

	// El atacante no recibe nada (ni el aviso de "se cambió tu contraseña") y su stream se cierra.
	attacker.Nothing(t, 200*time.Millisecond)
	if !attacker.Closed(time.Second) {
		t.Fatal("el stream de la sesión robada debería cortarse al recuperar la contraseña")
	}
}

func TestLogoutCutsItsStream(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	events := testutil.OpenEvents(t, srv, cookie, "")

	if resp := do(t, srv, cookie, http.MethodPost, "/auth/logout", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("el stream debería cortarse con el logout, sin esperar al próximo latido")
	}
}
