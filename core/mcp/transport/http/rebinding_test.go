package http_test

// WP01/WP02 — DNS-rebinding / unvalidated-dial regression harness for the
// MCP HTTP transport. See
// kitty-specs/paste-import-accepts-what-we-support-01PMZG16/spec.md §3,
// which ports the finding kitty-specs/egress-guards-that-hold-01PMZF15
// WP01 established for core/tools/webfetch to this package.
//
// One deliberate difference from the webfetch harness: web_fetch's
// pre-fix code ran an explicit (bypassable) pre-flight block-list check
// via net.LookupHost BEFORE the request, so its regression test needs
// TWO distinct DNS answers — one to satisfy the pre-flight, a different
// one to reach the transport's own separate, later resolution. The MCP
// HTTP transport's pre-fix code (core/mcp/transport/http/connection.go)
// has NO pre-flight check of any kind — spec §3 confirms "no
// connection-time address validation at all" — so there is only ONE
// resolution in this transport's dial path, at connect time. A single
// DNS answer already demonstrates the full defect: pre-fix, whatever
// that one lookup returns is dialed unconditionally; post-fix
// (PinnedDialContext in core/mcp/transport/egress_guard.go), that same
// one lookup is validated before the dial proceeds. This also means the
// fix has no separate check-then-connect window to race at all — each
// dial (initial or reconnect) resolves and validates atomically — so a
// public/private two-stage rebind would not exercise anything a
// single-stage "hostname resolves to a private address" test does not
// already cover for this architecture.
//
// The harness still runs a REAL local UDP DNS server and installs it as
// net.DefaultResolver for the duration of the test, so the exact
// resolution code path production code uses is exercised unmodified,
// with no test-only seam added to the Connection's public API. The same
// file runs against the pre-fix code (where it must FAIL, observing the
// real bypass — the mission's WP01 stop condition) and the post-fix code
// (where it must pass) with zero changes.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	httptransport "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
)

// ─── Fake authoritative DNS server ──────────────────────────────────────────

// staticDNSServer is a minimal local DNS server that answers every A
// query for any name with the same fixed address — standing in for an
// attacker-controlled hostname (whether that address was reached via a
// persistent record or a rebind is immaterial to the defect this test
// demonstrates; see the file doc comment).
type staticDNSServer struct {
	conn *net.UDPConn
	ip   net.IP
}

func startStaticDNSServer(t *testing.T, answer net.IP) *staticDNSServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	s := &staticDNSServer{conn: conn, ip: answer}
	go s.serve()
	t.Cleanup(func() { conn.Close() })
	return s
}

func (s *staticDNSServer) addr() string { return s.conn.LocalAddr().String() }

func (s *staticDNSServer) serve() {
	buf := make([]byte, 512)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // listener closed at test cleanup
		}
		pkt := append([]byte(nil), buf[:n]...)
		go s.handle(pkt, addr)
	}
}

func (s *staticDNSServer) handle(query []byte, addr *net.UDPAddr) {
	var m dnsmessage.Message
	if err := m.Unpack(query); err != nil || len(m.Questions) == 0 {
		return
	}
	q := m.Questions[0]

	resp := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 m.Header.ID,
			Response:           true,
			Authoritative:      true,
			RecursionDesired:   m.Header.RecursionDesired,
			RecursionAvailable: true,
		},
		Questions: []dnsmessage.Question{q},
	}

	if q.Type == dnsmessage.TypeA {
		if ip4 := s.ip.To4(); ip4 != nil {
			var a [4]byte
			copy(a[:], ip4)
			resp.Answers = append(resp.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 0},
				Body:   &dnsmessage.AResource{A: a},
			})
		}
	}
	// AAAA (and anything else) -> NOERROR with no records; this harness
	// only drives the IPv4 case. Multi-address/IPv6 fallback coverage
	// lives in the unit-level dial-context tests (egress_guard_test.go).

	out, err := resp.Pack()
	if err != nil {
		return
	}
	_, _ = s.conn.WriteToUDP(out, addr)
}

// withStaticResolver installs a *net.Resolver that forces the pure-Go
// resolver and redirects every DNS query to dnsAddr, restoring the
// previous net.DefaultResolver at test cleanup. Any in-process code
// resolving via net.DefaultResolver — net.DefaultResolver.LookupIPAddr
// (post-fix) and a bare *net.Dialer's own internal resolution when
// dialing a hostname (pre-fix) — is affected identically, with no
// production code changes required to drive this test both ways.
func withStaticResolver(t *testing.T, dnsAddr string) {
	t.Helper()
	prev := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", dnsAddr)
		},
	}
	t.Cleanup(func() { net.DefaultResolver = prev })
}

// mustParallelGuard serialises this file's tests against net.DefaultResolver
// mutation racing other packages' tests. Package-level (not process-global)
// since net.DefaultResolver is a process global but Go only runs one test
// binary per package.
var resolverMu sync.Mutex

// ─── WP01 falsification gate ────────────────────────────────────────────────

// TestHTTPConnection_UnvalidatedDial_ReachesPrivateAddress is the WP01
// falsification gate for S-1
// (paste-import-accepts-what-we-support-01PMZG16 spec §3): a hostname an
// attacker controls resolves to a private/loopback address — the real
// bind address of a local victim server — and the transport must not
// connect to it.
//
// Pre-fix (spec.HTTPClient nil -> bare &http.Client{} -> Transport nil
// -> http.DefaultTransport dials): this test FAILS, i.e. the victim is
// reached, because nothing anywhere in the HTTP transport validates the
// address before connecting. Post-fix
// (transport.GuardedHTTPTransport's PinnedDialContext): it PASSES —
// blocked, victim never reached. Same file, unmodified, both runs — this
// is the mission's stop condition per tasks.md WP01: "This WP succeeds
// when the test FAILS on current main."
func TestHTTPConnection_UnvalidatedDial_ReachesPrivateAddress(t *testing.T) {
	resolverMu.Lock()
	defer resolverMu.Unlock()

	victimReached := false
	victim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		victimReached = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	}))
	defer victim.Close()

	victimHost, victimPort, err := net.SplitHostPort(strings.TrimPrefix(victim.URL, "http://"))
	if err != nil {
		t.Fatalf("split victim addr: %v", err)
	}
	victimIP := net.ParseIP(victimHost)
	if victimIP == nil {
		t.Fatalf("victim server did not bind to an IP literal: %q", victimHost)
	}

	dns := startStaticDNSServer(t, victimIP)
	withStaticResolver(t, dns.addr())

	// Deliberately NOT an IP literal and NOT "localhost" — a real
	// hostname is what makes this go through resolution at all. A
	// literal address goes through PinnedDialContext's literal branch in
	// egress_guard.go, which validates it against the narrower
	// literalBlockedIPNets list instead of the resolved-hostname path
	// below — that is the separate, correct behaviour AC-6 covers, not
	// this test.
	conn := httptransport.NewConnection(httptransport.Spec{
		ID:  "rebind",
		URL: fmt.Sprintf("http://attacker-controlled.egress-guards.test:%s/mcp", victimPort),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.Send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := recvWithTimeout(t, conn, 5*time.Second)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}

	if victimReached {
		t.Errorf("unvalidated dial: the private victim address was reached")
	}
	if msg.Error == nil {
		t.Errorf("expected the dial to a private address to be blocked (JSON-RPC error envelope), got result %s", string(msg.Result))
	}
}

// ─── AC-6: deliberately-configured loopback still connects ─────────────────

// TestHTTPConnection_LoopbackLiteral_StillConnects is AC-6: a recipe
// deliberately pointed at a loopback literal — a local MCP server the user
// is running themselves — must keep working through the SAME default
// client the DNS-rebinding gate above hardens. This is not a redirect or a
// custom-HTTPClient test escape hatch: spec.HTTPClient is left nil, so
// Connection.Open builds transport.GuardedHTTPTransport() exactly as
// production does, and the request goes out over a real TCP connection to
// a real server.
//
// Together with TestHTTPConnection_UnvalidatedDial_ReachesPrivateAddress
// above, this is the pair spec §3 calls out: "if AC-5 and AC-6 cannot both
// hold, that is an escalation." They hold simultaneously here because the
// guard's rule is keyed on whether the URL's host is a literal address (no
// DNS involved — see egress_guard.go's top doc comment) validated against
// an MCP-specific list that permits loopback/RFC-1918/ULA, not on whether
// resolution is skipped outright: "127.0.0.1"/"[::1]" written directly in
// the recipe are literals that pass that narrower check; "attacker-
// controlled.egress-guards.test" above is a hostname that must be resolved
// and validated against the full block list, which is exactly the step
// this fix protects.
//
// Covers both IPv4 and IPv6 loopback end-to-end (real sockets). RFC-1918
// literal coverage (e.g. a LAN MCP server at 192.168.x.x) is at the unit
// level instead — TestPinnedDialContext_RFC1918Literal_Allowed in
// egress_guard_test.go — because this test binds a real listener and a
// non-loopback private address is not reliably bindable in a CI sandbox.
func TestHTTPConnection_LoopbackLiteral_StillConnects(t *testing.T) {
	cases := []struct {
		name    string
		network string
		bindIP  string
	}{
		{name: "IPv4", network: "tcp4", bindIP: "127.0.0.1"},
		{name: "IPv6", network: "tcp6", bindIP: "::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ln, err := net.Listen(c.network, net.JoinHostPort(c.bindIP, "0"))
			if err != nil {
				t.Skipf("cannot bind %s loopback in this environment: %v", c.name, err)
			}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"local_tool"}]}}`)
			}))
			srv.Listener = ln
			srv.Start()
			defer srv.Close()

			// srv.URL is "http://127.0.0.1:PORT" or "http://[::1]:PORT" — an
			// IP literal, not a hostname. spec.HTTPClient is deliberately
			// left unset.
			conn := httptransport.NewConnection(httptransport.Spec{
				ID:  "local-" + c.name,
				URL: srv.URL,
			}, nil)

			if err := conn.Open(context.Background()); err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = conn.Close() })

			if err := conn.Send(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			}); err != nil {
				t.Fatalf("Send: %v", err)
			}

			msg, err := recvWithTimeout(t, conn, 5*time.Second)
			if err != nil {
				t.Fatalf("Recv: %v", err)
			}
			if msg.Error != nil {
				t.Fatalf("deliberately-configured loopback server was blocked: %+v", msg.Error)
			}
			if !strings.Contains(string(msg.Result), "local_tool") {
				t.Errorf("result = %s, want it to contain local_tool", string(msg.Result))
			}
		})
	}
}

// recvWithTimeout wraps conn.Recv with a hard deadline so a bug that
// hangs instead of erroring doesn't hang the test suite.
func recvWithTimeout(t *testing.T, conn *httptransport.Connection, d time.Duration) (transport.RawMessage, error) {
	t.Helper()
	type result struct {
		msg transport.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := conn.Recv()
		ch <- result{msg: msg, err: err}
	}()
	select {
	case r := <-ch:
		return r.msg, r.err
	case <-time.After(d):
		t.Fatalf("Recv timed out after %v", d)
		return transport.RawMessage{}, nil
	}
}
