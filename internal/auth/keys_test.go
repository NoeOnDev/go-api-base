package auth_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// serviceWithKeys crea el servicio de auth con un llavero, como lo hace el comando rotate-keys.
func serviceWithKeys(t *testing.T, pool *pgxpool.Pool, current []byte, previous ...[]byte) (*auth.Service, *secretbox.Box) {
	t.Helper()
	box, err := secretbox.New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewService(pool, auth.Options{Box: box}), box
}

func TestRotateKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	apiA := testutil.NewAPI(pool) // clave A

	// Dos usuarios con 2FA activo y uno con el 2FA a medio configurar, todos con la clave A.
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	_, sofia := testutil.Login(t, pool, "sofia@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiA, ana, testutil.Password)
	testutil.EnableTwoFactor(t, apiA, luis, testutil.Password)
	testutil.Decode[setupResponse](t, testutil.Do(t, apiA, sofia, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	// Rotación: B es la actual y A queda como anterior.
	service, boxB := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	boxA, _ := secretbox.New(testutil.EncryptionKey)

	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentKeyID != boxB.KeyID() || report.Reencrypted != 3 || report.AlreadyCurrent != 0 {
		t.Errorf("reporte inesperado: %+v", report)
	}
	// Los códigos de recuperación de A no se pueden recalcular: el reporte lo avisa.
	if len(report.OldRecoveryCodes) != 1 || report.OldRecoveryCodes[0] != (auth.KeyUsage{KeyID: boxA.KeyID(), Users: 2}) {
		t.Errorf("códigos viejos inesperados: %+v", report.OldRecoveryCodes)
	}
	if report.Done() || len(report.RemovableKeys) != 0 {
		t.Errorf("la clave A todavía se usa y no debería poder quitarse: %+v", report)
	}

	// Una segunda pasada no vuelve a cifrar nada.
	again, err := service.RotateKeys(t.Context())
	if err != nil || again.Reencrypted != 0 || again.AlreadyCurrent != 3 {
		t.Errorf("segunda pasada inesperada: %+v (%v)", again, err)
	}

	// Cuando los dos usuarios regeneran sus códigos con la clave nueva, la rotación termina.
	apiB := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
		c.EncryptionPreviousKeys = [][]byte{testutil.EncryptionKey}
	}))
	for _, cookie := range []*http.Cookie{ana, luis} {
		testutil.Decode[recoveryCodes](t,
			testutil.Do(t, apiB, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"contraseña-de-prueba"}`), http.StatusOK)
	}

	done, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !done.Done() || len(done.RemovableKeys) != 1 || done.RemovableKeys[0] != boxA.KeyID() {
		t.Errorf("la rotación debería estar completa y la clave A se puede quitar: %+v", done)
	}
}

func TestRotateKeysReportsUndecryptable(t *testing.T) {
	pool := testutil.NewDB(t)

	// Un usuario con el secreto cifrado con una clave C que no está en el llavero.
	keyC := bytes.Repeat([]byte{3}, 32)
	apiC := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.EncryptionKey = keyC }))
	user, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiC, cookie, testutil.Password)

	service, _ := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	boxC, _ := secretbox.New(keyC)
	if len(report.Undecryptable) != 1 || report.Undecryptable[0].UserID != user.ID || report.Undecryptable[0].KeyID != boxC.KeyID() {
		t.Errorf("se esperaba un secreto sin descifrar del usuario %d: %+v", user.ID, report.Undecryptable)
	}
	if report.Done() {
		t.Error("la rotación no debería darse por terminada")
	}
}
