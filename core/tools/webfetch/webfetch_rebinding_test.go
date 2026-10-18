package webfetch_test

// WP01/WP02 — DNS-rebinding regression harness. See
// kitty-specs/egress-guards-that-hold-01PMZF15/spec.md.
//
// The harness runs a REAL local UDP DNS server and installs it as
// net.DefaultResolver for the duration of each test. This means the exact
// same resolution code path webfetch.go itself uses (net.LookupHost /
// net.DefaultResolver.LookupIPAddr, and — pre-fix — whatever
// http.DefaultTransport does internally when it dials) is exercised
// unmodified. No test-only seam is added to webfetch's production API for
// this: the same test file runs against the pre-fix code (where it must
// fail, observing the real bypass — AC-2) and the post-fix code (where it
// must pass) with zero changes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
)

// ─── Fake authoritative DNS server ──────────────────────────────────────────

// rebindDNSServer is a minimal local DNS server that answers the Nth A
// query for a given name with `private` once N>=2, and with `public` on the
// first query — simulating a hostname an attacker controls that answers
// differently at check-time versus connect-time (DNS rebinding).
type rebindDNSServer struct {
	conn    *net.UDPConn
	mu      sync.Mutex
	aCount  map[string]int
	public  net.IP
	private net.IP
}

func startRebindDNSServer(t *testing.T, public, private net.IP) *rebindDNSServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	s := &rebindDNSServer{conn: conn, aCount: map[string]int{}, public: public, private: private}
	go s.serve()
	t.Cleanup(func() { conn.Close() })
	return s
}

func (s *rebindDNSServer) addr() string { return s.conn.LocalAddr().String() }

func (s *rebindDNSServer) serve() {
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

func (s *rebindDNSServer) handle(query []byte, addr *net.UDPAddr) {
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
		s.mu.Lock()
		s.aCount[q.Name.String()]++
		n := s.aCount[q.Name.String()]
		s.mu.Unlock()

		ip := s.public
		if n >= 2 {
			ip = s.private
		}
		if ip4 := ip.To4(); ip4 != nil {
			var a [4]byte
			copy(a[:], ip4)
			resp.Answers = append(resp.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 0},
				Body:   &dnsmessage.AResource{A: a},
			})
		}
	}
	// AAAA (and anything else) -> NOERROR with no records. This harness only
	// drives the IPv4 rebinding scenario; AC-4's IPv6 coverage lives in the
	// unit-level dialer tests added alongside the fix.

	out, err := resp.Pack()
	if err != nil {
		return
	}
	_, _ = s.conn.WriteToUDP(out, addr)
}

// withRebindResolver installs a *net.Resolver that forces the pure-Go
// resolver and redirects every DNS query to dnsAddr, restoring the previous
// net.DefaultResolver at test cleanup. Any in-process code resolving via
// net.DefaultResolver — net.LookupHost, net.DefaultResolver.LookupIPAddr,
// and a bare *net.Dialer's own internal resolution when dialing a hostname
// — is affected identically, with no webfetch code changes required.
func withRebindResolver(t *testing.T, dnsAddr string) {
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

// ─── AC-1 / AC-2: direct-request rebinding ──────────────────────────────────

// TestWebFetch_DNSRebinding_DirectRequest is the WP01 falsification gate.
// A hostname resolves to a public-looking address on the FIRST lookup
// (passing web_fetch's F-002 pre-flight block-list check) and to a
// private/loopback address — the real bind address of a local victim
// server — on every subsequent lookup.
//
// On unfixed webfetch.go: blockListCheck's net.LookupHost call consumes the
// "public" answer and passes. The *http.Client's Transport is nil
// (newHardenedClient never sets one), so http.DefaultTransport dials, doing
// its OWN unvalidated resolution — which consumes the "private" answer and
// connects straight to the victim.
//
// Pre-fix: this test FAILS (the victim is reached, no error). Post-fix: it
// PASSES (blocked, victim never reached). Same file, unmodified, both runs.
func TestWebFetch_DNSRebinding_DirectRequest(t *testing.T) {
	victimReached := false
	victim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		victimReached = true
		w.WriteHeader(http.StatusOK)
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

	dns := startRebindDNSServer(t, net.ParseIP("93.184.216.34"), victimIP)
	withRebindResolver(t, dns.addr())

	tool := webfetch.New(webfetch.Options{})
	args := map[string]any{
		"url": fmt.Sprintf("http://rebind-direct.egress-guards.test:%s/x", victimPort),
	}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call returned a Go error (should always return a JSON result): %v", err)
	}

	var result struct {
		Status  int    `json:"status"`
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if victimReached {
		t.Errorf("DNS-rebinding bypass: the private victim address was reached. result=%+v", result)
	}
	if !result.IsError {
		t.Errorf("expected the DNS-rebinding fetch to be blocked, got a successful result: %+v", result)
	}
}

// ─── AC-3: redirect-path rebinding ──────────────────────────────────────────

// TestWebFetch_DNSRebinding_RedirectTarget proves the redirect path shares
// the direct-request gap and is closed by the same fix, not a parallel one.
//
// The origin is a real local httptest server (necessarily loopback — real
// servers in this test binary have no other address) reached via
// SkipBlockList, exactly as the pre-existing
// TestWebFetch_BlockList_RedirectToPrivate test already does for its own
// origin hop. The origin responds with a redirect to a SEPARATE hostname
// that rebinds exactly like the direct case: public on the first lookup
// (satisfies CheckRedirect's hostname check), private — the victim's real
// address — on the next.
func TestWebFetch_DNSRebinding_RedirectTarget(t *testing.T) {
	victimReached := false
	victim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		victimReached = true
		w.WriteHeader(http.StatusOK)
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

	dns := startRebindDNSServer(t, net.ParseIP("93.184.216.34"), victimIP)
	withRebindResolver(t, dns.addr())

	redirectTarget := fmt.Sprintf("http://rebind-redirect.egress-guards.test:%s/internal", victimPort)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusFound)
	}))
	defer origin.Close()

	// SkipBlockList reaches the loopback origin, exactly like the existing
	// TestWebFetch_BlockList_RedirectToPrivate. The redirect target is a
	// DIFFERENT host and is NOT covered by that bypass — it must be
	// checked and pinned in full.
	tool := webfetch.New(webfetch.Options{SkipBlockList: true})
	args := map[string]any{"url": origin.URL + "/start"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call returned a Go error (should always return a JSON result): %v", err)
	}

	var result struct {
		Status  int    `json:"status"`
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if victimReached {
		t.Errorf("DNS-rebinding bypass via redirect: the private victim address was reached. result=%+v", result)
	}
	if !result.IsError {
		t.Errorf("expected the DNS-rebinding redirect to be blocked, got a successful result: %+v", result)
	}
}
