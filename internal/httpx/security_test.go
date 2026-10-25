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
		xff        []string
		proxies    TrustedProxies
		want       string
	}{
		{"sin proxy", "203.0.113.7:4321", nil, nil, "203.0.113.7"},
		{"ignora XFF sin TRUST_PROXY", "203.0.113.7:4321", []string{"1.1.1.1"}, nil, "203.0.113.7"},
		{"usa el último XFF con proxy en localhost", "127.0.0.1:5000", []string{"1.1.1.1, 198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy en localhost IPv6", "[::1]:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy en una red de Docker", "172.18.0.2:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy en una red privada", "10.0.0.5:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"varios encabezados: el último es el del proxy", "127.0.0.1:5000", []string{"1.1.1.1", "198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy sin XFF", "127.0.0.1:5000", nil, DefaultTrustedProxies, "127.0.0.1"},
		{"XFF que no es una IP", "127.0.0.1:5000", []string{"inventado"}, DefaultTrustedProxies, "127.0.0.1"},
		// Con TRUST_PROXY y el puerto abierto a internet por error: el XFF inventado no se cree.
		{"conexión directa desde internet", "203.0.113.7:4321", []string{"1.1.1.1"}, DefaultTrustedProxies, "203.0.113.7"},
		{"IPv6", "[2001:db8::1]:443", nil, nil, "2001:db8::1"},
		// Con TRUSTED_PROXIES: solo el proxy indicado, aunque otra dirección sea de una red privada.
		{"proxy indicado", "10.0.0.5:5000", []string{"198.51.100.4"}, mustProxies(t, "10.0.0.5"), "198.51.100.4"},
		{"otro de la red privada, no indicado", "10.0.0.6:5000", []string{"1.1.1.1"}, mustProxies(t, "10.0.0.5"), "10.0.0.6"},
		{"rango indicado", "10.0.0.6:5000", []string{"198.51.100.4"}, mustProxies(t, "10.0.0.0/29"), "198.51.100.4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tt.proxies); got != tt.want {
				t.Errorf("ClientIP = %q, se esperaba %q", got, tt.want)
			}
		})
	}
}

func mustProxies(t *testing.T, list string) TrustedProxies {
	t.Helper()
	p, err := ParseTrustedProxies(list)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseTrustedProxies(t *testing.T) {
	p, err := ParseTrustedProxies(" 127.0.0.1, 10.0.0.0/24 ,::1")
	if err != nil || len(p) != 3 || p[0].String() != "127.0.0.1/32" || p[1].String() != "10.0.0.0/24" || p[2].String() != "::1/128" {
		t.Errorf("ParseTrustedProxies = %v, %v", p, err)
	}
	if _, err := ParseTrustedProxies("127.0.0.1, caddy"); err == nil {
		t.Error("una dirección inválida debería ser un error")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 3)
	h := rl.MiddlewareByIP(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestRateLimitKey(t *testing.T) {
	tests := map[string]string{
		"203.0.113.7":                "203.0.113.7",
		"::ffff:203.0.113.7":         "203.0.113.7",
		"2001:db8:aaaa:bbbb::1":      "2001:db8:aaaa:bbbb::/64",
		"2001:db8:aaaa:bbbb:ffff::9": "2001:db8:aaaa:bbbb::/64",
		"2001:db8:aaaa:cccc::1":      "2001:db8:aaaa:cccc::/64",
		"no-es-ip":                   "no-es-ip",
	}
	for ip, want := range tests {
		if got := rateLimitKey(ip); got != want {
			t.Errorf("rateLimitKey(%q) = %q, se esperaba %q", ip, got, want)
		}
	}
}

func TestRateLimiterGroupsIPv6Block(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 2)
	h := rl.MiddlewareByIP(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	call := func(remote string) int {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// Tres direcciones distintas del mismo /64 comparten el límite de 2.
	call("[2001:db8:1:2::1]:1000")
	call("[2001:db8:1:2::2]:1000")
	if code := call("[2001:db8:1:2::3]:1000"); code != http.StatusTooManyRequests {
		t.Errorf("tercera dirección del mismo /64: status = %d, se esperaba 429", code)
	}
	// Otro /64 tiene su propio límite.
	if code := call("[2001:db8:1:3::1]:1000"); code != http.StatusOK {
		t.Errorf("otro /64: status = %d, se esperaba 200", code)
	}
}

func TestRateLimiterRemembersUntilRefilled(t *testing.T) {
	// 3 intentos que se recargan de a uno cada 20 minutos: tarda 1 hora en llenarse.
	rl := NewRateLimiter(20*time.Minute, 3)
	for range 3 {
		rl.Allow("ana")
	}
	if rl.Allow("ana") {
		t.Fatal("el cuarto intento debería rechazarse")
	}

	// Pasaron 15 minutos sin intentos (más que visitorTTL) y toca la limpieza:
	// la clave no se olvida, porque todavía no se recargó.
	rl.mu.Lock()
	rl.visitors["ana"].lastSeen = time.Now().Add(-15 * time.Minute)
	rl.lastCleanup = time.Now().Add(-2 * time.Minute)
	rl.mu.Unlock()
	if rl.Allow("ana") {
		t.Error("la clave se olvidó antes de recargarse y recuperó todos sus intentos")
	}

	if rl := NewRateLimiter(12*time.Second, 5); rl.ttl != visitorTTL {
		t.Errorf("con recarga rápida debería usar visitorTTL: %v", rl.ttl)
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 1)
	rl.Allow("ana")
	if rl.Allow("ana") {
		t.Fatal("el segundo intento debería rechazarse")
	}
	rl.Reset("ana")
	if !rl.Allow("ana") {
		t.Error("después de Reset la clave debería tener sus intentos de nuevo")
	}
}
