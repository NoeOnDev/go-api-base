package httpx

import (
	"net/http"
	"slices"
)

// CORS lets a frontend on another domain (for example https://app.client.com) call the API
// with cookies. It only answers the origins of the list; with an empty list it does nothing,
// which is right when the frontend and the API share a domain.
//
// The frontend must use fetch(url, { credentials: "include" }) for the cookie to be sent.
func CORS(allowedOrigins []string) Middleware {
	return func(next http.Handler) http.Handler {
		if len(allowedOrigins) == 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The response depends on Origin: caches must not reuse it for another origin.
			w.Header().Add("Vary", "Origin")

			origin := r.Header.Get("Origin")
			if origin == "" || !slices.Contains(allowedOrigins, origin) {
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "Location, Retry-After, X-Request-ID")

			// Preflight: the browser asks first before a POST/PUT/DELETE with JSON.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
