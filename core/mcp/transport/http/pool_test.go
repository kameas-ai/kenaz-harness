package http_test

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	httptransport "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
)

// connector-lifecycle-truth-01PMZ303 UNIT-6: before this unit,
// http.Pool had no CloseOne method at all — dispatch.Pool's CloseOne
// fell through a comment-only "http and sse pools do not yet expose a
// per-server CloseOne method" arm and reported success without doing
// anything. These tests drive a REAL *http.Pool against a REAL
// httptest server (spec.md test rule R-2: "a fake sub-pool proves
// nothing about http.Pool") and assert on observable state — request
// counts, Tools()/Call() routing, probe-goroutine exit — never on
// CloseOne's return value alone, which already returned nil before
// the fix (spec.md AC-004: "CloseOne returning nil is not evidence —
// it already does").

// toolsListServer answers every JSON-RPC POST (including the health
// probe's tools/list) with a minimal one-tool success envelope, and
// counts how many requests it has received so tests can prove
// whether the probe is still ticking.
func toolsListServer(reqCount *int32) *httptest.Server {
	return httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(reqCount, 1)
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      raw["id"],
			"result": map[string]any{
				"tools": []any{map[string]any{"name": "t1"}},
			},
		})
	}))
}

// tickerRegistry records fakeTicker instances as HealthProbe.Start's
// internal goroutine creates them (NewTicker's factory runs inside
// that goroutine, not synchronously in Start), so it needs its own
// mutex + snapshot per CLAUDE.md's race-safe test-fake pattern rather
// than a bare slice the test goroutine reads unsynchronised.
type tickerRegistry struct {
	mu   sync.Mutex
	list []*fakeTicker
}

func (r *tickerRegistry) add(ft *fakeTicker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, ft)
}

func (r *tickerRegistry) snapshot() []*fakeTicker {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*fakeTicker, len(r.list))
	copy(out, r.list)
	return out
}

// waitForTicker polls until the registry holds at least n tickers and
// returns the n-th one (1-indexed by call count: the first call
// wanting n=1 returns the ticker from the first Open). Fails the test
// if n tickers never appear within a generous deadline.
func waitForTicker(t *testing.T, r *tickerRegistry, n int) *fakeTicker {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if list := r.snapshot(); len(list) >= n {
			return list[n-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry never reached %d ticker(s)", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestPoolCloseOneStopsProbeAndConnection is AC-004(a)+(b)+(c) at the
// http.Pool level: after CloseOne returns, the server's tools are
// gone from Tools(), the probe goroutine has exited (HealthProbe.Stop
// blocks on doneCh — see health.go), and — the strongest form of the
// assertion — the test server receives no further request no matter
// how many more ticks the (now-orphaned) fake ticker is fed.
func TestPoolCloseOneStopsProbeAndConnection(t *testing.T) {
	t.Parallel()
	var reqCount int32
	srv := toolsListServer(&reqCount)
	defer srv.Close()

	ft := newFakeTicker() // declared in health_test.go, same package
	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: 5 * time.Millisecond, // irrelevant value: the fake ticker ignores its factory argument
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Prove the probe is actually wired to this connection BEFORE
	// asserting it stops — a test that never lets the probe tick
	// would pass even against the pre-fix code (spec.md AC-005's
	// false-pass note, restated for AC-004: the probe must be
	// observed running before CloseOne, or "it stopped" is
	// unfalsifiable).
	ft.Tick()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&reqCount) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	before := atomic.LoadInt32(&reqCount)
	if before == 0 {
		t.Fatal("probe never reached the test server before CloseOne; the test proves nothing")
	}

	if err := pool.CloseOne(context.Background(), "srv1"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}

	// CloseOne already returned, and HealthProbe.Stop() blocks on the
	// probe goroutine's doneCh closing — so the goroutine is
	// GUARANTEED to have exited by this point, not just "probably
	// stopped soon". Feed the (now-dead) ticker several more ticks;
	// since nothing is listening, this must not produce further
	// requests.
	for i := 0; i < 5; i++ {
		select {
		case ft.ch <- time.Now():
		default:
		}
	}
	time.Sleep(50 * time.Millisecond)
	after := atomic.LoadInt32(&reqCount)
	if after != before {
		t.Errorf("requests after CloseOne = %d, want %d (probe goroutine should have exited)", after, before)
	}

	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("Tools after CloseOne = %+v, want empty", tools)
	}
	if _, err := pool.Call(context.Background(), "srv1", "t1", nil); err == nil {
		t.Error("Call after CloseOne: want an error, got nil")
	}
}

// TestPoolCloseOneLeavesOtherServersRunning is the negative half of
// AC-004: a teardown that over-deletes is worse than one that
// under-deletes. Two servers are opened; CloseOne on one must not
// touch the other's connection, tools, or callability.
func TestPoolCloseOneLeavesOtherServersRunning(t *testing.T) {
	t.Parallel()
	var reqA, reqB int32
	srvA := toolsListServer(&reqA)
	defer srvA.Close()
	srvB := toolsListServer(&reqB)
	defer srvB.Close()

	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: -1, // disable the probe; irrelevant to this assertion
	})
	specs := []coremcp.ServerSpec{
		{Name: "a", Transport: "http", URL: srvA.URL},
		{Name: "b", Transport: "http", URL: srvB.URL},
	}
	if err := pool.Open(context.Background(), specs); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := pool.CloseOne(context.Background(), "a"); err != nil {
		t.Fatalf("CloseOne(a): %v", err)
	}

	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Server != "b" {
		t.Fatalf("Tools after CloseOne(a) = %+v, want exactly server b's tool", tools)
	}
	if _, err := pool.Call(context.Background(), "b", "t1", nil); err != nil {
		t.Errorf("Call(b) after CloseOne(a): unrelated server must still work, got %v", err)
	}
	if _, err := pool.Call(context.Background(), "a", "t1", nil); err == nil {
		t.Error("Call(a) after CloseOne(a): want an error, got nil")
	}
}

// TestPoolCloseOneIdempotentAndSafeBeforeClose pins the AC-004
// idempotence requirement: CloseOne twice, and CloseOne then
// pool-wide Close, must neither panic nor double-close.
func TestPoolCloseOneIdempotentAndSafeBeforeClose(t *testing.T) {
	t.Parallel()
	var reqCount int32
	srv := toolsListServer(&reqCount)
	defer srv.Close()

	pool := httptransport.NewPool(httptransport.PoolOptions{PingPeriod: -1})
	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := pool.CloseOne(context.Background(), "srv1"); err != nil {
		t.Fatalf("first CloseOne: %v", err)
	}
	if err := pool.CloseOne(context.Background(), "srv1"); !errors.Is(err, httptransport.ErrServerNotFound) {
		t.Errorf("second CloseOne = %v, want ErrServerNotFound", err)
	}
	// Must not panic or double-close when the pool is torn down after
	// an explicit per-server close already ran.
	if err := pool.Close(context.Background()); err != nil {
		t.Errorf("Close after CloseOne: %v", err)
	}
}

// TestPoolCloseOneUnknownServer pins the plain not-found case with no
// server ever opened.
func TestPoolCloseOneUnknownServer(t *testing.T) {
	t.Parallel()
	pool := httptransport.NewPool(httptransport.PoolOptions{})
	if err := pool.CloseOne(context.Background(), "never-existed"); !errors.Is(err, httptransport.ErrServerNotFound) {
		t.Errorf("CloseOne unknown = %v, want ErrServerNotFound", err)
	}
}

// TestPoolOpenReplacesExistingNameEvenWithoutCloseOne documents a
// pre-existing fact this UNIT-6 fix does NOT change and that the
// mission spec's N-5 finding must be read against: openOne already
// self-heals a same-name re-Open by stopping the OLD entry's probe
// and closing its connection (the two-site `entry.probe.Stop()` the
// spec's own §1.3 cites as living "inside openOne"), independent of
// whether CloseOne was ever called first. This test opens the same
// spec.Name twice with NO CloseOne between them — simulating exactly
// what InstallRecipe's evict-before-spawn step produced before this
// UNIT-6 fix, when CloseOne was a no-op — and shows the old probe
// still stops.
//
// This narrows what UNIT-6 actually had to fix on the RE-INSTALL
// path: the accumulation spec.md N-5 describes ("the pool
// accumulates a second entry and the first one's probe keeps
// running") does not reproduce here, because Open()'s own duplicate-
// name handling already tears the old entry down. What UNIT-6's
// CloseOne fix newly enables is UNINSTALL WITHOUT a following
// re-install — there openOne's replace-on-collision logic never
// runs at all, so nothing but a real CloseOne can stop the old
// probe. See this file's doc comment and the mission report for the
// full account.
func TestPoolOpenReplacesExistingNameEvenWithoutCloseOne(t *testing.T) {
	t.Parallel()
	var reqCount int32
	srv := toolsListServer(&reqCount)
	defer srv.Close()

	// Each openOne call gets its OWN fake ticker (distinct channel), so
	// ticking the FIRST entry's ticker after the second Open can only
	// ever reach the first entry's probe goroutine — the second
	// entry's probe reads from a different channel entirely. That
	// isolation is what makes "the old probe is silent" a real
	// assertion instead of an artifact of both probes sharing one
	// channel and one target URL.
	//
	// NewTicker's factory function runs inside HealthProbe.Start's own
	// spawned goroutine (Start returns before the goroutine calls it),
	// so tr (the registry) needs its own mutex + snapshot — the
	// CLAUDE.md race-safe-fake pattern — rather than a bare slice a
	// second goroutine appends to while the test goroutine reads it.
	tr := &tickerRegistry{}
	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: 5 * time.Millisecond,
		NewTicker: func(time.Duration) transport.Ticker {
			ft := newFakeTicker()
			tr.add(ft)
			return ft
		},
	})

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	ft1 := waitForTicker(t, tr, 1)

	// Confirm the FIRST connection's probe is really ticking before we
	// replace it — otherwise "it stopped" is unfalsifiable.
	ft1.Tick()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&reqCount) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	before := atomic.LoadInt32(&reqCount)
	if before == 0 {
		t.Fatal("first connection's probe never reached the server; the test proves nothing")
	}

	// Re-open the SAME name with NO CloseOne in between — this is the
	// pre-fix evict-before-spawn shape (CloseOne no-op'd, so only
	// openOne's own replace-on-collision logic could tear the old
	// entry down).
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("second Open (same name): %v", err)
	}
	waitForTicker(t, tr, 2)

	// Feed the FIRST (old) entry's ticker several more ticks. If
	// openOne's replace-on-collision Stop() truly silenced the old
	// probe goroutine, nothing is left reading ft1.ch and these sends
	// must produce no further requests.
	before2 := atomic.LoadInt32(&reqCount)
	for i := 0; i < 5; i++ {
		select {
		case ft1.ch <- time.Now():
		default:
		}
	}
	time.Sleep(50 * time.Millisecond)
	after := atomic.LoadInt32(&reqCount)
	if after != before2 {
		t.Errorf("requests after ticking the OLD entry's ticker = %d, want %d unchanged (openOne's replace-on-collision Stop should have silenced the old probe)", after, before2)
	}

	// Exactly one entry remains — no accumulation.
	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("Tools after re-Open = %+v, want exactly one entry (no accumulation)", tools)
	}
}
