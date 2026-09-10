package webfetch

// White-box unit tests for pinnedDialContext (the WP02 DNS-rebinding fix).
// These use fake resolveIPFunc/dialFunc values — no real DNS server, no
// real network I/O, no timeouts — so they cover AC-4 (multi-address hosts,
// IPv6, CDN-style fallback, and a defined mixed public/private behaviour)
// fast and deterministically.
//
// This is deliberately separate from webfetch_rebinding_test.go, which
// drives the REAL net.DefaultResolver end to end through Tool.Call because
// that resolution path is the actual subject of the finding (AC-1/AC-2).
// Testing pinnedDialContext's own validation/iteration logic in isolation
// here does not weaken that: pinnedDialContext is new code the fix
// introduces, so there is no "must also compile/run against main"
// constraint on how its unit tests are built.

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

// dialRecorder is a fake dialFunc that records every address it was asked
// to dial (in order) and returns either a synthetic net.Conn or a
// preconfigured error per address.
type dialRecorder struct {
	mu      sync.Mutex
	calls   []string
	failFor map[string]error
}

func (r *dialRecorder) dial(_ context.Context, _ string, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.calls = append(r.calls, addr)
	failErr := r.failFor[addr]
	r.mu.Unlock()
	if failErr != nil {
		return nil, failErr
	}
	client, server := net.Pipe()
	_ = server.Close() // this test only needs a non-nil net.Conn, not a live pipe
	return client, nil
}

func (r *dialRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func fakeResolve(addrs []net.IPAddr, err error) resolveIPFunc {
	return func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return addrs, err
	}
}

func ipAddrs(ips ...string) []net.IPAddr {
	out := make([]net.IPAddr, len(ips))
	for i, s := range ips {
		out[i] = net.IPAddr{IP: net.ParseIP(s)}
	}
	return out
}

// ─── IP-literal fast path ────────────────────────────────────────────────────

func TestPinnedDialContext_IPLiteral_Blocked(t *testing.T) {
	rec := &dialRecorder{}
	dial := pinnedDialContext(fakeResolve(nil, nil), rec.dial)

	_, err := dial(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("expected an error dialing a loopback IP literal")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected a block-list error, got: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("dial should never have been invoked for a blocked literal, got calls=%v", calls)
	}
}

func TestPinnedDialContext_IPLiteral_Allowed(t *testing.T) {
	rec := &dialRecorder{}
	dial := pinnedDialContext(fakeResolve(nil, nil), rec.dial)

	_, err := dial(context.Background(), "tcp", "93.184.216.34:443")
	if err != nil {
		t.Fatalf("unexpected error dialing a public IP literal: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 1 || calls[0] != "93.184.216.34:443" {
		t.Errorf("expected exactly one dial to 93.184.216.34:443, got calls=%v", calls)
	}
}

// ─── AC-4: multi-address hosts, CDN-style fallback ──────────────────────────

func TestPinnedDialContext_MultiAddress_FallsBackOnDialError(t *testing.T) {
	rec := &dialRecorder{failFor: map[string]error{
		"203.0.113.1:443": net.ErrClosed, // first candidate: simulated dial failure
	}}
	resolve := fakeResolve(ipAddrs("203.0.113.1", "203.0.113.2"), nil)
	dial := pinnedDialContext(resolve, rec.dial)

	conn, err := dial(context.Background(), "tcp", "cdn.example.test:443")
	if err != nil {
		t.Fatalf("expected fallback to the second address to succeed, got: %v", err)
	}
	if conn == nil {
		t.Fatal("expected a non-nil conn from the successful fallback dial")
	}
	want := []string{"203.0.113.1:443", "203.0.113.2:443"}
	got := rec.snapshot()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("dial order = %v, want %v (try first, fall back to second on failure)", got, want)
	}
}

// ─── AC-4: IPv6 ──────────────────────────────────────────────────────────────

func TestPinnedDialContext_IPv6_Allowed(t *testing.T) {
	rec := &dialRecorder{}
	resolve := fakeResolve(ipAddrs("2001:db8::1"), nil) // RFC 3849 documentation space; not in the block list
	dial := pinnedDialContext(resolve, rec.dial)

	_, err := dial(context.Background(), "tcp", "ipv6-only.example.test:443")
	if err != nil {
		t.Fatalf("unexpected error dialing a public IPv6 host: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 1 || calls[0] != "[2001:db8::1]:443" {
		t.Errorf("expected exactly one dial to [2001:db8::1]:443, got calls=%v", calls)
	}
}

// ─── AC-4: mixed public + private addresses ─────────────────────────────────

func TestPinnedDialContext_MixedPublicPrivate_BlocksWholeHost(t *testing.T) {
	rec := &dialRecorder{}
	// One public-looking address and one loopback address in the SAME
	// resolution. Matches blockListCheck's policy: any blocked candidate
	// rejects the whole host — we do not cherry-pick "the safe one."
	resolve := fakeResolve(ipAddrs("93.184.216.34", "127.0.0.1"), nil)
	dial := pinnedDialContext(resolve, rec.dial)

	_, err := dial(context.Background(), "tcp", "mixed.example.test:443")
	if err == nil {
		t.Fatal("expected a mixed public/private address set to be blocked entirely")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected a block-list error, got: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("dial should never have been invoked for a mixed-address host, got calls=%v", calls)
	}
}

// ─── Resolution failure / empty answer ──────────────────────────────────────

func TestPinnedDialContext_ResolveError_Propagates(t *testing.T) {
	rec := &dialRecorder{}
	boom := &net.DNSError{Err: "no such host", Name: "nope.example.test", IsNotFound: true}
	dial := pinnedDialContext(fakeResolve(nil, boom), rec.dial)

	_, err := dial(context.Background(), "tcp", "nope.example.test:443")
	if err == nil {
		t.Fatal("expected the resolver error to propagate")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("dial should never have been invoked on resolve failure, got calls=%v", calls)
	}
}

func TestPinnedDialContext_EmptyAnswer_Errors(t *testing.T) {
	rec := &dialRecorder{}
	dial := pinnedDialContext(fakeResolve(nil, nil), rec.dial)

	_, err := dial(context.Background(), "tcp", "empty.example.test:443")
	if err == nil {
		t.Fatal("expected an error for a hostname with no resolved addresses")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("dial should never have been invoked with no addresses, got calls=%v", calls)
	}
}

// ─── SkipBlockList test seam: allowedHost bypass is host-scoped ─────────────

func TestPinnedDialContext_AllowedHostBypass_ExactHostOnly(t *testing.T) {
	rec := &dialRecorder{}
	resolveCalled := false
	resolve := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		resolveCalled = true
		return ipAddrs("127.0.0.1"), nil // would be blocked if pinning actually ran
	}
	dial := pinnedDialContext(resolve, rec.dial)

	ctx := withAllowedHost(context.Background(), "allowed.example.test")

	// The allowed host bypasses pinning entirely and dials the raw addr.
	if _, err := dial(ctx, "tcp", "allowed.example.test:8080"); err != nil {
		t.Fatalf("expected the allowed host to dial without validation, got: %v", err)
	}
	if resolveCalled {
		t.Error("resolve should never be consulted for the allowed host")
	}
	if calls := rec.snapshot(); len(calls) != 1 || calls[0] != "allowed.example.test:8080" {
		t.Errorf("expected exactly one dial to allowed.example.test:8080, got calls=%v", calls)
	}

	// A DIFFERENT host in the SAME context is not covered by the exemption
	// — this is what keeps a redirect target fully protected even when
	// SkipBlockList let the origin through.
	rec2 := &dialRecorder{}
	dial2 := pinnedDialContext(fakeResolve(ipAddrs("127.0.0.1"), nil), rec2.dial)
	if _, err := dial2(ctx, "tcp", "other-host.example.test:8080"); err == nil {
		t.Error("expected a different host in the same context to still be pinned/blocked")
	}
	if calls := rec2.snapshot(); len(calls) != 0 {
		t.Errorf("dial should never have been invoked for the non-exempt host, got calls=%v", calls)
	}
}
