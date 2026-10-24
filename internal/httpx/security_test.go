package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		trustProxy bool
		want       string
	}{
		{"sin proxy", "203.0.113.7:4321", "", false, "203.0.113.7"},
		{"ignora XFF sin proxy confiable", "203.0.113.7:4321", "1.1.1.1", false, "203.0.113.7"},
		{"usa el último XFF con proxy", "127.0.0.1:5000", "1.1.1.1, 198.51.100.4", true, "198.51.100.4"},
		{"proxy sin XFF", "127.0.0.1:5000", "", true, "127.0.0.1"},
		{"IPv6", "[2001:db8::1]:443", "", false, "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := ClientIP(r, tt.trustProxy); got != tt.want {
				t.Errorf("ClientIP = %q, se esperaba %q", got, tt.want)
			}
		})
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 3, false)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for i := range 3 {
		if w := call("203.0.113.1"); w.Code != http.StatusOK {
			t.Fatalf("intento %d: status = %d", i+1, w.Code)
		}
	}

	w := call("203.0.113.1")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("cuarto intento: status = %d, se esperaba 429", w.Code)
	}
	if w.Header().Get("Retry-After") != "3600" {
		t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
	}

	// Otra IP tiene su propio límite.
	if w := call("203.0.113.2"); w.Code != http.StatusOK {
		t.Errorf("otra IP: status = %d", w.Code)
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := CORS([]string{"https://app.ejemplo.com"})(next)

	t.Run("preflight de un origen permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/notes", nil)
		r.Header.Set("Origin", "https://app.ejemplo.com")
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "https://app.ejemplo.com" ||
			w.Header().Get("Access-Control-Allow-Credentials") != "true" ||
			w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Errorf("encabezados incompletos: %v", w.Header())
		}
	})

	t.Run("petición normal de un origen permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://app.ejemplo.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "https://app.ejemplo.com" {
			t.Errorf("status = %d, encabezados = %v", w.Code, w.Header())
		}
	})

	t.Run("origen no permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://otro-sitio.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("no debería permitir el origen: %v", w.Header())
		}
	})

	t.Run("sin orígenes configurados no agrega nada", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://app.ejemplo.com")
		w := httptest.NewRecorder()
		CORS(nil)(next).ServeHTTP(w, r)

		if len(w.Header()) != 0 {
			t.Errorf("encabezados inesperados: %v", w.Header())
		}
	})
}
