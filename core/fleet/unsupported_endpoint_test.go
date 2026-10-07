package fleet

// unsupported_endpoint_test.go — a plain (route-level) 404 from a fleet
// server that has no such endpoint latches the feature UNSUPPORTED: logged
// once at INFO, never re-requested, events stay local. A 5xx keeps the
// existing transient handling. Verified fleet fact (kenaz-fleet main,
// 2026-10-05): /api/v1/context/append, /api/v1/context/replay,
// /api/v1/handoff/* and /api/v1/audit/append are not registered and answer
// the Go mux's "404 page not found".

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

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// countingFleet answers every request with a fixed status/body and counts
// hits per path. Handler goroutine writes, test body reads: mutex + snapshot.
type countingFleet struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	status int
	body   string
	ctype  string
}

func newCountingFleet(t *testing.T, status int, body, ctype string) *countingFleet {
	t.Helper()
	f := &countingFleet{hits: map[string]int{}, status: status, body: body, ctype: ctype}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		status, body, ctype := f.status, f.body, f.ctype
		f.mu.Unlock()
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *countingFleet) hitsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *countingFleet) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.hits {
		n += v
	}
	return n
}

const goMuxNotFound = "404 page not found\n"

func TestIsPlainNotFound(t *testing.T) {
	const plain = "text/plain; charset=utf-8"
	for _, tc := range []struct {
		status int
		ct     string
		body   string
		want   bool
	}{
		{404, plain, goMuxNotFound, true},
		{404, "", "", true},
		{404, "application/json", `{"code":"not_found","message":"no such context"}`, false},
		{404, plain, `{"message":"no code"}`, true},
		{404, "text/html; charset=utf-8", "<html><body>404 Not Found</body></html>", false},
		{404, "application/xml", "<Error><Code>NoSuchKey</Code></Error>", false},
		{404, plain, "<html>mislabelled</html>", false},
		{404, "", goMuxNotFound, false}, // non-empty body without text/plain
		{500, plain, goMuxNotFound, false},
		{200, "", "", false},
	} {
		if got := isPlainNotFound(tc.status, tc.ct, []byte(tc.body)); got != tc.want {
			t.Errorf("isPlainNotFound(%d, %q, %q) = %v, want %v", tc.status, tc.ct, tc.body, got, tc.want)
		}
	}
}

func TestEventStreamAppend_Plain404_UnsupportedOnce_NoFurtherHTTP(t *testing.T) {
	logs := captureLogs(t)
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain; charset=utf-8")
	es := makeTestStream(t, f.srv.URL, "sess:abc")

	for i := 0; i < 5; i++ {
		err := es.Append(context.Background(), []Event{{Payload: []byte("x")}})
		if !errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("append %d: err = %v, want ErrEndpointUnsupported", i, err)
		}
	}
	if got := f.hitsFor("/api/v1/context/append"); got != 1 {
		t.Fatalf("append route hit %d times, want exactly 1 — an unsupported route must not be retried", got)
	}
	// Replay belongs to the same (absent) feature: short-circuits too.
	err := es.Replay(context.Background(), 0, func(Event) error { return nil })
	if !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("replay err = %v, want ErrEndpointUnsupported", err)
	}
	if got := f.hitsFor("/api/v1/context/replay"); got != 0 {
		t.Fatalf("replay hit %d times after the feature latched, want 0", got)
	}
	if n := logs.count(slog.LevelInfo, "fleet.endpoint.unsupported"); n != 1 {
		t.Fatalf("fleet.endpoint.unsupported logged %d times, want once", n)
	}
}

func TestEventStreamAppend_JSON404_IsNotUnsupported(t *testing.T) {
	f := newCountingFleet(t, http.StatusNotFound, `{"code":"not_found","message":"no such stream"}`, "application/json")
	es := makeTestStream(t, f.srv.URL, "sess:abc")
	for i := 0; i < 2; i++ {
		err := es.Append(context.Background(), []Event{{Payload: []byte("x")}})
		var se *AppendStatusError
		if !errors.As(err, &se) || se.Status != 404 || errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("err = %v, want AppendStatusError{404} (an application answer, not a missing route)", err)
		}
	}
	if got := f.hitsFor("/api/v1/context/append"); got != 2 {
		t.Fatalf("hits = %d, want 2 (a JSON 404 does not latch)", got)
	}
}

func TestEventStreamAppend_500_KeepsTransientBehaviour(t *testing.T) {
	f := newCountingFleet(t, http.StatusInternalServerError, "boom", "text/plain")
	es := makeTestStream(t, f.srv.URL, "sess:abc")
	// Through the breaker a 5xx is still a transient, degraded failure — the
	// client's own 5xx retry loop runs as before, and the feature never
	// latches unsupported.
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	err := b.Do(context.Background(), "s1", func(ctx context.Context) error {
		return es.Append(ctx, []Event{{Payload: []byte("x")}})
	})
	if err == nil || errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("err = %v, want a transient (non-unsupported) failure", err)
	}
	first := f.hitsFor("/api/v1/context/append")
	if first == 0 {
		t.Fatal("append route never hit")
	}
	lane := lanes.Snapshot(LaneContextSync)
	if lane.Status != LaneDegraded || len(lane.Sessions) != 1 || lane.Sessions[0].Open {
		t.Fatalf("lane = %+v, want degraded with one not-yet-open session", lane)
	}
	// Not latched: a later attempt goes back to the server.
	if err := es.Append(context.Background(), []Event{{Payload: []byte("x")}}); errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("second append err = %v, a 5xx must not latch", err)
	}
	if got := f.hitsFor("/api/v1/context/append"); got <= first {
		t.Fatalf("hits %d → %d, want the second attempt to reach the server", first, got)
	}
}

func TestAppendBreaker_Unsupported_LaneOff_NoBreakerChurn(t *testing.T) {
	logs := captureLogs(t)
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	var posts atomic.Int32
	post := func(context.Context) error {
		posts.Add(1)
		return &UnsupportedEndpointError{Feature: FeatureContextEventStream, Endpoint: "/api/v1/context/append"}
	}
	for i := 0; i < 10; i++ {
		if err := b.Do(context.Background(), "s1", post); err != nil {
			t.Fatalf("Do returned %v, want nil — unsupported is not a failure", err)
		}
	}
	lane := lanes.Snapshot(LaneContextSync)
	if lane.Status != LaneOff || lane.Reason != reasonEndpointUnsupported || len(lane.Sessions) != 0 {
		t.Fatalf("lane = %+v, want off/%s with no per-session breaker state", lane, reasonEndpointUnsupported)
	}
	if w := logs.count(slog.LevelWarn, "rpc.context_sync.append_event_failed"); w != 0 {
		t.Fatalf("append_event_failed WARN logged %d times, want 0", w)
	}
	// Process lifetime: a sign-in reset does not un-latch it.
	b.ResetAll()
	_ = b.Do(context.Background(), "s2", post)
	if lane := lanes.Snapshot(LaneContextSync); lane.Status != LaneOff {
		t.Fatalf("after ResetAll lane = %+v, want still off", lane)
	}
}

func TestSessionSyncer_EnableSync_RefusedOnceUnsupported(t *testing.T) {
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain")
	es := makeTestStream(t, f.srv.URL, "sess:abc")
	_ = es.Append(context.Background(), []Event{{Payload: []byte("x")}}) // latches
	ss := NewSessionSyncer(es.client, nil, nil)
	if err := ss.EnableSync(context.Background(), "abc", nil); !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("EnableSync err = %v, want ErrEndpointUnsupported (don't persist a toggle that can't sync)", err)
	}
}

func TestHandoff_Plain404_UnsupportedOnce_NoFurtherHTTP(t *testing.T) {
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain")
	h := NewHandoffHandler(makeTestClient(t, f.srv.URL), nil, nil)

	for i := 0; i < 3; i++ {
		if _, err := h.Inbox(context.Background()); !errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("Inbox %d: err = %v, want ErrEndpointUnsupported", i, err)
		}
	}
	if _, err := h.AcceptShare(context.Background(), "item-1"); !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("AcceptShare err = %v, want ErrEndpointUnsupported", err)
	}
	if err := h.ShareSession(context.Background(), "s", "u", nil); !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("ShareSession err = %v, want ErrEndpointUnsupported", err)
	}
	if got := f.total(); got != 1 {
		t.Fatalf("fleet hit %d times, want exactly 1 (the first inbox GET)", got)
	}
}

func TestHandoff_500_IsNotUnsupported(t *testing.T) {
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusInternalServerError, "boom", "text/plain")
	h := NewHandoffHandler(makeTestClient(t, f.srv.URL), nil, nil)
	_, err := h.Inbox(context.Background())
	if err == nil || errors.Is(err, ErrEndpointUnsupported) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Inbox err = %v, want a status-500 error", err)
	}
	first := f.hitsFor("/api/v1/handoff/inbox")
	if _, err := h.Inbox(context.Background()); errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("second Inbox err = %v, a 5xx must not latch", err)
	}
	if got := f.hitsFor("/api/v1/handoff/inbox"); got <= first {
		t.Fatalf("inbox hits %d → %d, want the second call to reach the server", first, got)
	}
}

// plainPoster answers every audit POST with a fixed status/body.
type plainPoster struct {
	mu     sync.Mutex
	posts  int
	status int
	body   string
}

func (p *plainPoster) Post(_ context.Context, _, _ string, body io.Reader) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, body)
	p.mu.Lock()
	p.posts++
	status, b := p.status, p.body
	p.mu.Unlock()
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rec.WriteHeader(status)
	_, _ = io.WriteString(rec, b)
	return rec.Result(), nil
}

func (p *plainPoster) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.posts }

func newArchiverWithEvents(t *testing.T, poster AuditHTTPPoster) *AuditArchiver {
	t.Helper()
	tr := &contextaudit.MemoryTailReader{}
	e1 := makeTailEvent("EV001", [32]byte{})
	tr.Append(e1)
	return NewAuditArchiver(AuditArchiverConfig{
		Poster:        poster,
		DataDir:       t.TempDir(),
		Tail:          tr,
		Signer:        fakeSigner{},
		BatchSize:     100,
		BatchInterval: 10 * time.Millisecond,
	})
}

func TestAuditArchiver_Plain404_UnsupportedOnce_LoopStops(t *testing.T) {
	logs := captureLogs(t)
	p := &plainPoster{status: http.StatusNotFound, body: goMuxNotFound}
	a := newArchiverWithEvents(t, p)

	for i := 0; i < 3; i++ {
		if err := a.flushOnce(context.Background()); !errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("flush %d: err = %v, want ErrEndpointUnsupported", i, err)
		}
	}
	if got := p.count(); got != 1 {
		t.Fatalf("audit append posted %d times, want 1", got)
	}
	if err := a.ArchiveNow(context.Background()); !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("ArchiveNow err = %v, want ErrEndpointUnsupported", err)
	}
	if n := logs.count(slog.LevelInfo, "fleet.endpoint.unsupported"); n != 1 {
		t.Fatalf("unsupported logged %d times, want once", n)
	}

	// The background loop exits instead of backing off forever, so the
	// compliance status stops reporting the archiver as running.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for a.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.IsRunning() {
		t.Fatal("archiver still running after the endpoint latched unsupported")
	}
	if got := p.count(); got != 1 {
		t.Fatalf("loop posted again (%d posts) after the latch", got)
	}
}

func TestAuditArchiver_500_KeepsRetrying(t *testing.T) {
	p := &plainPoster{status: http.StatusInternalServerError, body: "boom"}
	a := newArchiverWithEvents(t, p)
	for i := 0; i < 2; i++ {
		err := a.flushOnce(context.Background())
		if err == nil || errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("flush err = %v, want a transient status-500 error", err)
		}
	}
	if got := p.count(); got != 2 {
		t.Fatalf("posted %d times, want 2 — a 5xx does not latch", got)
	}
}

func TestEventStreamAppend_HTML404_IsTransientNotLatched(t *testing.T) {
	f := newCountingFleet(t, http.StatusNotFound, "<html><body><h1>404 Not Found</h1></body></html>", "text/html; charset=utf-8")
	es := makeTestStream(t, f.srv.URL, "sess:abc")
	for i := 0; i < 2; i++ {
		err := es.Append(context.Background(), []Event{{Payload: []byte("x")}})
		if errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("an HTML 404 page latched unsupported: %v", err)
		}
		reason, permanent := classifyAppendError(err)
		if reason != "fleet_api_not_routed" || permanent {
			t.Fatalf("classify = (%q, %v), want fleet_api_not_routed/transient", reason, permanent)
		}
	}
	if got := f.hitsFor("/api/v1/context/append"); got != 2 {
		t.Fatalf("hits = %d, want 2 — an HTML 404 is retried, not latched", got)
	}
}

func TestUnsupportedLatch_SignInReset_RetriesOnce(t *testing.T) {
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain; charset=utf-8")
	es := makeTestStream(t, f.srv.URL, "sess:abc")
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	appendOnce := func() {
		_ = b.Do(context.Background(), "s1", func(ctx context.Context) error {
			return es.Append(ctx, []Event{{Payload: []byte("x")}})
		})
	}
	appendOnce()
	appendOnce()
	if got := f.hitsFor("/api/v1/context/append"); got != 1 {
		t.Fatalf("hits before reset = %d, want 1", got)
	}
	if l := lanes.Snapshot(LaneContextSync); l.Status != LaneOff {
		t.Fatalf("lane = %+v, want off", l)
	}

	// Sign-in: the settings view runs its session-reset hooks — the
	// client latch and the breaker (core/rpc/api.go wiring).
	es.client.ResetUnsupportedEndpoints()
	b.ResetAll()
	if l := lanes.Snapshot(LaneContextSync); l.Status != LaneUnknown {
		t.Fatalf("lane after reset = %+v, want unknown (re-probe pending)", l)
	}
	appendOnce()
	appendOnce()
	if got := f.hitsFor("/api/v1/context/append"); got != 2 {
		t.Fatalf("hits after reset = %d, want exactly one more probe (2)", got)
	}
}

func TestAuditArchiver_ResetUnsupported_RestartsLoop(t *testing.T) {
	p := &plainPoster{status: http.StatusNotFound, body: goMuxNotFound}
	a := newArchiverWithEvents(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Stop()
	waitFor := func(cond func() bool, what string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !cond() {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	waitFor(func() bool { return a.Unsupported() && !a.IsRunning() }, "loop to exit via the latch")
	if got := p.count(); got != 1 {
		t.Fatalf("posts = %d, want 1", got)
	}

	// Fleet ships the route; the user signs in again.
	p.mu.Lock()
	p.status = http.StatusOK
	p.mu.Unlock()
	a.ResetUnsupported()
	waitFor(func() bool { return a.LastArchivedAt() != (time.Time{}) }, "the restarted loop to archive")
	if a.Unsupported() {
		t.Fatal("still latched after reset")
	}
}

func TestAuditArchiver_ResetUnsupported_NoRestartAfterStop(t *testing.T) {
	p := &plainPoster{status: http.StatusNotFound, body: goMuxNotFound}
	a := newArchiverWithEvents(t, p)
	_ = a.flushOnce(context.Background()) // latch
	a.Start(context.Background())
	a.Stop()
	a.ResetUnsupported()
	if a.IsRunning() {
		t.Fatal("a stopped archiver was restarted by the reset hook")
	}
}

// WP04 (audit §2.2): /team/members and /identity/public-key are routes fleet
// does not register either; a plain 404 latches each, no further HTTP.
func TestTeamMembersAndPublicKey_Plain404_Latched(t *testing.T) {
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain")
	h := NewHandoffHandler(makeTestClient(t, f.srv.URL), nil, nil)
	for i := 0; i < 3; i++ {
		if _, err := h.ListTeam(context.Background()); !errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("ListTeam %d: err = %v, want ErrEndpointUnsupported", i, err)
		}
		if _, err := h.fetchRecipientKeys(context.Background(), "u1"); !errors.Is(err, ErrEndpointUnsupported) {
			t.Fatalf("public key %d: err = %v, want ErrEndpointUnsupported", i, err)
		}
	}
	if a, b := f.hitsFor("/api/v1/team/members"), f.hitsFor("/api/v1/identity/public-key"); a != 1 || b != 1 {
		t.Fatalf("hits team/members=%d public-key=%d, want 1 each", a, b)
	}
}

// A JSON 404 on public-key is the application saying "no key for that
// user" — recipient-not-found, not a missing route.
func TestPublicKey_JSON404_IsRecipientNotFound(t *testing.T) {
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusNotFound, `{"code":"not_found","message":"no key"}`, "application/json")
	h := NewHandoffHandler(makeTestClient(t, f.srv.URL), nil, nil)
	if _, err := h.fetchRecipientKeys(context.Background(), "u1"); !errors.Is(err, ErrHandoffRecipientNotFound) {
		t.Fatalf("err = %v, want ErrHandoffRecipientNotFound", err)
	}
	if _, err := h.fetchRecipientKeys(context.Background(), "u1"); errors.Is(err, ErrEndpointUnsupported) {
		t.Fatal("a JSON 404 latched the route unsupported")
	}
}

// audit_append now latches on the Client too, so the shared, resettable
// latch reports it.
func TestAuditArchiver_Plain404_LatchesClient(t *testing.T) {
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusNotFound, goMuxNotFound, "text/plain")
	c := makeTestClient(t, f.srv.URL)
	a := newArchiverWithEvents(t, c)
	a.cfg.Client = c
	if err := a.flushOnce(context.Background()); !errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("flush err = %v", err)
	}
	if c.endpointUnsupported(FeatureAuditAppend) == nil {
		t.Fatal("audit_append not latched on the Client")
	}
	c.ResetUnsupportedEndpoints()
	a.ResetUnsupported()
	if a.isUnsupported() {
		t.Fatal("session reset did not clear the audit_append latch")
	}
}
