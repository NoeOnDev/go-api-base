package httpx

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP devuelve la IP del cliente.
//
// Sin proxy, es la dirección de la conexión (RemoteAddr). Con trustProxy, se toma el último
// valor de X-Forwarded-For, que es el que agregó nuestro proxy (Caddy, Nginx). Los valores
// anteriores los puede inventar el cliente, así que no se usan.
//
// Activá trustProxy solo si la API no es accesible directamente desde internet; si no,
// cualquiera podría enviar un X-Forwarded-For falso para esquivar el límite de peticiones.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
