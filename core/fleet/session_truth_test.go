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

func TestAppendBreaker_TransientFailures_BackOffThenOpen(t *testing.T) {
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	now := time.Unix(1_000_000, 0)
	b.now = func() time.Time { return now }
	var posts int
	fail := func(context.Context) error { posts++; return &AppendStatusError{Status: 503} }

	_ = b.Do(context.Background(), "s", fail) // attempt 1 → backoff 30s
	_ = b.Do(context.Background(), "s", fail) // inside backoff → held back
	if posts != 1 {
		t.Fatalf("posted %d times inside the backoff window, want 1", posts)
	}
	for i := 0; i < 10; i++ {
		now = now.Add(appendBackoffMax + time.Second)
		_ = b.Do(context.Background(), "s", fail)
	}
	if posts != appendMaxConsecutiveFailures {
		t.Fatalf("posted %d times, want the circuit to open after %d", posts, appendMaxConsecutiveFailures)
	}
	if s := lanes.Snapshot(LaneContextSync).Sessions[0]; !s.Open || s.Reason != "server_error" {
		t.Fatalf("session = %+v, want open/server_error", s)
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
func TestEventStreamAppend_404_IsTypedStatusError(t *testing.T) {
	withExternalToken(t, "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/context/append") {
			http.NotFound(w, r)
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
	lane := lanes.Snapshot(LaneUnitPoll)
	if lane.Status != LaneDegraded || lane.ConsecutiveFailures != unitPollWarnThreshold || lane.Reason != "network" {
		t.Fatalf("lane = %+v, want degraded/network with the consecutive count", lane)
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
