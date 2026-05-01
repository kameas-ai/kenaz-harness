// Package httpx provides shared HTTP plumbing for LLM adapters —
// notably a Transport with OS-level TCP keepalive enabled so long-
// lived SSE streams don't get edge-proxy idle-timeouted during quiet
// model-reasoning stretches.
//
// The default Go http.Transport relies on HTTP/2 frame-level pings
// (and only for HTTP/2) to keep the wire alive. That's not enough
// when an upstream model can sit silent for minutes between SSE
// deltas: an edge proxy (CloudFront, Cloudflare, OpenRouter's own
// gateway) idle-timeouts the underlying TCP socket and the next
// scanner read returns EOF wrapped as "transient provider error:
// Network connection lost". Setting net.Dialer.KeepAlive arms the
// kernel to send TCP keepalive probes during idle, which keeps the
// NAT / edge-proxy connection table entry warm.
//
// All four LLM adapters (anthropic, openai, openrouter, bedrock)
// construct their default *http.Client through DefaultTransport().
// Tests that pass WithHTTPClient(...) still win — the option swaps
// the entire client out, so the keepalive transport is only the
// production default.
package httpx

import (
	"net"
	"net/http"
	"time"
)

// keepalivePeriod is the OS-level TCP keepalive ping interval. 30s
// is short enough to defeat the typical 60–300s edge-proxy idle
// timeouts we've observed on OpenRouter / DeepSeek without putting
// meaningful load on the network.
const keepalivePeriod = 30 * time.Second

// defaultDialer returns the *net.Dialer used by DefaultTransport.
// Exposed as a package-internal seam so tests can introspect the
// dialer's KeepAlive setting directly without resorting to
// reflection on the transport's DialContext closure.
//
// Notes on the field choices:
//   - Timeout is the connect timeout; long-lived streams reuse an
//     idle pooled connection and so don't pay this cost per turn.
//   - KeepAlive=30s sets SO_KEEPALIVE + TCP_KEEPINTVL on the socket
//     once it's established. Idle connections are probed every 30s.
func defaultDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: keepalivePeriod,
	}
}

// DefaultTransport returns an *http.Transport configured for long-
// lived LLM streaming connections. Notable choices:
//   - net.Dialer.KeepAlive=30s — OS sends TCP keepalive pings every
//     30s during idle so edge proxies don't kill the connection.
//   - No request-level timeout — adapters use context.Context for
//     lifetime control (matches existing adapter conventions).
//   - HTTP/2 forced; matches the production behaviour every adapter
//     was already getting via the default transport.
//
// Each call returns a fresh *http.Transport. Adapters cache the
// result inside their *http.Client; the connection pool lives on
// the transport, so sharing one transport across adapter instances
// would also be fine but isn't required.
func DefaultTransport() *http.Transport {
	d := defaultDialer()
	return &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
