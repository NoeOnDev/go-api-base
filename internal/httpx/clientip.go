package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP devuelve la IP del cliente.
//
// Sin proxy, es la dirección de la conexión (RemoteAddr). Con trustProxy, se toma el último
// valor de X-Forwarded-For, que es el que agregó nuestro proxy (Caddy, Nginx). Los valores
// anteriores los puede inventar el cliente, así que no se usan.
//
// Aun con trustProxy, el encabezado solo se cree si la conexión viene de localhost o de una red
// privada, que es donde está el proxy (en el mismo servidor, o en una red de Docker). Así, si el
// puerto de la API quedara abierto a internet por error, nadie puede conectarse directo con un
// X-Forwarded-For inventado para esquivar los límites por IP: se usa su dirección real.
func ClientIP(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if trustProxy && isTrustedProxy(host) {
		if ip, ok := lastForwardedFor(r.Header.Values("X-Forwarded-For")); ok {
			return ip
		}
	}
	return host
}

// isTrustedProxy indica si la conexión viene de localhost o de una red privada
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 y fc00::/7 en IPv6).
func isTrustedProxy(host string) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate()
}

// lastForwardedFor devuelve el último valor de X-Forwarded-For, aunque lleguen varios
// encabezados: el proxy agrega el suyo al final. Si no es una IP válida, no se usa.
func lastForwardedFor(values []string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	parts := strings.Split(values[len(values)-1], ",")
	ip := strings.TrimSpace(parts[len(parts)-1])
	if _, err := netip.ParseAddr(ip); err != nil {
		return "", false
	}
	return ip, true
}
