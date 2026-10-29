package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/server"
)

// EncryptionKey is the ENCRYPTION_KEY of the tests: 32 fixed bytes, not a secret.
var EncryptionKey = bytes.Repeat([]byte{1}, 32)

// APIError is the API error format, to read it in the tests.
type APIError struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

// Option adjusts the test API that NewAPI creates.
type Option func(*server.Deps)

// WithMailer uses the given sender for the emails, usually a *mail.Recorder to read the emails
// sent in the test.
func WithMailer(m mail.Sender) Option {
	return func(d *server.Deps) { d.Mailer = m }
}

// WithBroker uses the given broker, to publish events from the test.
func WithBroker(b *realtime.Broker) Option {
	return func(d *server.Deps) { d.Broker = b }
}

// WithConfig changes the configuration, for example to turn on email verification.
func WithConfig(change func(*config.Config)) Option {
	return func(d *server.Deps) { change(&d.Config) }
}

// NewAPI returns the whole API (routes and middlewares) connected to the test database.
// By default the emails go to a mail.Recorder that is thrown away.
func NewAPI(pool *pgxpool.Pool, opts ...Option) http.Handler {
	d := server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
		Config: config.Config{FrontendURL: "https://app.test", AppName: "Test App", EncryptionKey: EncryptionKey},
		Mailer: &mail.Recorder{},
	}
	for _, opt := range opts {
		opt(&d)
	}
	// The key ring is built with the final configuration, as in main: the rotation tests change the
	// keys with WithConfig.
	if d.Box == nil && len(d.Config.EncryptionKey) > 0 {
		box, err := secretbox.New(d.Config.EncryptionKey, d.Config.EncryptionPreviousKeys...)
		if err != nil {
			panic(fmt.Sprintf("testutil.NewAPI: invalid key ring: %v", err))
		}
		d.Box = box
	}
	return server.NewHandler(d)
}

var tokenInLink = regexp.MustCompile(`[?&]token=([A-Za-z0-9_-]+)`)

// TokenFromEmail extracts the token from the link of an email (for example, the password reset one).
func TokenFromEmail(t *testing.T, msg mail.Message) string {
	t.Helper()
	m := tokenInLink.FindStringSubmatch(msg.Body)
	if m == nil {
		t.Fatalf("the email has no link with a token:\n%s", msg.Body)
	}
	return m[1]
}

// Do sends a request to the API. cookie is the user's session, or nil for a request without one.
// They all come from the same IP (192.0.2.1), so they share the per-IP limits.
func Do(t *testing.T, api http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return DoFromIP(t, api, "", cookie, method, path, body)
}

// DoFromIP is like Do, but the request comes from the given IP. It helps to simulate an attacker
// who spreads their attempts across many IPs, or to avoid hitting the per-IP limit.
func DoFromIP(t *testing.T, api http.Handler, ip string, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, api, ip, "", cookie, method, path, body)
}

// DoWithLanguage is like Do, but with the Accept-Language header (for example "es-MX"), to test
// the responses in another language (see docs/i18n.md).
func DoWithLanguage(t *testing.T, api http.Handler, lang string, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, api, "", lang, cookie, method, path, body)
}

func request(t *testing.T, api http.Handler, ip, lang string, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	if ip != "" {
		r.RemoteAddr = ip + ":1234"
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

// Decode checks the status of the response and decodes its JSON body into T.
func Decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, wantStatus, w.Body)
	}
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}
	return v
}
