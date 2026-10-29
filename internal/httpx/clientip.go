package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// TrustedProxies are the addresses of the proxies (Caddy, Nginx) whose X-Forwarded-For is
// accepted. When empty, it is accepted from nobody and the client IP is the connection's.
type TrustedProxies []netip.Prefix

// DefaultTrustedProxies are localhost and the private networks: where the proxy is when it runs
// on the same server or in a Docker network. They are used with TRUST_PROXY=true and without
// TRUSTED_PROXIES.
//
// Beware of shared private networks: some providers have private networks common to every
// customer of a data center. There, set the exact IP of the proxy with TRUSTED_PROXIES.
var DefaultTrustedProxies = TrustedProxies{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParseTrustedProxies reads a comma-separated list of IPs or CIDR ranges, for example
// "127.0.0.1, 10.0.0.0/24". A single IP is a one-address range.
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
			return nil, fmt.Errorf("%q is not an IP or a CIDR range", item)
		}
		addr = addr.Unmap()
		proxies = append(proxies, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return proxies, nil
}

// Contains reports whether the address belongs to one of the proxies.
func (p TrustedProxies) Contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range p {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the client IP.
//
// If the connection comes from one of the trusted proxies, the IP comes from X-Forwarded-For (see
// clientFromForwardedFor). From anywhere else (or with an empty list), the connection address is
// used: that way, if the API port were open to the internet by mistake, nobody could make up their
// IP to get around the limits.
func ClientIP(r *http.Request, proxies TrustedProxies) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if addr, err := netip.ParseAddr(host); err == nil && proxies.Contains(addr) {
		if ip, ok := clientFromForwardedFor(r.Header.Values("X-Forwarded-For"), proxies); ok {
			return ip
		}
	}
	return host
}

// clientFromForwardedFor finds the client in X-Forwarded-For, even across several headers.
// Each proxy appends the IP that connected to it, so the list is walked from right to left
// skipping the trusted proxies: the first IP that is not a proxy is the client's. With two
// proxies (for example, Cloudflare in front of Caddy) the last value is Cloudflare's, not the
// client's. Whatever is to the left can be made up by the client, so it is not used.
//
// A value that is not an IP stops the search. If every IP belongs to a trusted proxy (a client
// on the private network), the leftmost one that could be read is used.
func clientFromForwardedFor(values []string, proxies TrustedProxies) (string, bool) {
	var ips []string
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			ips = append(ips, strings.TrimSpace(part))
		}
	}

	client := ""
	for _, ip := range slices.Backward(ips) {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			break
		}
		client = ip
		if !proxies.Contains(addr) {
			break
		}
	}
	return client, client != ""
}
