// Package testutil reúne ayudas para los tests de integración.
package testutil

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// NewDB conecta a la base de pruebas (TEST_DATABASE_URL), aplica las migraciones
// y vacía todas las tablas para que cada test empiece desde cero.
//
// Si TEST_DATABASE_URL no está definida, el test se saltea en lugar de fallar:
// así los tests unitarios siguen corriendo en una máquina sin base de datos.
//
// Los paquetes comparten la base de pruebas, así que deben correr de a uno:
// go test -p 1 ./... (es lo que hace make test). Dentro de un paquete, los tests
// que usan la base no deben llevar t.Parallel().
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL no está definida: se saltea el test de integración")
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

// Login crea un usuario con el rol indicado, le inicia una sesión y devuelve el usuario
// y la cookie para enviar en las peticiones: r.AddCookie(cookie).
func Login(t *testing.T, pool *pgxpool.Pool, email, role string) (auth.User, *http.Cookie) {
	t.Helper()

	service := auth.NewService(store.New(pool), auth.Options{})
	u, err := service.CreateUser(t.Context(), auth.RegisterInput{
		Name:     "Usuario de prueba",
		Email:    email,
		Password: "contraseña-de-prueba",
	}, role)
	if err != nil {
		t.Fatalf("crear usuario %s: %v", email, err)
	}

	w := httptest.NewRecorder()
	if err := service.StartSession(t.Context(), w, u.ID); err != nil {
		t.Fatalf("iniciar sesión de %s: %v", email, err)
	}

	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			return u, c
		}
	}
	t.Fatal("no se generó la cookie de sesión")
	return auth.User{}, nil
}
