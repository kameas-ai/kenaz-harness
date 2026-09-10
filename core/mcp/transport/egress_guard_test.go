package transport

// Unit-level, hermetic tests for the S-1 fix
// (paste-import-accepts-what-we-support-01PMZG16). These exercise
// PinnedDialContext's validation/fallback logic against fake
// resolve/dial functions — no real DNS, no real sockets — mirroring
// core/tools/webfetch's pinned_dial_internal_test.go pattern (PR #317).
// The end-to-end regression coverage that drives the real resolver and a
// real victim/loopback server lives in
// core/mcp/transport/http/rebinding_test.go: the DNS-rebinding gate
// (AC-5) and the deliberately-configured-loopback-still-connects proof
// (AC-6) in the same file.

import (
	"context"
	"errors"
	"net"
	"testing"
)

func mustIPAddr(t *testing.T, s string) net.IPAddr {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test IP literal %q", s)
	}
	return net.IPAddr{IP: ip}
}

// ── PinnedDialContext: literal handling — resolution skipped, validation
// NOT skipped ─────────────────────────────────────────────────────────────
//
// 2026-09-09 PR #324 review: an earlier version of this file treated
// "is a literal" and "skip validation entirely" as the same branch, which
// let a pasted recipe pointed at "http://169.254.169.254/..." (the cloud
// IMDS endpoint, a literal, not a hostname) reach a real dial completely
// unvalidated. The tests below pin the corrected rule: a literal always
// skips resolve() (there is nothing to resolve), but is still checked
// against checkLiteralIP before dial() is ever called.

func TestPinnedDialContext_LoopbackLiteral_ResolutionSkipped_Dials(t *testing.T) {
	resolveCalled := false
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		resolveCalled = true
		return nil, errors.New("resolve should never be called for a literal address")
	}
	var dialedAddr string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialedAddr = addr
		return &net.TCPConn{}, nil
	}

	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "127.0.0.1:3000")
	if err != nil {
		t.Fatalf("dial to loopback literal returned error: %v", err)
	}
	if resolveCalled {
		t.Error("resolve was called for a literal address; want resolution skipped")
	}
	if dialedAddr != "127.0.0.1:3000" {
		t.Errorf("dialed %q, want 127.0.0.1:3000", dialedAddr)
	}
}

func TestPinnedDialContext_IPv6LoopbackLiteral_ResolutionSkipped_Dials(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should never be called for a literal address")
		return nil, nil
	}
	var dialedAddr string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialedAddr = addr
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "[::1]:3000")
	if err != nil {
		t.Fatalf("dial to IPv6 loopback literal returned error: %v", err)
	}
	if dialedAddr != "[::1]:3000" {
		t.Errorf("dialed %q, want [::1]:3000", dialedAddr)
	}
}

func TestPinnedDialContext_LocalhostLiteral_ResolutionSkipped_Dials(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should never be called for the literal host %q", host)
		return nil, nil
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	if _, err := dc(context.Background(), "tcp", "localhost:8080"); err != nil {
		t.Fatalf("dial to localhost literal returned error: %v", err)
	}
}

func TestPinnedDialContext_RFC1918Literal_Allowed(t *testing.T) {
	// A deliberately-configured LAN MCP server (e.g. a Raspberry Pi at
	// 192.168.1.50) is the same kind of explicit, non-attacker-
	// influenceable choice as "localhost" — the recipe author wrote the
	// address directly, no DNS resolution occurred, and RFC-1918 space is
	// a plausible real server location (unlike link-local — see the
	// tests below).
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should never be called for a literal IP")
		return nil, nil
	}
	var dialedAddr string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialedAddr = addr
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	if _, err := dc(context.Background(), "tcp", "192.168.1.50:8080"); err != nil {
		t.Fatalf("dial to private IP literal returned error: %v", err)
	}
	if dialedAddr != "192.168.1.50:8080" {
		t.Errorf("dialed %q, want 192.168.1.50:8080", dialedAddr)
	}
}

// ── PinnedDialContext: link-local literal is BLOCKED, not bypassed ──────────
//
// This is the exact bypass the review caught: 169.254.169.254 is a
// numeric IP literal, so it never goes through resolve() — but that must
// not mean it skips validation. checkLiteralIP still runs and still
// blocks it.

func TestPinnedDialContext_LinkLocalLiteral_Blocked(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should never be called for a literal IP")
		return nil, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	// The cloud instance-metadata endpoint, pasted as a literal IP —
	// exactly the payload the review demonstrated reaching a real dial
	// on the pre-fix code.
	_, err := dc(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected error dialing a link-local literal, got nil")
	}
	if dialed {
		t.Error("dial was invoked for a link-local literal (169.254.169.254) — IMDS bypass")
	}
}

func TestPinnedDialContext_IPv6LinkLocalLiteral_Blocked(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should never be called for a literal IP")
		return nil, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "[fe80::1]:80")
	if err == nil {
		t.Fatal("expected error dialing an IPv6 link-local literal, got nil")
	}
	if dialed {
		t.Error("dial was invoked for an IPv6 link-local literal")
	}
}

// ── PinnedDialContext: hostname resolution path ──────────────────────────

func TestPinnedDialContext_HostnameResolvesToPrivate_Blocked(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{mustIPAddr(t, "10.0.0.5")}, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "attacker.example.test:443")
	if err == nil {
		t.Fatal("expected error for hostname resolving to a private address, got nil")
	}
	if dialed {
		t.Error("dial was invoked for a blocked address")
	}
}

func TestPinnedDialContext_HostnameResolvesToPublic_Dials(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{mustIPAddr(t, "93.184.216.34")}, nil
	}
	var dialedAddr string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialedAddr = addr
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "api.example.com:443")
	if err != nil {
		t.Fatalf("dial to public-resolving hostname returned error: %v", err)
	}
	if dialedAddr != "93.184.216.34:443" {
		t.Errorf("dialed %q, want 93.184.216.34:443", dialedAddr)
	}
}

func TestPinnedDialContext_MixedPublicAndPrivate_RejectedEntirely(t *testing.T) {
	// A host presenting both a public and a private address is itself a
	// known SSRF/rebinding technique — reject outright rather than
	// cherry-pick "the safe one" (matches web_fetch's blockListCheck
	// policy from PR #317).
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			mustIPAddr(t, "93.184.216.34"),
			mustIPAddr(t, "10.0.0.5"),
		}, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "mixed.example.test:443")
	if err == nil {
		t.Fatal("expected error for mixed public/private address set, got nil")
	}
	if dialed {
		t.Error("dial was invoked despite a blocked candidate in the address set")
	}
}

func TestPinnedDialContext_MultiAddressFallback(t *testing.T) {
	// CDN-style host: first candidate fails to connect, second succeeds.
	// Both candidates are public, so both pass validation.
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			mustIPAddr(t, "93.184.216.34"),
			mustIPAddr(t, "93.184.216.35"),
		}, nil
	}
	var attempted []string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		attempted = append(attempted, addr)
		if addr == "93.184.216.34:443" {
			return nil, errors.New("connection refused")
		}
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "cdn.example.test:443")
	if err != nil {
		t.Fatalf("expected fallback to the second address to succeed, got: %v", err)
	}
	if len(attempted) != 2 {
		t.Fatalf("attempted = %v, want 2 dial attempts", attempted)
	}
}

func TestPinnedDialContext_IPv6Loopback_Blocked(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{mustIPAddr(t, "::1")}, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "ipv6-loopback.example.test:443")
	if err == nil {
		t.Fatal("expected error for hostname resolving to IPv6 loopback, got nil")
	}
	if dialed {
		t.Error("dial was invoked for a blocked IPv6 address")
	}
}

func TestPinnedDialContext_ResolutionError_Propagates(t *testing.T) {
	wantErr := errors.New("boom")
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, wantErr
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "broken.example.test:443")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapping %v", err, wantErr)
	}
	if dialed {
		t.Error("dial was invoked despite a resolution error")
	}
}

func TestPinnedDialContext_EmptyAnswerSet_Errors(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, nil
	}
	dialed := false
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return &net.TCPConn{}, nil
	}
	dc := PinnedDialContext(resolve, dial)
	_, err := dc(context.Background(), "tcp", "nowhere.example.test:443")
	if err == nil {
		t.Fatal("expected error for an empty answer set, got nil")
	}
	if dialed {
		t.Error("dial was invoked despite an empty answer set")
	}
}

func TestPinnedDialContext_BadAddrFormat_Errors(t *testing.T) {
	resolve := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		t.Fatalf("resolve should not be called when addr fails to split")
		return nil, nil
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Fatalf("dial should not be called when addr fails to split")
		return nil, nil
	}
	dc := PinnedDialContext(resolve, dial)
	if _, err := dc(context.Background(), "tcp", "not-a-host-port"); err == nil {
		t.Fatal("expected error for malformed addr, got nil")
	}
}

// ── checkEgressIP ─────────────────────────────────────────────────────────

func TestCheckEgressIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.0.1",
		"169.254.169.254", "fd00::1", "fe80::1",
	}
	for _, ip := range blocked {
		if err := checkEgressIP(net.ParseIP(ip)); err == nil {
			t.Errorf("checkEgressIP(%q) = nil, want blocked", ip)
		}
	}
	allowed := []string{"93.184.216.34", "8.8.8.8", "2606:4700:4700::1111"}
	for _, ip := range allowed {
		if err := checkEgressIP(net.ParseIP(ip)); err != nil {
			t.Errorf("checkEgressIP(%q) = %v, want allowed", ip, err)
		}
	}
}

// TestCheckLiteralIP pins the narrower list applied to a URL host that
// is itself a numeric IP literal: link-local (incl. IMDS) blocked,
// everything else checkEgressIP blocks — loopback, RFC-1918, IPv6 ULA —
// deliberately ALLOWED here, because those are real self-hosted MCP
// server locations a recipe author can legitimately type directly.
func TestCheckLiteralIP(t *testing.T) {
	blocked := []string{"169.254.169.254", "169.254.1.1", "fe80::1"}
	for _, ip := range blocked {
		if err := checkLiteralIP(net.ParseIP(ip)); err == nil {
			t.Errorf("checkLiteralIP(%q) = nil, want blocked", ip)
		}
	}
	allowed := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.0.1",
		"fd00::1", "93.184.216.34", "8.8.8.8",
	}
	for _, ip := range allowed {
		if err := checkLiteralIP(net.ParseIP(ip)); err != nil {
			t.Errorf("checkLiteralIP(%q) = %v, want allowed (literal-address rule)", ip, err)
		}
	}
}
