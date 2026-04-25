package http_loopback

import (
	"net"
	"net/url"
	"strings"
)

// IsLoopbackHost reports whether host (a hostname or IP literal) is
// strictly loopback. Strings that resolve to non-loopback addresses,
// or that are unrecognised, return false. Hostname resolution is NOT
// performed against external DNS — only literal IP / `localhost` are
// accepted, which keeps the loopback check from leaking the dial
// attempt to the network.
func IsLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".") // tolerate trailing dot
	// Strip optional brackets used by IPv6 literals.
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Unresolved / non-literal hostname. Refuse rather than
		// risking a DNS-mediated egress.
		return false
	}
	return ip.IsLoopback()
}

// IsLoopbackEndpoint extracts the host from a URL (or `host:port`
// string) and runs IsLoopbackHost over it.
func IsLoopbackEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return IsLoopbackHost(host)
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		// Not host:port; treat the whole string as a host literal.
		return IsLoopbackHost(endpoint)
	}
	return IsLoopbackHost(host)
}
