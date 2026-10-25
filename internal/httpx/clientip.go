package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies son las direcciones de los proxies (Caddy, Nginx) de los que se acepta
// X-Forwarded-For. Vacía, no se acepta de nadie y la IP del cliente es la de la conexión.
type TrustedProxies []netip.Prefix

// DefaultTrustedProxies son localhost y las redes privadas: donde está el proxy si corre en el
// mismo servidor o en una red de Docker. Es lo que se usa con TRUST_PROXY=true y sin
// TRUSTED_PROXIES.
//
// Ojo con las redes privadas compartidas: algunos proveedores tienen redes privadas comunes a
// todos los clientes de un centro de datos. Ahí conviene indicar la IP exacta del proxy con
// TRUSTED_PROXIES.
var DefaultTrustedProxies = TrustedProxies{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParseTrustedProxies lee una lista de IPs o rangos CIDR separados por comas, por ejemplo
// "127.0.0.1, 10.0.0.0/24". Una IP sola es un rango de una dirección.
func ParseTrustedProxies(list string) (TrustedProxies, error) {
	var proxies TrustedProxies
	for item := range strings.SplitSeq(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			proxies = append(proxies, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q no es una IP ni un rango CIDR", item)
		}
		addr = addr.Unmap()
		proxies = append(proxies, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return proxies, nil
}

// Contains indica si la dirección es de uno de los proxies.
func (p TrustedProxies) Contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range p {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP devuelve la IP del cliente.
//
// Si la conexión viene de uno de los proxies de confianza, se toma el último valor de
// X-Forwarded-For, que es el que agregó el proxy. Los valores anteriores los puede inventar el
// cliente, así que no se usan. Si la conexión viene de cualquier otro lado (o la lista está
// vacía), se usa la dirección de la conexión: así, si el puerto de la API quedara abierto a
// internet por error, nadie puede inventar su IP para esquivar los límites.
func ClientIP(r *http.Request, proxies TrustedProxies) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if addr, err := netip.ParseAddr(host); err == nil && proxies.Contains(addr) {
		if ip, ok := lastForwardedFor(r.Header.Values("X-Forwarded-For")); ok {
			return ip
		}
	}
	return host
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
