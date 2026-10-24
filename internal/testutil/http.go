package testutil

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/server"
)

// APIError es el formato de error de la API, para leerlo en los tests.
type APIError struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

// Option ajusta la API de prueba que crea NewAPI.
type Option func(*server.Deps)

// WithMailer usa el sender indicado para los correos, normalmente un *mail.Recorder
// para poder leer los correos enviados en el test.
func WithMailer(m mail.Sender) Option {
	return func(d *server.Deps) { d.Mailer = m }
}

// WithConfig permite cambiar la configuración, por ejemplo para activar la verificación de email.
func WithConfig(change func(*config.Config)) Option {
	return func(d *server.Deps) { change(&d.Config) }
}

// NewAPI devuelve la API completa (rutas y middlewares) conectada a la base de pruebas.
// Por defecto los correos van a un mail.Recorder que se descarta.
func NewAPI(pool *pgxpool.Pool, opts ...Option) http.Handler {
	d := server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
		Config: config.Config{FrontendURL: "https://app.test"},
		Mailer: &mail.Recorder{},
	}
	for _, opt := range opts {
		opt(&d)
	}
	return server.NewHandler(d)
}

var tokenInLink = regexp.MustCompile(`[?&]token=([A-Za-z0-9_-]+)`)

// TokenFromEmail extrae el token del enlace de un correo (por ejemplo, el de recuperar la contraseña).
func TokenFromEmail(t *testing.T, msg mail.Message) string {
	t.Helper()
	m := tokenInLink.FindStringSubmatch(msg.Body)
	if m == nil {
		t.Fatalf("el correo no tiene un enlace con token:\n%s", msg.Body)
	}
	return m[1]
}

// Do envía una petición a la API. cookie es la sesión del usuario, o nil para una petición sin sesión.
func Do(t *testing.T, api http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

// Decode comprueba el status de la respuesta y decodifica su cuerpo JSON en T.
func Decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, se esperaba %d; cuerpo: %s", w.Code, wantStatus, w.Body)
	}
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	return v
}
