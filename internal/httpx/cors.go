package httpx

import (
	"net/http"
	"slices"
)

// CORS permite que un frontend en otro dominio (por ejemplo https://app.cliente.com)
// llame a la API con cookies. Solo responde a los orígenes de la lista; con la lista vacía
// no hace nada, que es lo correcto cuando el frontend y la API comparten dominio.
//
// Desde el frontend hay que usar fetch(url, { credentials: "include" }) para que viaje la cookie.
func CORS(allowedOrigins []string) Middleware {
	return func(next http.Handler) http.Handler {
		if len(allowedOrigins) == 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// La respuesta depende del Origin: los caches no deben reutilizarla para otro origen.
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

			// Preflight: el navegador pregunta antes de un POST/PUT/DELETE con JSON.
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
