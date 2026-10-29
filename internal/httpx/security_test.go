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
		{"no proxy", "203.0.113.7:4321", nil, nil, "203.0.113.7"},
		{"ignores XFF without TRUST_PROXY", "203.0.113.7:4321", []string{"1.1.1.1"}, nil, "203.0.113.7"},
		{"uses the last XFF with a proxy on localhost", "127.0.0.1:5000", []string{"1.1.1.1, 198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy en localhost IPv6", "[::1]:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy on a Docker network", "172.18.0.2:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy on a private network", "10.0.0.5:5000", []string{"198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"several headers: the last one is the proxy's", "127.0.0.1:5000", []string{"1.1.1.1", "198.51.100.4"}, DefaultTrustedProxies, "198.51.100.4"},
		{"proxy without XFF", "127.0.0.1:5000", nil, DefaultTrustedProxies, "127.0.0.1"},
		{"XFF that is not an IP", "127.0.0.1:5000", []string{"inventado"}, DefaultTrustedProxies, "127.0.0.1"},
		// With TRUST_PROXY and the port open to the internet by mistake: a made-up XFF is not believed.
		{"direct connection from the internet", "203.0.113.7:4321", []string{"1.1.1.1"}, DefaultTrustedProxies, "203.0.113.7"},
		{"IPv6", "[2001:db8::1]:443", nil, nil, "2001:db8::1"},
		// With TRUSTED_PROXIES: only the given proxy, even if another address is on a private network.
		{"proxy indicado", "10.0.0.5:5000", []string{"198.51.100.4"}, mustProxies(t, "10.0.0.5"), "198.51.100.4"},
		{"another one on the private network, not listed", "10.0.0.6:5000", []string{"1.1.1.1"}, mustProxies(t, "10.0.0.5"), "10.0.0.6"},
		{"rango indicado", "10.0.0.6:5000", []string{"198.51.100.4"}, mustProxies(t, "10.0.0.0/29"), "198.51.100.4"},
		// With two proxies (Cloudflare in front of Caddy): XFF is walked from right to left skipping the
		// trusted ones.
		{"Cloudflare y Caddy", "127.0.0.1:5000", []string{"203.0.113.7, 173.245.48.1"}, mustProxies(t, "127.0.0.1, 173.245.48.0/20"), "203.0.113.7"},
		{"ignores what the client makes up on the left", "127.0.0.1:5000", []string{"6.6.6.6, 203.0.113.7, 173.245.48.1"}, mustProxies(t, "127.0.0.1, 173.245.48.0/20"), "203.0.113.7"},
		{"the chain across several headers", "127.0.0.1:5000", []string{"203.0.113.7", "173.245.48.1"}, mustProxies(t, "127.0.0.1, 173.245.48.0/20"), "203.0.113.7"},
		{"client on the private network", "127.0.0.1:5000", []string{"192.168.1.5"}, DefaultTrustedProxies, "192.168.1.5"},
		{"all trusted: the leftmost one", "127.0.0.1:5000", []string{"192.168.1.5, 10.0.0.9"}, DefaultTrustedProxies, "192.168.1.5"},
		{"an invalid value stops the search", "127.0.0.1:5000", []string{"inventado, 10.0.0.9"}, DefaultTrustedProxies, "10.0.0.9"},
		{"garbage to the left of a valid client", "127.0.0.1:5000", []string{"inventado, 203.0.113.7"}, DefaultTrustedProxies, "203.0.113.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tt.proxies); got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
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
		t.Error("an invalid address should be an error")
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
			t.Fatalf("attempt %d: status = %d", i+1, w.Code)
		}
	}

	w := call("203.0.113.1")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth attempt: status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") != "3600" {
		t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
	}

	// Another IP has its own limit.
	if w := call("203.0.113.2"); w.Code != http.StatusOK {
		t.Errorf("another IP: status = %d", w.Code)
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := CORS([]string{"https://app.example.com"})(next)

	t.Run("preflight de un origen permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/notes", nil)
		r.Header.Set("Origin", "https://app.example.com")
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
			w.Header().Get("Access-Control-Allow-Credentials") != "true" ||
			w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Errorf("encabezados incompletos: %v", w.Header())
		}
	})

	t.Run("regular request from an allowed origin", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://app.example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
			t.Errorf("status = %d, encabezados = %v", w.Code, w.Header())
		}
	})

	t.Run("origen no permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://other-site.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("should not allow the origin: %v", w.Header())
		}
	})

	t.Run("without configured origins it adds nothing", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		r.Header.Set("Origin", "https://app.example.com")
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
			t.Errorf("rateLimitKey(%q) = %q, want %q", ip, got, want)
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

	// Three different addresses of the same /64 share the limit of 2.
	call("[2001:db8:1:2::1]:1000")
	call("[2001:db8:1:2::2]:1000")
	if code := call("[2001:db8:1:2::3]:1000"); code != http.StatusTooManyRequests {
		t.Errorf("third address of the same /64: status = %d, want 429", code)
	}
	// Another /64 has its own limit.
	if code := call("[2001:db8:1:3::1]:1000"); code != http.StatusOK {
		t.Errorf("another /64: status = %d, want 200", code)
	}
}

func TestRateLimiterRemembersUntilRefilled(t *testing.T) {
	// 3 attempts refilled one every 20 minutes: it takes 1 hour to fill up.
	rl := NewRateLimiter(20*time.Minute, 3)
	for range 3 {
		rl.Allow("alice")
	}
	if rl.Allow("alice") {
		t.Fatal("the fourth attempt should be rejected")
	}

	// 15 minutes went by without attempts (more than visitorTTL) and the cleanup runs: the key is
	// not forgotten, because it has not refilled yet.
	rl.mu.Lock()
	rl.visitors["alice"].lastSeen = time.Now().Add(-15 * time.Minute)
	rl.lastCleanup = time.Now().Add(-2 * time.Minute)
	rl.mu.Unlock()
	if rl.Allow("alice") {
		t.Error("the key was forgotten before refilling and got all its attempts back")
	}

	if rl := NewRateLimiter(12*time.Second, 5); rl.ttl != visitorTTL {
		t.Errorf("with a fast refill it should use visitorTTL: %v", rl.ttl)
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(time.Hour, 1)
	rl.Allow("alice")
	if rl.Allow("alice") {
		t.Fatal("the second attempt should be rejected")
	}
	rl.Reset("alice")
	if !rl.Allow("alice") {
		t.Error("after Reset the key should have its attempts again")
	}
}
