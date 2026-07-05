package authbroker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

// ─── helpers ──────────────────────────────────────────────────────────────

// fakeBroker is a controllable HTTP server mimicking the host broker endpoint.
type fakeBroker struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	mode    brokerMode // current response behaviour
	callCnt atomic.Int32
}

type brokerMode int

const (
	brokerOK        brokerMode = iota // 200 {access_token, expires_in}
	brokerSignedOut                   // 401 signed_out
	brokerForbidden                   // 403 forbidden
	brokerError503                    // 503 unavailable
)

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	fb := &fakeBroker{t: t, mode: brokerOK}
	fb.srv = httptest.NewServer(http.HandlerFunc(fb.handle))
	t.Cleanup(fb.srv.Close)
	return fb
}

func (fb *fakeBroker) handle(w http.ResponseWriter, r *http.Request) {
	fb.callCnt.Add(1)
	fb.mu.Lock()
	mode := fb.mode
	fb.mu.Unlock()

	if r.URL.Path != "/auth/token" || r.Method != http.MethodPost {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// We accept any bearer token in tests to keep fakes simple.
	switch mode {
	case brokerOK:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "renewed-token-" + r.Header.Get("Authorization"),
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        "openid profile email",
		})
	case brokerSignedOut:
		w.Header().Set("WWW-Authenticate", `Bearer realm="kenaz-broker", error="signed_out"`)
		http.Error(w, "signed out", http.StatusUnauthorized)
	case brokerForbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
	case brokerError503:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}
}

func (fb *fakeBroker) setMode(mode brokerMode) {
	fb.mu.Lock()
	fb.mode = mode
	fb.mu.Unlock()
}

func (fb *fakeBroker) addr() string {
	// strip http:// prefix — broker is addressed without scheme
	return fb.srv.Listener.Addr().String()
}

// controlledTimer is an injectable *time.Timer replacement that never fires
// spontaneously; tests fire it by calling fire().
type controlledTimer struct {
	C    chan time.Time
	once sync.Once
}

func newControlledTimer() *controlledTimer {
	return &controlledTimer{C: make(chan time.Time, 1)}
}

func (ct *controlledTimer) fire() {
	select {
	case ct.C <- time.Now():
	default:
	}
}

// timerFactory produces controlledTimers and records the durations they were
// created with.
type timerFactory struct {
	mu     sync.Mutex
	timers []*controlledTimer
	durs   []time.Duration
}

func (tf *timerFactory) new(d time.Duration) *time.Timer {
	ct := newControlledTimer()
	tf.mu.Lock()
	tf.timers = append(tf.timers, ct)
	tf.durs = append(tf.durs, d)
	tf.mu.Unlock()
	// We must return a real *time.Timer but swap out its channel.
	// Go does not expose a way to construct a timer with a custom channel, so
	// we create a timer that fires very far in the future and replace its C.
	// The renewalLoop reads ct.C; it never reads the underlying timer.C field
	// again after construction.  We achieve this by making the factory return
	// a wrapper: the loop accesses t.C which we have replaced.
	t := time.NewTimer(24 * time.Hour) // will never fire during the test
	t.C = ct.C                          //nolint:govet — intentional field override for tests
	return t
}

func (tf *timerFactory) latest() *controlledTimer {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	if len(tf.timers) == 0 {
		return nil
	}
	return tf.timers[len(tf.timers)-1]
}

func (tf *timerFactory) count() int {
	tf.mu.Lock()
	defer tf.mu.Unlock()
	return len(tf.timers)
}

// waitForState blocks until the session reaches the desired state or the
// deadline passes.
func waitForState(t *testing.T, s *authbroker.Session, want authbroker.State, deadline time.Duration) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for state %v; current=%v", want, s.State())
		case <-s.StateChangedCh():
			if s.State() == want {
				return
			}
		}
	}
}

// ─── ReadConfig ───────────────────────────────────────────────────────────

func TestReadConfig_AllEnvSet(t *testing.T) {
	env := map[string]string{
		authbroker.EnvBrokerAddr:      "192.168.64.1:9501",
		authbroker.EnvBrokerToken:     "secret-broker-token",
		authbroker.EnvSignedIn:        "true",
		authbroker.EnvSeedAccessToken: "seed-access-token",
	}
	cfg := authbroker.ReadConfig(func(k string) string { return env[k] })

	if cfg.BrokerAddr != "192.168.64.1:9501" {
		t.Errorf("BrokerAddr = %q", cfg.BrokerAddr)
	}
	if cfg.BrokerToken != "secret-broker-token" {
		t.Errorf("BrokerToken = %q", cfg.BrokerToken)
	}
	if !cfg.SignedIn {
		t.Error("SignedIn should be true")
	}
	if cfg.SeedAccessToken != "seed-access-token" {
		t.Errorf("SeedAccessToken = %q", cfg.SeedAccessToken)
	}
}

func TestReadConfig_SignedInFalse(t *testing.T) {
	env := map[string]string{
		authbroker.EnvSignedIn: "false",
	}
	cfg := authbroker.ReadConfig(func(k string) string { return env[k] })
	if cfg.SignedIn {
		t.Error("SignedIn should be false")
	}
}

func TestReadConfig_EmptyEnv(t *testing.T) {
	cfg := authbroker.ReadConfig(func(string) string { return "" })
	if cfg.SignedIn {
		t.Error("empty env: SignedIn should be false")
	}
	if cfg.BrokerAddr != "" || cfg.BrokerToken != "" || cfg.SeedAccessToken != "" {
		t.Error("empty env: all string fields should be empty")
	}
}

// ─── Anonymous mode ───────────────────────────────────────────────────────

func TestSession_AnonymousWhenNotSignedIn(t *testing.T) {
	cfg := authbroker.Config{SignedIn: false}
	s := authbroker.NewSession(context.Background(), cfg, nil)
	if s.State() != authbroker.StateAnonymous {
		t.Errorf("expected anonymous, got %v", s.State())
	}
	if s.AccessToken() != "" {
		t.Error("anonymous session must not expose an access token")
	}
}

func TestSession_AnonymousWhenEmptySeedToken(t *testing.T) {
	cfg := authbroker.Config{SignedIn: true, SeedAccessToken: ""}
	s := authbroker.NewSession(context.Background(), cfg, nil)
	if s.State() != authbroker.StateAnonymous {
		t.Errorf("expected anonymous when seed token empty, got %v", s.State())
	}
}

// ─── Expiry-triggered renewal ─────────────────────────────────────────────

func TestSession_ExpiryTriggeredRenewal(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}

	cfg := authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
	)

	// Initial state is signed-in with the seed token.
	if s.State() != authbroker.StateSignedIn {
		t.Fatalf("expected signed_in, got %v", s.State())
	}
	if s.AccessToken() == "" {
		t.Fatal("expected non-empty initial access token")
	}

	// Wait for the renewal goroutine to create its timer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if tf.count() == 0 {
		t.Fatal("renewal goroutine did not create a timer in time")
	}

	// Fire the timer to trigger a renewal.
	tf.latest().fire()

	// Wait for the state-changed notification indicating a successful renewal.
	waitForState(t, s, authbroker.StateSignedIn, 2*time.Second)

	// After renewal the broker should have been called at least once.
	if fb.callCnt.Load() == 0 {
		t.Error("broker was not called")
	}
	// Token should have been updated (renewed token starts with "renewed-token-").
	tok := s.AccessToken()
	if tok == "seed-tok" {
		t.Error("token was not updated after renewal")
	}
	if tok == "" {
		t.Error("token must not be empty after successful renewal")
	}
}

// ─── 401 cascade → signed out ─────────────────────────────────────────────

func TestSession_BrokerSignedOut_ClearsToken(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}

	var ledgerEvents []string
	var ledgerMu sync.Mutex
	ledgerEmit := func(event string) {
		ledgerMu.Lock()
		ledgerEvents = append(ledgerEvents, event)
		ledgerMu.Unlock()
	}

	cfg := authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
		authbroker.WithLedgerEmit(ledgerEmit),
	)

	// Wait for the timer to be registered.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// Switch broker to 401 mode (host signed out).
	fb.setMode(brokerSignedOut)

	// Fire the timer to trigger the renewal attempt.
	tf.latest().fire()

	// Wait for the state to transition to signed_out.
	waitForState(t, s, authbroker.StateSignedOut, 2*time.Second)

	// Token must be cleared — privacy constraint.
	if tok := s.AccessToken(); tok != "" {
		t.Errorf("access token must be cleared on sign-out, got non-empty")
	}

	// Ledger event is emitted asynchronously after the state transition, so
	// poll for it rather than reading once (the immediate read raced the emit
	// and flaked under CI's -race timing).
	ledgerDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(ledgerDeadline) {
		ledgerMu.Lock()
		n := len(ledgerEvents)
		ledgerMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Ledger event must have been emitted.
	ledgerMu.Lock()
	events := ledgerEvents
	ledgerMu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected session.signed_out ledger event, got none")
	}
	if events[0] != "session.signed_out" {
		t.Errorf("expected ledger event 'session.signed_out', got %q", events[0])
	}
}

// ─── 403 / network error → backoff + token retained ─────────────────────

func TestSession_BrokerForbidden_BackoffAndTokenRetained(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}

	// Start in forbidden mode immediately.
	fb.setMode(brokerForbidden)

	cfg := authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
	)

	// Wait for the initial timer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	initialCallCount := fb.callCnt.Load()

	// Fire the timer to trigger a renewal attempt that will get 403.
	tf.latest().fire()

	// Give the goroutine time to process the 403 response.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fb.callCnt.Load() <= initialCallCount {
		time.Sleep(5 * time.Millisecond)
	}

	// Broker must have been called at least once.
	if fb.callCnt.Load() <= initialCallCount {
		t.Error("broker was not called after timer fire")
	}

	// Session must remain signed_in after 403.
	if s.State() != authbroker.StateSignedIn {
		t.Errorf("expected signed_in after 403, got %v", s.State())
	}
	// Token must NOT be cleared.
	if s.AccessToken() != "seed-tok" {
		t.Errorf("token must be retained after 403, got %q", s.AccessToken())
	}

	// Verify recovery: switch broker back to OK and fire the timer again.
	// The loop should retry and successfully renew.
	fb.setMode(brokerOK)
	tf.latest().fire()
	waitForState(t, s, authbroker.StateSignedIn, 2*time.Second)
	// Token should now be updated from the broker.
	if s.AccessToken() == "seed-tok" {
		t.Error("expected token to be updated after recovery from 403")
	}
}

func TestSession_NetworkError_BackoffAndTokenRetained(t *testing.T) {
	tf := &timerFactory{}

	// Use an address that is not listening to simulate a network error.
	cfg := authbroker.Config{
		BrokerAddr:      "127.0.0.1:19999", // nothing listening here
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a short HTTP timeout to make the network error fast.
	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(&http.Client{Timeout: 100 * time.Millisecond}),
		authbroker.WithTimerFactory(tf.new),
	)

	// Wait for the initial timer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// Fire the timer — HTTP will fail with connection refused.
	tf.latest().fire()

	// Give the goroutine time to handle the error (connection refused is fast).
	// We verify the session remained signed_in by polling briefly.
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == authbroker.StateSignedOut {
			t.Error("session must not transition to signed_out on network error")
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Session must remain signed_in.
	if s.State() != authbroker.StateSignedIn {
		t.Errorf("expected signed_in after network error, got %v", s.State())
	}
	// Token must NOT be cleared.
	if s.AccessToken() != "seed-tok" {
		t.Errorf("token must be retained after network error, got %q", s.AccessToken())
	}
}

// ─── NotifyOn401 ─────────────────────────────────────────────────────────

func TestSession_NotifyOn401_TriggersRenewal(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}

	cfg := authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
	)

	// Wait for the renewal goroutine to start (timer created).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// Signal 401 from outside (as if an LLM API call returned 401).
	s.NotifyOn401()

	// Wait for a renewal to complete (state-changed notification).
	waitForState(t, s, authbroker.StateSignedIn, 2*time.Second)

	if fb.callCnt.Load() == 0 {
		t.Error("broker was not called after NotifyOn401")
	}
}

func TestSession_NotifyOn401_NopInAnonymousMode(t *testing.T) {
	// Should not panic or hang.
	cfg := authbroker.Config{SignedIn: false}
	s := authbroker.NewSession(context.Background(), cfg, nil)
	s.NotifyOn401() // must be a no-op
	if s.State() != authbroker.StateAnonymous {
		t.Errorf("expected anonymous after NotifyOn401 no-op, got %v", s.State())
	}
}

// ─── Context cancellation ─────────────────────────────────────────────────

func TestSession_ContextCancel_StopsRenewalLoop(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}

	cfg := authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := authbroker.NewSession(ctx, cfg, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
	)
	_ = s

	// Wait for goroutine to start.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// Cancel should cause the goroutine to exit cleanly.
	// We verify this indirectly by ensuring a subsequent renewal attempt
	// (if the goroutine were still running) does NOT happen.
	cancel()

	// A brief wait; if the loop hasn't stopped it would keep creating timers
	// after the context cancel (which the test timer factory records).
	time.Sleep(50 * time.Millisecond)
	countAfterCancel := tf.count()

	// Fire the existing timer after cancel — the loop should not process it.
	if tf.latest() != nil {
		tf.latest().fire()
	}
	time.Sleep(50 * time.Millisecond)

	if tf.count() != countAfterCancel {
		t.Errorf("renewal goroutine appears to still be running after context cancel (new timers created)")
	}
}

// ─── State String ─────────────────────────────────────────────────────────

func TestState_String(t *testing.T) {
	tests := []struct {
		state authbroker.State
		want  string
	}{
		{authbroker.StateAnonymous, "anonymous"},
		{authbroker.StateSignedIn, "signed_in"},
		{authbroker.StateSignedOut, "signed_out"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}
