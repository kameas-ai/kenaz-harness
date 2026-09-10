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
// loopback/LAN use case webfetch does not. web_fetch is a model-facing
// tool where every private address is inherently suspicious — nothing
// the model asks to fetch should reach the user's own machine. An MCP
// recipe's URL, by contrast, is frequently *meant* to be
// "http://localhost:PORT" or "http://192.168.1.50:PORT" — a local or
// LAN MCP server the user is deliberately running.
//
// THE RULE, STATED PLAINLY (see the 2026-09-09 PR #324 review — an
// earlier version of this file got this wrong): a URL host that is a
// numeric IP literal or the name "localhost" never goes through DNS
// resolution, so there is no rebinding window for it — nothing an
// attacker can flip between a check and a connect. But "no resolution"
// is not "no validation": a literal is still checked, against
// literalBlockedIPNets below, which is DELIBERATELY NARROWER than
// egressBlockedIPNets (the list applied to a resolved hostname).
// Loopback and RFC-1918/IPv6-ULA space are excluded from the literal
// check because they are exactly where a real self-hosted MCP server
// lives. Link-local space (169.254.0.0/16, fe80::/10) is NOT excluded —
// it stays blocked even as a literal, because it is never a plausible
// place to run a real MCP server, and 169.254.169.254 is precisely the
// cloud instance-metadata endpoint attackers use for credential theft.
// A hostname, by contrast, is always resolved and validated against the
// FULL block list (loopback + RFC-1918 + link-local + IPv6 ULA) at every
// dial, because DNS resolution is exactly the step an attacker can
// manipulate — nothing about a hostname's destination is something the
// user typed and can vouch for.
//
// This is a narrower carve-out than PR #317's pinnedDialContext for
// web_fetch, not the same rule reused: #317 skips *resolution* for a
// literal but still runs it through the (there, unconditional) block
// list — every private address is checked, because web_fetch has no
// legitimate loopback/LAN use case. This file also skips resolution for
// a literal, but validates it against a DIFFERENT, MCP-specific list
// that permits loopback/RFC-1918/ULA (real server locations) while still
// blocking link-local (never a real server location, and the IMDS
// range) — because unlike web_fetch, blocking every private address
// here would break the normal "point a recipe at your local MCP server"
// configuration.

// egressBlockedIPNets is the full F-002 SSRF block list web_fetch
// enforces: loopback, RFC-1918 private ranges, link-local (including
// the 169.254.169.254 cloud IMDS endpoint), and IPv6 unique-local/
// link-local. Applied to every address a HOSTNAME resolves to — see
// this file's top doc comment for why literals get a narrower list
// instead. Kept as a separate literal here (rather than an exported
// symbol re-imported from core/tools/webfetch) to avoid a tools/ <->
// mcp/ dependency; the two lists must be kept in sync by hand if the
// policy changes.
var egressBlockedIPNets []*net.IPNet

// literalBlockedIPNets is the list applied to a URL host that is
// ITSELF a numeric IP literal — no DNS resolution involved. It is a
// strict subset of egressBlockedIPNets: only link-local space, because
// that is the one range in the full list that is never a plausible
// place to run a real MCP server and is where cloud credential-theft
// targets (169.254.169.254) live. Loopback, RFC-1918, and IPv6 ULA are
// intentionally NOT in this list — see this file's top doc comment.
var literalBlockedIPNets []*net.IPNet

func init() {
	fullCIDRs := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"::1/128",        // IPv6 loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // IPv4 link-local (incl. AWS/Azure/GCP IMDS)
		"fd00::/8",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	}
	for _, cidr := range fullCIDRs {
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			egressBlockedIPNets = append(egressBlockedIPNets, ipNet)
		}
	}

	literalCIDRs := []string{
		"169.254.0.0/16", // IPv4 link-local (incl. AWS/Azure/GCP IMDS)
		"fe80::/10",      // IPv6 link-local
	}
	for _, cidr := range literalCIDRs {
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			literalBlockedIPNets = append(literalBlockedIPNets, ipNet)
		}
	}
}

// checkEgressIP returns an error if ip falls in a blocked range for a
// RESOLVED HOSTNAME (the full block list — loopback, RFC-1918,
// link-local, IPv6 ULA).
func checkEgressIP(ip net.IP) error {
	for _, blocked := range egressBlockedIPNets {
		if blocked.Contains(ip) {
			return fmt.Errorf("mcp transport: connection to private/reserved address %s is blocked (SSRF protection)", ip)
		}
	}
	return nil
}

// checkLiteralIP returns an error if ip falls in a blocked range for a
// URL host that is ITSELF a numeric IP literal (the narrow list — link-
// local only). See this file's top doc comment for why loopback and
// RFC-1918/ULA space are deliberately allowed here even though
// checkEgressIP blocks them for a resolved hostname.
func checkLiteralIP(ip net.IP) error {
	for _, blocked := range literalBlockedIPNets {
		if blocked.Contains(ip) {
			return fmt.Errorf("mcp transport: connection to link-local address %s is blocked (cloud metadata / IMDS protection)", ip)
		}
	}
	return nil
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
// that closes the DNS-rebinding TOCTOU the way PR #317 closed it for
// web_fetch — resolve, validate, dial the validated address, with no
// separate "check" step the connection can silently disagree with — but
// with an MCP-specific validation list for the literal-address case. See
// this file's top doc comment for the full rule and why it is narrower
// than a straight reuse of #317's.
//
// http.Transport invokes DialContext identically for a request's first
// connection and for every reconnection (the MCP HTTP and SSE transports
// both disable redirect-following outright — see their Open() — so the
// only "reconnection" case in practice is idle-connection churn and the
// SSE reconnect loop, both of which go through this same function).
//
// Three cases, ALL of which are validated — never skipped outright:
//
//  1. host is "localhost" (case-insensitive): always resolves to
//     loopback via the OS, not attacker-influenceable DNS. Dialed
//     directly — loopback is always allowed regardless of which list
//     applies.
//  2. host is a numeric IP literal: no resolution occurs, so validated
//     against checkLiteralIP (link-local only — see top doc comment)
//     rather than skipped. A literal 169.254.169.254 is blocked here.
//  3. host is anything else (a real hostname): resolved via resolve;
//     every candidate must pass checkEgressIP (the full list), then
//     each is tried in order until one connects (matching net/http's
//     own multi-A-record fallback behaviour). A host resolving to both
//     a public and a private address is rejected outright rather than
//     cherry-picked, matching the block list's policy for a single
//     address.
func PinnedDialContext(resolve ResolveIPFunc, dial DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		bareHost := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")

		if strings.EqualFold(bareHost, "localhost") {
			return dial(ctx, network, addr)
		}

		if litIP := net.ParseIP(bareHost); litIP != nil {
			if err := checkLiteralIP(litIP); err != nil {
				return nil, err
			}
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
