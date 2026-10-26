// Package testutil reúne ayudas para los tests de integración.
package testutil

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/server"
)

// NewDB conecta a la base de pruebas (TEST_DATABASE_URL), aplica las migraciones
// y vacía todas las tablas para que cada test empiece desde cero.
//
// Si TEST_DATABASE_URL no está definida, el test se saltea en lugar de fallar:
// así los tests unitarios siguen corriendo en una máquina sin base de datos.
//
// Como vacía todas las tablas, se niega a usar una base cuyo nombre no termine en "_test"
// o que sea la misma de DATABASE_URL (ver checkTestDatabase).
//
// Los paquetes comparten la base de pruebas, así que deben correr de a uno:
// go test -p 1 ./... (es lo que hace make test). Dentro de un paquete, los tests
// que usan la base no deben llevar t.Parallel().
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if requireTestDatabase(os.Getenv) {
			t.Fatal("TEST_DATABASE_URL es obligatoria en CI: sin ella, los tests de integración no corren")
		}
		t.Skip("TEST_DATABASE_URL no está definida: se saltea el test de integración")
	}
	if err := checkTestDatabase(url, os.Getenv("DATABASE_URL")); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("conectar a la base de pruebas: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrar la base de pruebas: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("listar tablas: %v", err)
	}
	tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
		var name string
		err := row.Scan(&name)
		return pgx.Identifier{name}.Sanitize(), err
	})
	if err != nil {
		t.Fatalf("listar tablas: %v", err)
	}

	if len(tables) > 0 {
		sql := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("vaciar tablas: %v", err)
		}
	}

	return pool
}

// testPassword es la contraseña de los usuarios que crea Login (y la que envía EnableTwoFactor).
const testPassword = "contraseña-de-prueba"

// Login crea un usuario con el rol indicado, le inicia una sesión y devuelve el usuario
// y la cookie para enviar en las peticiones: r.AddCookie(cookie). Su contraseña es
// "contraseña-de-prueba".
//
// Acepta las mismas opciones que NewAPI: si la API del test cambia la configuración de la
// cookie (por ejemplo, WithConfig con CookieSecure), pasale las mismas a Login para que la
// cookie coincida.
func Login(t *testing.T, pool *pgxpool.Pool, email, role string, opts ...Option) (auth.User, *http.Cookie) {
	t.Helper()

	var d server.Deps
	for _, opt := range opts {
		opt(&d)
	}
	service := auth.NewService(pool, auth.Options{
		SessionTTL:   d.Config.SessionTTL,
		CookieSecure: d.Config.CookieSecure,
	})
	u, err := service.CreateUser(t.Context(), auth.RegisterInput{
		Name:     "Usuario de prueba",
		Email:    email,
		Password: testPassword,
	}, role)
	if err != nil {
		t.Fatalf("crear usuario %s: %v", email, err)
	}

	w := httptest.NewRecorder()
	if err := service.StartSession(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil), u.ID); err != nil {
		t.Fatalf("iniciar sesión de %s: %v", email, err)
	}

	// En producción la cookie lleva el prefijo __Host- (ver auth.CookieName).
	for _, c := range w.Result().Cookies() {
		if strings.TrimPrefix(c.Name, "__Host-") == auth.CookieName {
			return u, c
		}
	}
	t.Fatal("no se generó la cookie de sesión")
	return auth.User{}, nil
}

// requireTestDatabase indica si la falta de TEST_DATABASE_URL es un error. En CI lo es: si no,
// el CI daría todo "ok" sin haber probado nada contra la base. GitHub Actions (y casi todos los
// servicios de CI) definen CI=true.
func requireTestDatabase(getenv func(string) string) bool {
	ci, _ := strconv.ParseBool(getenv("CI"))
	return ci
}

// checkTestDatabase evita que los tests borren datos reales por un error de configuración
// (por ejemplo, copiar DATABASE_URL en TEST_DATABASE_URL). Exige que la base de pruebas
// se llame "<algo>_test" y que no sea la misma base que usa la aplicación.
func checkTestDatabase(testURL, appURL string) error {
	testCfg, err := pgx.ParseConfig(testURL)
	if err != nil {
		return fmt.Errorf("TEST_DATABASE_URL inválida: %w", err)
	}

	if !strings.HasSuffix(testCfg.Database, "_test") {
		return fmt.Errorf("TEST_DATABASE_URL apunta a la base %q: por seguridad, la base de pruebas debe terminar en \"_test\" "+
			"(los tests vacían todas sus tablas)", testCfg.Database)
	}

	if appURL == "" {
		return nil
	}
	appCfg, err := pgx.ParseConfig(appURL)
	if err != nil {
		return nil // la URL de la app no es asunto de los tests; config.Load la valida
	}
	if testCfg.Host == appCfg.Host && testCfg.Port == appCfg.Port && testCfg.Database == appCfg.Database {
		return fmt.Errorf("TEST_DATABASE_URL y DATABASE_URL apuntan a la misma base (%s): los tests la vaciarían", testCfg.Database)
	}
	return nil
}
