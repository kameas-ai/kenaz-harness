package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient constructs a Client that talks to the given httptest.Server.
// It bypasses the kill-switch by setting the profile directly.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	// Seed the FleetConfig cache so /api/v1/* calls don't try to fetch
	// /config.json from the test server.
	SeedFleetConfigForTesting(srv.URL, FleetConfig{
		Issuer:     "https://example.com",
		ClientID:   "client-id",
		APIBaseURL: srv.URL,
	})
	c := &Client{
		profile: EnvProfile{
			Name:           "test",
			FleetBaseURL:   srv.URL,
			ZitadelIssuer:  "https://example.com",
			NativeClientID: "client-id",
			APIAudience:    "aud",
		},
		httpClient:  srv.Client(),
		httpTimeout: 5 * time.Second,
	}
	return c
}

// fakeCapabilityServer returns an httptest.Server that responds to
// GET /api/v1/me/capabilities with the given Capabilities.
// The statusCode parameter overrides the HTTP status (0 = use 200).
func fakeCapabilityServer(t *testing.T, caps Capabilities, statusCode int) *httptest.Server {
	t.Helper()
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/capabilities" {
			http.NotFound(w, r)
			return
		}
		// Also need a token in keychain-free tests — auth.go uses LoadTokens.
		// We bypass the normal auth flow by having the server not check the
		// Authorization header; the client's do() calls LoadTokens first.
		// Since we don't have tokens in test, we bypass do() by hitting a
		// separate endpoint path. However the poller calls p.client.Get which
		// calls do() which calls LoadTokens. To avoid this chicken-and-egg we
		// mock at a lower level: install a fake token in the keychain-less
		// in-memory token store. In tests we use a test-friendly approach:
		// override the httptest server client's Transport to intercept before
		// auth headers matter.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if statusCode != http.StatusOK {
			return
		}
		wire := capabilitiesWireResponse{
			Tier:         caps.Tier,
			Capabilities: make(map[string]bool, len(caps.Enabled)),
			FetchedAt:    caps.FetchedAt,
		}
		for k, v := range caps.Enabled {
			wire.Capabilities[string(k)] = v
		}
		_ = json.NewEncoder(w).Encode(wire)
	}))
	return srv
}

// TestCapabilityPoller_Current_DefaultDeny verifies the poller starts
// in default-deny state before any fetch.
func TestCapabilityPoller_Current_DefaultDeny(t *testing.T) {
	dir := t.TempDir()
	p := NewCapabilityPoller(nil, dir)
	cur := p.Current()
	if cur.Source != "default-deny" {
		t.Errorf("Source = %q, want 'default-deny'", cur.Source)
	}
	if cur.Has(CapHostedInference) {
		t.Error("default-deny Has(CapHostedInference) = true, want false")
	}
}

// TestBackoffState_Progression verifies next() returns 5/15/60 then stays at 60.
func TestBackoffState_Progression(t *testing.T) {
	b := &backoffState{}
	want := []time.Duration{5 * time.Minute, 15 * time.Minute, 60 * time.Minute, 60 * time.Minute}
	for i, w := range want {
		got := b.next()
		if got != w {
			t.Errorf("step %d: next() = %v, want %v", i, got, w)
		}
	}
}

// TestBackoffState_ResetOnSuccess verifies reset() starts over from step 0.
func TestBackoffState_ResetOnSuccess(t *testing.T) {
	b := &backoffState{}
	_ = b.next()
	_ = b.next()
	b.reset()
	if got := b.next(); got != 5*time.Minute {
		t.Errorf("after reset, next() = %v, want 5m", got)
	}
}

// fakeClientWithTransport builds a *Client with a mock transport that
// bypasses the LoadTokens auth path. It overrides the httpClient directly.
// This lets us test the poller's Refresh without keychain interactions.
type bypassAuthTransport struct {
	inner   http.RoundTripper
	authOK  bool
}

func (t *bypassAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Strip the Authorization header check on the server side by always
	// succeeding. The Client.do() calls LoadTokens; we pre-install a
	// fake token to satisfy it.
	return t.inner.RoundTrip(req)
}

// installFakeTokens writes a minimal token set to the in-process keychain so
// Client.do() can load them. Because tests don't have a real keychain, we use
// SaveTokens which writes to the OS keychain mock in tests.
// In the fleet package, token storage may hit the OS keychain which isn't
// available in CI. We skip tests that require it if SaveTokens fails.
func tryInstallFakeTokens(t *testing.T) bool {
	t.Helper()
	ts := TokenSet{
		AccessToken:  "fake-access",
		RefreshToken: "fake-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := SaveTokens(ts); err != nil {
		t.Logf("skip: SaveTokens unavailable (no OS keychain in this env): %v", err)
		return false
	}
	t.Cleanup(func() { _ = ClearTokens() })
	return true
}

// TestCapabilityPoller_Refresh_404_GracefulDegrade verifies that a 404 response
// results in default-deny capabilities and no error.
func TestCapabilityPoller_Refresh_404_GracefulDegrade(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	srv := fakeCapabilityServer(t, DefaultDenyCapabilities(), http.StatusNotFound)
	defer srv.Close()

	client := newTestClient(t, srv)
	dir := t.TempDir()
	p := NewCapabilityPoller(client, dir)

	caps, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh 404 = error %v, want nil (graceful degrade)", err)
	}
	if caps.Source != "default-deny" {
		t.Errorf("Source = %q, want 'default-deny'", caps.Source)
	}
}

// TestCapabilityPoller_Refresh_Success verifies a successful 200 response.
func TestCapabilityPoller_Refresh_Success(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	expected := Capabilities{
		Tier:      "pro",
		Enabled:   map[Capability]bool{CapHostedInference: true, CapLauncherUpdates: false},
		FetchedAt: time.Now().UTC().Truncate(time.Second),
		Source:    "fleet",
	}
	srv := fakeCapabilityServer(t, expected, 0)
	defer srv.Close()

	client := newTestClient(t, srv)
	dir := t.TempDir()
	p := NewCapabilityPoller(client, dir)

	caps, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh success = error %v", err)
	}
	if caps.Source != "fleet" {
		t.Errorf("Source = %q, want 'fleet'", caps.Source)
	}
	if caps.Tier != "pro" {
		t.Errorf("Tier = %q, want 'pro'", caps.Tier)
	}
	if !caps.Enabled[CapHostedInference] {
		t.Errorf("CapHostedInference: got false, want true")
	}
}

// TestCapabilityPoller_Refresh_UpdatesCurrent verifies that after a successful
// Refresh(), Current() immediately returns the newly fetched capabilities.
// This is the regression test for the bug where Refresh() returned the correct
// value but never called setCurrent, so Current() lagged behind.
func TestCapabilityPoller_Refresh_UpdatesCurrent(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	expected := Capabilities{
		Tier:      "team",
		Enabled:   map[Capability]bool{CapHostedInference: true},
		FetchedAt: time.Now().UTC().Truncate(time.Second),
		Source:    "fleet",
	}
	srv := fakeCapabilityServer(t, expected, 0)
	defer srv.Close()

	client := newTestClient(t, srv)
	dir := t.TempDir()
	p := NewCapabilityPoller(client, dir)

	// Before Refresh, Current() must be default-deny.
	if p.Current().Source != "default-deny" {
		t.Fatal("pre-condition: Current() should be default-deny before Refresh")
	}

	_, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh = error %v", err)
	}

	// Current() must now reflect the freshly fetched capabilities.
	cur := p.Current()
	if cur.Source != "fleet" {
		t.Errorf("Current().Source = %q, want 'fleet' (Refresh must call setCurrent)", cur.Source)
	}
	if cur.Tier != "team" {
		t.Errorf("Current().Tier = %q, want 'team'", cur.Tier)
	}
	if !cur.Has(CapHostedInference) {
		t.Error("Current().Has(CapHostedInference) = false, want true")
	}
}

// TestCapabilityPoller_SingleFlight_Collapse verifies multiple concurrent
// Refresh calls collapse to one HTTP request.
func TestCapabilityPoller_SingleFlight_Collapse(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	var callCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/capabilities" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt64(&callCount, 1)
		// Add a small delay so concurrent calls overlap.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		wire := capabilitiesWireResponse{
			Tier:         "team",
			Capabilities: map[string]bool{string(CapHostedInference): true},
			FetchedAt:    time.Now(),
		}
		_ = json.NewEncoder(w).Encode(wire)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	dir := t.TempDir()
	p := NewCapabilityPoller(client, dir)

	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_, _ = p.Refresh(context.Background())
		}()
	}
	wg.Wait()

	got := atomic.LoadInt64(&callCount)
	// singleflight should collapse all concurrent calls to 1 (or a very small
	// number if they arrive in different singleflight windows).
	if got > 3 {
		t.Errorf("server received %d requests, want ≤3 (singleflight should collapse)", got)
	}
}

// TestCapabilityPoller_Stop_Race verifies Stop() does not race with the
// goroutine. Run with -race.
func TestCapabilityPoller_Stop_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine stop test in -short mode")
	}
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	srv := fakeCapabilityServer(t, Capabilities{
		Tier:      "pro",
		Enabled:   map[Capability]bool{CapHostedInference: true},
		FetchedAt: time.Now(),
	}, 0)
	defer srv.Close()

	client := newTestClient(t, srv)
	dir := t.TempDir()
	p := NewCapabilityPoller(client, dir)
	// Use a short interval for the test.
	p.interval = 10 * time.Millisecond

	ctx := context.Background()
	p.Start(ctx)

	// Let it run for a bit, concurrently access Current().
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = p.Current()
				time.Sleep(time.Millisecond)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	p.Stop()
	wg.Wait()
}

// TestCapabilityPoller_NopClient returns ErrFleetDisabled and default-deny.
func TestCapabilityPoller_NopClient(t *testing.T) {
	nop := nopClientInstance()
	dir := t.TempDir()
	p := NewCapabilityPoller(nop, dir)

	caps, err := p.Refresh(context.Background())
	if err == nil {
		t.Error("Refresh(nop) = nil error, want ErrFleetDisabled")
	}
	if caps.Source != "default-deny" {
		t.Errorf("Source = %q, want 'default-deny'", caps.Source)
	}
}

// TestCapabilityPoller_LoadsCacheOnStart verifies that Start pre-loads the
// disk cache into the in-memory snapshot before the first network fetch.
func TestCapabilityPoller_LoadsCacheOnStart(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine start not tested in -short")
	}
	dir := t.TempDir()

	// Pre-populate cache with a fresh snapshot (recent enough to not trigger immediate refetch).
	cached := Capabilities{
		Tier:      "team",
		Enabled:   map[Capability]bool{CapHostedInference: true},
		FetchedAt: time.Now(), // fresh
		Source:    "cache",
	}
	if err := SaveCapabilities(dir, cached); err != nil {
		t.Fatal(err)
	}

	// Use a nil-ish nop client so we don't need a real server.
	nop := nopClientInstance()
	p := NewCapabilityPoller(nop, dir)

	// Before Start, the in-memory state is default-deny.
	if p.Current().Source != "default-deny" {
		t.Error("initial state should be default-deny")
	}

	// Start triggers cache pre-load even before any network fetch.
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	// Give the goroutine a moment to execute its cache load step.
	time.Sleep(10 * time.Millisecond)

	cur := p.Current()
	if cur.Source != "cache" {
		t.Errorf("after Start, Source = %q, want 'cache'", cur.Source)
	}
	if cur.Tier != "team" {
		t.Errorf("after Start, Tier = %q, want 'team'", cur.Tier)
	}

	cancel()
	p.Stop()
}
