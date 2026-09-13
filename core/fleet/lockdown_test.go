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

// fakeBrokerSink is a race-safe test double for BrokerSink.
type fakeBrokerSink struct {
	mu     sync.Mutex
	events []LockdownChangedPayload
}

func (f *fakeBrokerSink) Emit(topic string, payload any) {
	if topic != TopicFleetLockdownChanged {
		return
	}
	p, ok := payload.(LockdownChangedPayload)
	if !ok {
		return
	}
	f.mu.Lock()
	f.events = append(f.events, p)
	f.mu.Unlock()
}

func (f *fakeBrokerSink) snapshot() []LockdownChangedPayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]LockdownChangedPayload, len(f.events))
	copy(out, f.events)
	return out
}

// setupLockdownTest sets up tokens, resets the flag, and returns a *Client
// pointed at baseURL. All tests in this file must be sequential (no t.Parallel)
// because stubTokens uses a process-global keyring.
func setupLockdownTest(t *testing.T, baseURL string) *Client {
	t.Helper()
	// Reset the global flag.
	lockdownActive.Store(false)
	t.Cleanup(func() { lockdownActive.Store(false) })

	// Stub tokens so client.do() can find credentials.
	stubTokens(t, TokenSet{
		AccessToken:  "at-lockdown-test",
		RefreshToken: "rt-lockdown-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	SeedFleetConfigForTesting(baseURL, FleetConfig{
		Issuer:     baseURL,
		ClientID:   "test",
		APIBaseURL: baseURL,
	})
	return &Client{
		profile: EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  baseURL,
			NativeClientID: "test",
			FleetBaseURL:   baseURL,
			OIDCScopes:     DefaultOIDCScopes,
		},
		httpClient:  &http.Client{},
		httpTimeout: 5 * time.Second,
	}
}

// newTestPoller creates a CapabilityPoller pre-seeded with a snapshot that
// has CapEmergencyLockdown set to the given value.
func newTestPoller(enabled bool) *CapabilityPoller {
	p := &CapabilityPoller{
		backoff: &backoffState{},
		done:    make(chan struct{}),
	}
	enabledMap := map[Capability]bool{
		CapEmergencyLockdown: enabled,
	}
	p.current = Capabilities{
		Tier:      "team",
		Enabled:   enabledMap,
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	return p
}

// TestWatcher — sequential group to avoid keyring races.
// Do NOT add t.Parallel() inside these subtests.

func TestWatcher_LockdownFlagFlipsOnSignal(t *testing.T) {
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requestCount.Add(1))
		switch n {
		case 1:
			// First poll: return lockdown=true.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(LockdownStatus{Lockdown: true, Reason: "drill"})
		case 2:
			// Second poll: return lockdown=false (un-lock).
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(LockdownStatus{Lockdown: false})
		default:
			// Block indefinitely to avoid frantic retries.
			<-r.Context().Done()
		}
	}))
	t.Cleanup(srv.Close)

	client := setupLockdownTest(t, srv.URL)
	sink := &fakeBrokerSink{}
	w := NewWatcher(client, newTestPoller(true), sink)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.Start(ctx)

	// Wait until we have at least 2 events.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.snapshot()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	w.Stop()

	evts := sink.snapshot()
	if len(evts) < 2 {
		t.Fatalf("expected at least 2 broker events; got %d", len(evts))
	}
	if !evts[0].Active {
		t.Errorf("event[0]: expected Active=true, got false")
	}
	if evts[0].Reason != "drill" {
		t.Errorf("event[0]: expected Reason=%q, got %q", "drill", evts[0].Reason)
	}
	if evts[1].Active {
		t.Errorf("event[1]: expected Active=false, got true")
	}
	if LockdownActive() {
		t.Error("expected LockdownActive()=false after un-lock event")
	}
}

func TestWatcher_NoEmitOnNoDelta(t *testing.T) {
	// Pre-set to true so the first response (also true) produces no delta.
	lockdownActive.Store(true)
	t.Cleanup(func() { lockdownActive.Store(false) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond with the same state as the current flag (no change).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LockdownStatus{Lockdown: true, Reason: "no-change"})
	}))
	t.Cleanup(srv.Close)

	// Need tokens set; use stubTokens directly (setupLockdownTest resets flag to false).
	stubTokens(t, TokenSet{
		AccessToken:  "at-no-delta",
		RefreshToken: "rt-no-delta",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	SeedFleetConfigForTesting(srv.URL, FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "test",
		APIBaseURL: srv.URL,
	})
	client := &Client{
		profile: EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  srv.URL,
			NativeClientID: "test",
			FleetBaseURL:   srv.URL,
			OIDCScopes:     DefaultOIDCScopes,
		},
		httpClient:  &http.Client{},
		httpTimeout: 2 * time.Second,
	}

	sink := &fakeBrokerSink{}
	w := NewWatcher(client, newTestPoller(true), sink)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w.Start(ctx)
	// Let it run one poll cycle.
	time.Sleep(300 * time.Millisecond)
	w.Stop()

	evts := sink.snapshot()
	if len(evts) != 0 {
		t.Errorf("expected 0 broker events (no delta), got %d", len(evts))
	}
	if !LockdownActive() {
		t.Error("expected LockdownActive()=true after no-delta response")
	}
}

func TestWatcher_BackoffOnServerError(t *testing.T) {
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requestCount.Add(1))
		if n == 1 {
			// First request fails with 503.
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Subsequent requests succeed with lockdown=false.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LockdownStatus{Lockdown: false})
	}))
	t.Cleanup(srv.Close)

	// Shorten backoff steps for test speed.
	origSteps := lockdownBackoffSteps
	lockdownBackoffSteps = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
	t.Cleanup(func() { lockdownBackoffSteps = origSteps })

	client := setupLockdownTest(t, srv.URL)
	// Use very short timeout so the internal 5xx retry budget exhausts quickly.
	client.httpTimeout = 20 * time.Millisecond

	w := NewWatcher(client, newTestPoller(true), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if int(requestCount.Load()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	w.Stop()

	if int(requestCount.Load()) < 2 {
		t.Errorf("expected at least 2 requests (1 error + 1 success); got %d", requestCount.Load())
	}
}

func TestWatcher_StopIsClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client disconnects (Stop() cancels ctx).
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	client := setupLockdownTest(t, srv.URL)
	w := NewWatcher(client, newTestPoller(true), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.Start(ctx)

	// Let the goroutine enter the long-poll.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — Stop returned cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 s")
	}
}

func TestWatcher_CapabilityGate_NoOp(t *testing.T) {
	callCount := make(chan struct{}, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := setupLockdownTest(t, srv.URL)

	// Poller with CapEmergencyLockdown=false.
	w := NewWatcher(client, newTestPoller(false), nil)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	// Give it a moment to (not) start.
	time.Sleep(50 * time.Millisecond)
	cancel()
	w.Stop()

	select {
	case <-callCount:
		t.Error("expected no HTTP call when capability is disabled")
	default:
		// OK
	}
}

func TestBootstrapLockdownStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/lockdown/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LockdownStatus{Lockdown: true, Reason: "bootstrap-test"})
	}))
	t.Cleanup(srv.Close)

	client := setupLockdownTest(t, srv.URL)
	BootstrapLockdownStatus(context.Background(), client)

	if !LockdownActive() {
		t.Error("expected LockdownActive()=true after bootstrap")
	}

	// AC-011 (fleet-enforcement-truth-01PMZ505 WP08): the reason must
	// survive the BOOT-INTO-LOCKED-STATE path with no broker event ever
	// replayed — this is the whole defect. Before WP08, bootstrap parsed
	// the reason off the wire and dropped it (only logged it), so
	// LockdownReason() read "" here even though the server sent one.
	if got := LockdownReason(); got != "bootstrap-test" {
		t.Errorf("LockdownReason() = %q, want %q (reason must survive the bootstrap "+
			"path with no broker event replayed — this is the boot-into-locked-state "+
			"case LockdownBanner.vue's mount hook exists to handle)", got, "bootstrap-test")
	}
}

// TestLockdownReason_ClearsWhenLockdownClears is the negative half of
// AC-011: LockdownStatusView.Reason is documented as "Empty when Active is
// false" (fleet.go's LockdownStatusView doc comment) — a stale reason
// surviving past the lockdown that produced it would misinform an operator
// reading the banner after the fact.
func TestLockdownReason_ClearsWhenLockdownClears(t *testing.T) {
	lockdownActive.Store(false)
	t.Cleanup(func() { lockdownActive.Store(false); lockdownReason.Store("") })

	setLockdownState(true, "planned maintenance")
	if got := LockdownReason(); got != "planned maintenance" {
		t.Fatalf("LockdownReason() = %q, want %q", got, "planned maintenance")
	}

	setLockdownState(false, "")
	if got := LockdownReason(); got != "" {
		t.Errorf("LockdownReason() = %q, want \"\" once lockdown clears "+
			"(a stale reason after clearing would misinform an operator)", got)
	}
	if LockdownActive() {
		t.Error("expected LockdownActive()=false after clearing")
	}
}

// TestSetLockdownState_RaceSafe drives concurrent writers and a concurrent
// reader through setLockdownState/LockdownReason to prove the storage is
// race-safe under -race, per spec §5.6's explicit requirement ("the watcher
// goroutine writes and FleetLockdownStatus reads, so it must be race-safe
// under -race").
func TestSetLockdownState_RaceSafe(t *testing.T) {
	lockdownActive.Store(false)
	t.Cleanup(func() { lockdownActive.Store(false); lockdownReason.Store("") })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			setLockdownState(i%2 == 0, "reason")
		}
	}()
	for i := 0; i < 200; i++ {
		_ = LockdownReason()
		_ = LockdownActive()
	}
	<-done
}

func TestBootstrapLockdownStatus_404IsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate fleet server that doesn't have the lockdown endpoint yet.
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client := setupLockdownTest(t, srv.URL)
	BootstrapLockdownStatus(context.Background(), client)

	if LockdownActive() {
		t.Error("expected LockdownActive()=false on 404 (endpoint not deployed)")
	}
}
