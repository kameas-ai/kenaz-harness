package fleet

// session_truth_test.go — fleet-session-truth-01DOGF0A WP06 (fleet half).
//
// Pins:
//   P-7   append 404 ×N → bounded attempts, one WARN, consecutive failures
//         + dropped count visible on the lane board.
//   P-8a  DefaultOIDCScopes requests the Zitadel resource-owner scope.
//   B3b   Client.do no longer cancels the per-call context before the
//         caller reads the body (the intermittent "context canceled").
//   B3b   unit-poll failures WARN / degrade only at the threshold.
//
// Tokens come from the external token source — no keychain, real or mock.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// logCapture records log records; written from whatever goroutine logs,
// read by the test body (mutex + snapshot).
type logCapture struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
	return nil
}
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) count(level slog.Level, msg string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.recs {
		if r.Level == level && r.Message == msg {
			n++
		}
	}
	return n
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	prev := logging.Handler()
	c := &logCapture{}
	logging.Replace(c)
	t.Cleanup(func() { logging.Replace(prev) })
	return c
}

func withExternalToken(t *testing.T, tok string) {
	t.Helper()
	SetExternalTokenSource(func() string { return tok })
	t.Cleanup(func() { SetExternalTokenSource(nil) })
}

// ── P-8a ─────────────────────────────────────────────────────────────────────

func TestDefaultOIDCScopes_RequestResourceOwnerClaim(t *testing.T) {
	found := false
	for _, s := range DefaultOIDCScopes {
		if s == "urn:zitadel:iam:user:resourceowner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefaultOIDCScopes = %v lacks urn:zitadel:iam:user:resourceowner — Zitadel then omits the "+
			"resource-owner claim and OTLP export deactivates on every reconcile (dogfood B3a)", DefaultOIDCScopes)
	}
}

// ── B3b root cause: body read after do() returns ──────────────────────────────

func TestClientDo_BodyReadAfterReturn_IsNotCancelled(t *testing.T) {
	withExternalToken(t, "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes":[`))
		w.(http.Flusher).Flush()
		// The rest of the body arrives after Do has returned to the caller.
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`]}`))
	}))
	defer srv.Close()
	c := makeTestClient(t, srv.URL)

	resp, err := c.Get(context.Background(), "/api/v1/context/pull")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the body after do() returned failed: %v (the per-call context was cancelled under the caller — B3b)", err)
	}
	if string(body) != `{"nodes":[]}` {
		t.Fatalf("body = %q", body)
	}
}

// ── P-7: append breaker ──────────────────────────────────────────────────────

func TestAppendBreaker_404_BoundedAttempts_OneWarn_Visible(t *testing.T) {
	logs := captureLogs(t)
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	var posts atomic.Int32
	post := func(context.Context) error {
		posts.Add(1)
		return &AppendStatusError{Status: http.StatusNotFound}
	}
	const n = 20
	for i := 0; i < n; i++ {
		_ = b.Do(context.Background(), "session-16dc912d", post)
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("a 404 (missing remote context) was re-posted %d times over %d messages, want 1 — the circuit must open", got, n)
	}
	if w := logs.count(slog.LevelWarn, "rpc.context_sync.append_event_failed"); w != 1 {
		t.Fatalf("WARN logged %d times, want once (surfaced, not logged N times)", w)
	}
	lane := lanes.Snapshot(LaneContextSync)
	if lane.Status != LaneDegraded || lane.Reason != "remote_context_missing" {
		t.Fatalf("lane = %+v, want degraded/remote_context_missing", lane)
	}
	if len(lane.Sessions) != 1 {
		t.Fatalf("lane sessions = %d, want exactly one degraded badge", len(lane.Sessions))
	}
	s := lane.Sessions[0]
	if !s.Open || s.ConsecutiveFailures != 1 || s.Dropped != n-1 || s.SessionID != "session-16dc912d" {
		t.Fatalf("session state = %+v, want open, consecutive=1, dropped=%d", s, n-1)
	}

	// Re-enabling sync (or a new sign-in) is the explicit retry.
	b.Reset("session-16dc912d")
	if lane := lanes.Snapshot(LaneContextSync); lane.Status != LaneOK || len(lane.Sessions) != 0 {
		t.Fatalf("after reset lane = %+v, want ok with no sessions", lane)
	}
}

func TestAppendBreaker_TransientFailures_BackOffThenOpen_ThenHalfOpenRecovers(t *testing.T) {
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	now := time.Unix(1_000_000, 0)
	b.now = func() time.Time { return now }
	var posts int
	failing := true
	post := func(context.Context) error {
		posts++
		if failing {
			return &AppendStatusError{Status: 503}
		}
		return nil
	}

	_ = b.Do(context.Background(), "s", post) // attempt 1 → backoff 30s
	_ = b.Do(context.Background(), "s", post) // inside backoff → held back
	if posts != 1 {
		t.Fatalf("posted %d times inside the backoff window, want 1", posts)
	}
	for posts < appendMaxConsecutiveFailures {
		now = now.Add(appendBackoffMax + time.Second)
		_ = b.Do(context.Background(), "s", post)
	}
	if s := lanes.Snapshot(LaneContextSync).Sessions[0]; !s.Open || s.Reason != "server_error" {
		t.Fatalf("session = %+v, want open/server_error after %d failures", s, appendMaxConsecutiveFailures)
	}
	// Open: nothing is posted before the half-open window…
	now = now.Add(appendBackoffMax / 2)
	_ = b.Do(context.Background(), "s", post)
	if posts != appendMaxConsecutiveFailures {
		t.Fatalf("an open circuit posted before its half-open window (%d posts)", posts)
	}
	// …then exactly one probe; fleet is back, so the circuit closes (F4).
	failing = false
	now = now.Add(appendBackoffMax)
	_ = b.Do(context.Background(), "s", post)
	if posts != appendMaxConsecutiveFailures+1 {
		t.Fatalf("half-open probe not attempted (%d posts)", posts)
	}
	if lane := lanes.Snapshot(LaneContextSync); lane.Status != LaneOK || len(lane.Sessions) != 0 {
		t.Fatalf("after a successful probe lane = %+v, want ok — a transient-open circuit must self-heal", lane)
	}
}

// F4: a PERMANENT open (404) stays latched — no probe ever — until Reset.
func TestAppendBreaker_PermanentOpen_NeverProbes(t *testing.T) {
	b := NewAppendBreaker(NewSyncLanes())
	now := time.Unix(1_000_000, 0)
	b.now = func() time.Time { return now }
	posts := 0
	post := func(context.Context) error { posts++; return &AppendStatusError{Status: 404} }
	_ = b.Do(context.Background(), "s", post)
	for i := 0; i < 5; i++ {
		now = now.Add(24 * time.Hour)
		_ = b.Do(context.Background(), "s", post)
	}
	if posts != 1 {
		t.Fatalf("a 404-latched circuit probed (%d posts)", posts)
	}
}

// F3: the caller cancelling (a user-stopped turn) is not a fleet failure.
func TestAppendBreaker_CallerCancel_IsNotAFailure(t *testing.T) {
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	posts := 0
	for i := 0; i < appendMaxConsecutiveFailures+2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = b.Do(ctx, "s", func(ctx context.Context) error { posts++; return ctx.Err() })
	}
	if posts != appendMaxConsecutiveFailures+2 {
		t.Fatalf("cancelled appends were held back (%d of %d attempted) — cancellation added backoff", posts, appendMaxConsecutiveFailures+2)
	}
	if lane := lanes.Snapshot(LaneContextSync); lane.Status == LaneDegraded || len(lane.Sessions) != 0 {
		t.Fatalf("caller cancellation counted as a sync failure: %+v", lane)
	}
}

func TestAppendBreaker_SuccessClears_DisabledIsNotAFailure(t *testing.T) {
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	_ = b.Do(context.Background(), "s", func(context.Context) error { return ErrFleetDisabled })
	if lanes.Snapshot(LaneContextSync).Status != LaneUnknown {
		t.Fatalf("fleet-disabled counted as a sync failure")
	}
	_ = b.Do(context.Background(), "s", func(context.Context) error { return errors.New("dial tcp: i/o timeout") })
	b.now = func() time.Time { return time.Now().Add(time.Hour) }
	_ = b.Do(context.Background(), "s", func(context.Context) error { return nil })
	if lane := lanes.Snapshot(LaneContextSync); lane.Status != LaneOK || len(lane.Sessions) != 0 {
		t.Fatalf("lane = %+v, want ok after a success", lane)
	}
}

// The real append path returns the typed status error the breaker keys on.
//
// The 404 here carries a JSON {code} envelope: an application answer. A
// PLAIN (Go-mux "404 page not found") 404 is a missing route — which is
// what kenaz-fleet actually returns for /api/v1/context/append (verified
// 2026-10-05) — and latches the feature unsupported instead
// (unsupported_endpoint_test.go). The original dogfood F7 "remote context
// missing" reading of that plain 404 was a misdiagnosis.
func TestEventStreamAppend_404_IsTypedStatusError(t *testing.T) {
	withExternalToken(t, "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/context/append") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"no such stream"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	es, err := NewEventStream(makeTestClient(t, srv.URL), "stream-1", make([]byte, 32))
	if err != nil {
		t.Fatalf("NewEventStream: %v", err)
	}
	err = es.Append(context.Background(), []Event{{Payload: []byte("x")}})
	var se *AppendStatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Fatalf("Append err = %v, want *AppendStatusError{404}", err)
	}
	if reason, permanent := classifyAppendError(err); reason != "remote_context_missing" || !permanent {
		t.Fatalf("classify = (%q,%v), want remote_context_missing/permanent", reason, permanent)
	}
}

// ── B3b: unit poll threshold ─────────────────────────────────────────────────

func TestUnitPollReport_WarnsAndDegradesOnlyAtThreshold(t *testing.T) {
	logs := captureLogs(t)
	lanes := NewSyncLanes()
	s := &UnitSyncer{}
	s.SetLanes(lanes)
	err := context.Canceled
	for i := 1; i < unitPollWarnThreshold; i++ {
		s.reportPoll(err, i, time.Now())
	}
	if w := logs.count(slog.LevelWarn, "fleet.unit.poll.pull_failed"); w != 0 {
		t.Fatalf("WARN logged below the threshold (%d)", w)
	}
	if st := lanes.Snapshot(LaneUnitPoll).Status; st != LaneUnknown {
		t.Fatalf("lane = %q below the threshold, want unknown (transient)", st)
	}
	s.reportPoll(err, unitPollWarnThreshold, time.Now())
	if w := logs.count(slog.LevelWarn, "fleet.unit.poll.pull_failed"); w != 1 {
		t.Fatalf("WARN count at threshold = %d, want 1", w)
	}
	if lane := lanes.Snapshot(LaneUnitPoll); lane.Status != LaneDegraded || lane.ConsecutiveFailures != unitPollWarnThreshold || lane.Reason != "network" {
		t.Fatalf("lane = %+v, want degraded/network with the consecutive count", lane)
	}
	// Further failures past the threshold do not WARN again (review F8).
	s.reportPoll(err, unitPollWarnThreshold+1, time.Now())
	s.reportPoll(err, unitPollWarnThreshold+2, time.Now())
	if w := logs.count(slog.LevelWarn, "fleet.unit.poll.pull_failed"); w != 1 {
		t.Fatalf("WARN count after further failures = %d, want still 1 (once per crossing)", w)
	}
	if c := lanes.Snapshot(LaneUnitPoll).ConsecutiveFailures; c != unitPollWarnThreshold+2 {
		t.Fatalf("lane count = %d, want the running count", c)
	}
	// A successful PullDown is what resets the counter — and the lane.
	s.reportPoll(nil, 0, time.Now())
	if st := lanes.Snapshot(LaneUnitPoll).Status; st != LaneOK {
		t.Fatalf("lane after success = %q, want ok", st)
	}
}

// ── P-5 (flow half): DeviceCodeFlow honours cancellation ─────────────────────

func TestDeviceCodeFlow_Cancel_ReturnsCanceled_OneBrowserOpen(t *testing.T) {
	var opens atomic.Int32
	prev := openBrowserFn
	openBrowserFn = func(string) error { opens.Add(1); return nil } // never a real browser
	t.Cleanup(func() { openBrowserFn = prev })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := DeviceCodeFlow(ctx, EnvProfile{
		Name: EnvLocal, ZitadelIssuer: "http://127.0.0.1:1", NativeClientID: "test",
		FleetBaseURL: "http://127.0.0.1:1", OIDCScopes: DefaultOIDCScopes,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DeviceCodeFlow err = %v, want context.Canceled", err)
	}
	if n := opens.Load(); n != 1 {
		t.Fatalf("browser opened %d times, want 1", n)
	}
}

// ── FR-9: role / roles ───────────────────────────────────────────────────────

func TestMergeRoles(t *testing.T) {
	cases := []struct {
		role  string
		roles []string
		want  []string
	}{
		{"", nil, nil},
		{"org_owner", nil, []string{"org_owner"}},
		{"", []string{"member", "policy_admin"}, []string{"member", "policy_admin"}},
		{"member", []string{"member", " policy_admin ", ""}, []string{"member", "policy_admin"}},
	}
	for _, c := range cases {
		got := mergeRoles(c.role, c.roles)
		if len(got) != len(c.want) {
			t.Fatalf("mergeRoles(%q,%v) = %v, want %v", c.role, c.roles, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("mergeRoles(%q,%v) = %v, want %v", c.role, c.roles, got, c.want)
			}
		}
	}
}

// ── F2: refresh transport failure vs definite rejection ──────────────────────

type expiredSink struct {
	mu sync.Mutex
	n  int
}

func (e *expiredSink) Emit(topic string, _ any) {
	if topic == TopicFleetSessionExpired {
		e.mu.Lock()
		e.n++
		e.mu.Unlock()
	}
}
func (e *expiredSink) count() int { e.mu.Lock(); defer e.mu.Unlock(); return e.n }

func TestClientDo_RefreshTransportFailure_IsNotSessionExpired(t *testing.T) {
	SetExternalTokenSource(nil)
	// Expired access token, refresh token present: do() refreshes first.
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt"})
	_ = keyring.Set(keyringService, keyExpiresAt(), "1")

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // token endpoint unreachable: the wake-from-sleep case
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer api.Close()
	c := makeTestClient(t, api.URL)
	c.profile.ZitadelIssuer = deadURL
	sink := &expiredSink{}
	c.SetSessionBroker(sink)

	_, err := c.Get(context.Background(), "/api/v1/x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrTokenExpired) {
		t.Fatalf("a transport failure during refresh returned ErrTokenExpired (%v) — the session is not dead", err)
	}
	if !errors.Is(err, ErrFleetUnreachable) {
		t.Fatalf("err = %v, want ErrFleetUnreachable (retryable)", err)
	}
	if n := sink.count(); n != 0 {
		t.Fatalf("fleet:session:expired fired %d time(s) for a transport failure", n)
	}
}

func TestClientDo_RefreshRejected_IsSessionExpired(t *testing.T) {
	SetExternalTokenSource(nil)
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt"})
	_ = keyring.Set(keyringService, keyExpiresAt(), "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := makeTestClient(t, srv.URL)
	sink := &expiredSink{}
	c.SetSessionBroker(sink)
	_, err := c.Get(context.Background(), "/api/v1/x")
	if !errors.Is(err, ErrTokenExpired) || sink.count() != 1 {
		t.Fatalf("err=%v expiredEvents=%d, want ErrTokenExpired + one event for a definite rejection", err, sink.count())
	}
}

// ── F6: capability delivery is serialised and never ends stale ───────────────

func TestCapabilityPoller_ConcurrentSetCurrent_LastDeliveryIsCurrent(t *testing.T) {
	p := NewCapabilityPoller(nil, "")
	var mu sync.Mutex
	var got []bool
	firstIn := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	p.OnChange(func(c Capabilities) {
		if calls.Add(1) == 1 { // first delivery stalls; later ones do not wait on it
			close(firstIn)
			<-release
		}
		mu.Lock()
		got = append(got, c.Enabled[CapSitesHosting])
		mu.Unlock()
	})
	a := Capabilities{Enabled: map[Capability]bool{CapSitesHosting: true}, Source: "fleet"}
	bset := Capabilities{Enabled: map[Capability]bool{CapContextSync: true}, Source: "fleet"}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.setCurrent(a) }()
	<-firstIn
	go func() { defer wg.Done(); p.setCurrent(bset) }()
	time.Sleep(50 * time.Millisecond) // let the second refresh race the stalled first
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || got[len(got)-1] {
		t.Fatalf("deliveries (sites_hosting per call) = %v; the last delivery carried the stale set", got)
	}
}

// Delta review #2: a rate limit is transient — it must not latch sync off.
func TestAppendBreaker_429_IsTransient(t *testing.T) {
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	now := time.Unix(1_000_000, 0)
	b.now = func() time.Time { return now }
	posts := 0
	_ = b.Do(context.Background(), "s", func(context.Context) error { posts++; return &AppendStatusError{Status: 429} })
	if s := lanes.Snapshot(LaneContextSync).Sessions[0]; s.Open || s.Reason != "rate_limited" {
		t.Fatalf("429 → %+v, want a transient (not open) rate_limited failure", s)
	}
	now = now.Add(appendBackoffMax)
	_ = b.Do(context.Background(), "s", func(context.Context) error { posts++; return nil })
	if posts != 2 || lanes.Snapshot(LaneContextSync).Status != LaneOK {
		t.Fatalf("posts=%d lane=%+v, want a retry after backoff that recovers", posts, lanes.Snapshot(LaneContextSync))
	}
}

// Delta review #3: a cancel during the token refresh stays a cancellation.
func TestClientDo_CancelDuringRefresh_PreservesCanceled(t *testing.T) {
	SetExternalTokenSource(nil)
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt"})
	_ = keyring.Set(keyringService, keyExpiresAt(), "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token" {
			// Hang until the client gives up (bounded so srv.Close can
			// never wait forever on this handler).
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := makeTestClient(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := c.Get(ctx, "/api/v1/x")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled preserved through the refresh wrap", err)
	}
	if reason, _ := classifyAppendError(err); reason == "" {
		t.Fatal("unreachable")
	}
	// And the breaker treats it as not-a-failure.
	lanes := NewSyncLanes()
	bk := NewAppendBreaker(lanes)
	_ = bk.Do(ctx, "s", func(context.Context) error { return err })
	if lanes.Snapshot(LaneContextSync).Status == LaneDegraded {
		t.Fatal("a cancel during refresh was counted as a sync failure")
	}
}

// Delta review #4: an IdP 5xx during refresh is not an expired session.
func TestRefreshTokenSet_IdP5xx_IsNotExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := RefreshTokenSet(context.Background(), EnvProfile{ZitadelIssuer: srv.URL, NativeClientID: "c"}, "rt")
	if err == nil || errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want a non-expired transient error", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer srv2.Close()
	_, err = RefreshTokenSet(context.Background(), EnvProfile{ZitadelIssuer: srv2.URL, NativeClientID: "c"}, "rt")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("400 invalid_grant err = %v, want ErrTokenExpired", err)
	}
}
