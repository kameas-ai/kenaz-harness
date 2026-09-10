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

// ── isLiteralDialAddress ─────────────────────────────────────────────────

func TestIsLiteralDialAddress(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"LocalHost", true},
		{"127.0.0.1", true},
		{"::1", true},
		// Non-loopback private IP literal: still a literal — the user
		// typed an address directly, no DNS involved. See the doc
		// comment on isLiteralDialAddress for why this is deliberate.
		{"192.168.1.50", true},
		{"10.0.0.5", true},
		{"93.184.216.34", true}, // public IP literal is still a literal
		{"example.com", false},
		{"attacker-controlled.example.test", false},
		{"localhost.evil.com", false}, // NOT the literal "localhost"
	}
	for _, c := range cases {
		if got := isLiteralDialAddress(c.host); got != c.want {
			t.Errorf("isLiteralDialAddress(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// ── PinnedDialContext: literal bypass (AC-6 unit-level) ─────────────────────

func TestPinnedDialContext_LoopbackLiteral_BypassesResolutionAndBlockList(t *testing.T) {
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
		t.Error("resolve was called for a literal address; want bypass")
	}
	if dialedAddr != "127.0.0.1:3000" {
		t.Errorf("dialed %q, want 127.0.0.1:3000", dialedAddr)
	}
}

func TestPinnedDialContext_LocalhostLiteral_BypassesResolutionAndBlockList(t *testing.T) {
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

func TestPinnedDialContext_NonLoopbackPrivateIPLiteral_StillBypasses(t *testing.T) {
	// A deliberately-configured LAN MCP server (e.g. a Raspberry Pi at
	// 192.168.1.50) is the same kind of explicit, non-attacker-
	// influenceable choice as "localhost" — the recipe author wrote the
	// address directly, no DNS resolution occurred.
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
