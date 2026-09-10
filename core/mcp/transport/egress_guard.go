package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// ── S-1: MCP HTTP/SSE transports connect to the address they validated ──
//
// Before this file, core/mcp/transport/http and core/mcp/transport/sse
// built a bare &http.Client{CheckRedirect: ...} with no Transport, so
// every outbound POST/GET dialed through http.DefaultTransport with no
// address validation at all — not even a bypassable block list. That was
// tolerable only because a recipe's URL came from the curated shipped
// catalog or a hand-typed config. The paste-import mission
// (paste-import-accepts-what-we-support-01PMZG16) makes an arbitrary
// pasted URL a supported input, which removes that bound: a hostname an
// attacker controls could answer publicly when (if anything) validates it
// and privately when the transport actually connects — the same DNS-
// rebinding TOCTOU PR #317 closed for core/tools/webfetch.
//
// This file ports that fix — resolve once, validate, dial the validated
// address — to the MCP transport layer, reusing the same mechanism rather
// than inventing a second one that can drift. It deliberately does NOT
// import core/tools/webfetch (that would be a layering inversion: a
// tools/ package reaching down into core/mcp/transport, or vice versa)
// and does not touch core/llm/ (out of scope for both missions — the
// shared LLM httpx transport is guarded separately, if at all, and two of
// its adapters legitimately dial loopback for local models).
//
// The one deliberate difference from webfetch: MCP has a legitimate
// loopback use case webfetch does not. web_fetch is a model-facing tool
// where every private address is inherently suspicious — nothing the
// model asks to fetch should reach the user's own machine. An MCP
// recipe's URL, by contrast, is frequently *meant* to be
// "http://localhost:PORT" — a local MCP server the user is deliberately
// running. See isLiteralDialAddress for how that distinction is drawn.

// egressBlockedIPNets is the same F-002 SSRF block list webfetch enforces:
// loopback, RFC-1918 private ranges, link-local (including the
// 169.254.169.254 cloud IMDS endpoint), and IPv6 unique-local/link-local.
// Kept as a separate literal here (rather than an exported symbol
// re-imported from core/tools/webfetch) to avoid a tools/ <-> mcp/
// dependency; the two lists must be kept in sync by hand if the policy
// changes.
var egressBlockedIPNets []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"::1/128",        // IPv6 loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // IPv4 link-local (incl. AWS/Azure IMDS)
		"fd00::/8",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	}
	for _, cidr := range cidrs {
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			egressBlockedIPNets = append(egressBlockedIPNets, ipNet)
		}
	}
}

// checkEgressIP returns an error if ip falls in a blocked range.
func checkEgressIP(ip net.IP) error {
	for _, blocked := range egressBlockedIPNets {
		if blocked.Contains(ip) {
			return fmt.Errorf("mcp transport: connection to private/reserved address %s is blocked (SSRF protection)", ip)
		}
	}
	return nil
}

// isLiteralDialAddress reports whether host — as written in the recipe's
// URL, BEFORE any DNS resolution — is either a numeric IP literal or the
// canonical name "localhost".
//
// This is the rule that separates "the user deliberately configured
// http://localhost:3000" from "a public hostname resolved to a private
// address": a literal has no DNS lookup between what the recipe says and
// what gets dialed, so there is nothing for an attacker to rebind. A
// hostname the user typed, by contrast, is resolved fresh on every dial —
// that resolution is exactly the step DNS-rebinding attacks exploit, so
// hostnames always go through the resolve-validate-dial path below,
// regardless of what address they happen to resolve to. This deliberately
// covers non-loopback private-IP literals too (e.g. a self-hosted MCP
// server at 192.168.1.50): typing an IP directly is the same kind of
// explicit, non-attacker-influenceable choice as typing "localhost".
func isLiteralDialAddress(host string) bool {
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host) != nil
}

// ResolveIPFunc abstracts hostname resolution for PinnedDialContext.
// Production passes net.DefaultResolver.LookupIPAddr; tests substitute a
// fake so validation/fallback logic can be exercised without real DNS
// I/O. The DNS-rebinding regression test in the http package drives the
// real resolver end-to-end instead, because that resolution path is the
// subject of the finding.
type ResolveIPFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// DialFunc abstracts the underlying connect primitive for
// PinnedDialContext. Production passes a *net.Dialer's DialContext method
// value; tests substitute a fake that records calls without touching a
// socket.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// PinnedDialContext returns an http.Transport.DialContext implementation
// that closes the DNS-rebinding TOCTOU the same way PR #317 closed it for
// web_fetch: it resolves the dial's hostname itself, validates every
// candidate address against the block list, and connects only to a
// validated address. There is no separate "check" step whose result the
// connection can silently disagree with.
//
// http.Transport invokes DialContext identically for a request's first
// connection and for every reconnection (the MCP HTTP and SSE transports
// both disable redirect-following outright — see their Open() — so the
// only "reconnection" case in practice is idle-connection churn and the
// SSE reconnect loop, both of which go through this same function).
//
// Literal addresses (numeric IPs, "localhost") bypass validation — see
// isLiteralDialAddress. Everything else is resolved via resolve; every
// candidate must pass the block list, then each is tried in order until
// one connects (matching net/http's own multi-A-record fallback
// behaviour). A host resolving to both a public and a private address is
// rejected outright rather than cherry-picked, matching the block list's
// policy for a single address.
func PinnedDialContext(resolve ResolveIPFunc, dial DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		if isLiteralDialAddress(host) {
			return dial(ctx, network, addr)
		}

		addrs, err := resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("mcp transport: could not resolve host %q", host)
		}
		for _, a := range addrs {
			if err := checkEgressIP(a.IP); err != nil {
				return nil, err
			}
		}

		var lastErr error
		for _, a := range addrs {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

// GuardedHTTPTransport returns an *http.Transport tuned to match
// http.DefaultTransport (so ordinary HTTP/HTTPS behaviour — proxying,
// keep-alive, HTTP/2, TLS timeouts — is unchanged) except DialContext is
// PinnedDialContext, so every dial made through it is protected against
// DNS rebinding per this file's doc comment.
//
// The http and sse transport packages call this instead of leaving
// Transport nil (which would fall back to the unguarded
// http.DefaultTransport) whenever a Connection is opened without a
// caller-supplied HTTPClient. Tests that inject their own HTTPClient
// (httptest.Server.Client()) bypass this entirely, same as
// core/tools/webfetch's SkipBlockList test path — that is the test's own
// client against its own fake server, not production dial code.
func GuardedHTTPTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           PinnedDialContext(net.DefaultResolver.LookupIPAddr, dialer.DialContext),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
