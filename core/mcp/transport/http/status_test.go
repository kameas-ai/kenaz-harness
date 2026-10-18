package http_test

import (
	"context"
	"encoding/json"
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

// connector-lifecycle-truth-01PMZ303 UNIT-7 (spec.md §1.4, FR-005,
// AC-005): before this unit, core/mcp/dispatch/pool.go fabricated
// State/Enabled/UpdatedAt for every http-owned recipe id — the
// ownership entry proved Open once succeeded and nothing else. These
// tests drive http.Pool's own RecipeStatus/AllRecipeStatuses directly
// against a REAL httptest server (spec.md test rule R-2: "a fake
// sub-pool proves nothing") to prove the accessor is honest at its
// source, independent of dispatch.Pool's wiring (covered separately).

// flippableServer answers tools/list with a success envelope while
// healthy != 0, and with HTTP 401 otherwise — the same shape ★2's
// TestHealthProbe_TripsOnHTTP401 (health_test.go) proved actually
// trips the probe. reqCount lets a test prove the probe ticked before
// trusting any assertion built on top of it (AC-005's false-pass
// note: "a test that never lets the probe run … passes falsely").
func flippableServer(reqCount *int32, healthy *int32) *httptest.Server {
	return httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(reqCount, 1)
		if atomic.LoadInt32(healthy) == 0 {
			w.WriteHeader(stdhttp.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
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

func waitForRequestCount(t *testing.T, reqCount *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if atomic.LoadInt32(reqCount) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("request count never reached %d (stuck at %d) — the probe never ticked; this test proves nothing", want, atomic.LoadInt32(reqCount))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitForState(t *testing.T, pool *httptransport.Pool, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	for {
		rs, ok := pool.RecipeStatus(id)
		if ok {
			last = rs.State
			if last == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("RecipeStatus(%q).State never reached %q (stuck at %q)", id, want, last)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestPoolRecipeStatus_HonestState is AC-005 (FR-005): a healthy
// server reports "running"; a tripped probe (two consecutive 401s)
// reports a non-running state with a non-empty LastError at BOTH the
// single-id (RecipeStatus) and the panel-facing (AllRecipeStatuses)
// sites; recovery to 200 returns the state to "running" — the
// transition CLAUDE.md calls "the half that 'fabricated' status gets
// wrong" is observable both ways, not just failure-only.
//
// Mutation target: reverting RecipeStatus/AllRecipeStatuses to the
// synthesised `State: string(transport.StateRunning)` literal must
// fail every assertion below except the first (a permanently-"running"
// stub trivially passes the healthy check and trivially fails
// everything after the trip).
func TestPoolRecipeStatus_HonestState(t *testing.T) {
	t.Parallel()
	var reqCount int32
	var healthy int32 = 1
	srv := flippableServer(&reqCount, &healthy)
	defer srv.Close()

	ft := newFakeTicker() // declared in health_test.go, same package
	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: 5 * time.Millisecond, // irrelevant: fakeTicker ignores its factory argument
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close(context.Background()) }()

	if _, ok := pool.RecipeStatus("unknown-id"); ok {
		t.Error("RecipeStatus(unknown id): want ok=false")
	}

	// --- Healthy: first tick must actually reach the server before
	// any assertion is trusted.
	ft.Tick()
	waitForRequestCount(t, &reqCount, 1)
	waitForState(t, pool, "srv1", string(transport.StateRunning))

	rs, ok := pool.RecipeStatus("srv1")
	if !ok {
		t.Fatal("RecipeStatus: not found")
	}
	if rs.LastError != "" {
		t.Errorf("healthy LastError = %q, want empty", rs.LastError)
	}
	if rs.UpdatedAt.IsZero() {
		t.Error("healthy UpdatedAt is zero, want the probe's last-tick time")
	}

	// --- Trip: flip to 401. Two consecutive failing ticks trip
	// OnFailure (health.go) and must flip the derived state.
	atomic.StoreInt32(&healthy, 0)
	before := atomic.LoadInt32(&reqCount)
	ft.Tick()
	waitForRequestCount(t, &reqCount, before+1)
	ft.Tick()
	waitForRequestCount(t, &reqCount, before+2)

	waitForState(t, pool, "srv1", string(transport.StateFailed))

	rs, ok = pool.RecipeStatus("srv1")
	if !ok {
		t.Fatal("RecipeStatus after trip: not found")
	}
	if rs.State == string(transport.StateRunning) {
		t.Fatalf("RecipeStatus after trip: State = %q, want non-running", rs.State)
	}
	if rs.LastError == "" {
		t.Error("RecipeStatus after trip: LastError is empty, want the probe's error")
	}

	// AllRecipeStatuses is the site the health panel actually reads
	// (dispatch.Pool.AllRecipeStatuses -> views/mcp/impl.go's
	// HealthSnapshot) — assert it independently of the single-id path.
	all := pool.AllRecipeStatuses()
	if len(all) != 1 {
		t.Fatalf("AllRecipeStatuses = %d entries, want 1", len(all))
	}
	if all[0].ID != "srv1" {
		t.Errorf("AllRecipeStatuses[0].ID = %q, want %q", all[0].ID, "srv1")
	}
	if all[0].State == string(transport.StateRunning) {
		t.Errorf("AllRecipeStatuses[0].State = %q, want non-running", all[0].State)
	}
	if all[0].LastError == "" {
		t.Error("AllRecipeStatuses[0].LastError is empty, want the probe's error")
	}

	// --- Recovery: flip back to 200; the state returns to running —
	// this is the direction a purely failure-biased implementation
	// (report unhealthy for everything) could not satisfy.
	atomic.StoreInt32(&healthy, 1)
	before = atomic.LoadInt32(&reqCount)
	ft.Tick()
	waitForRequestCount(t, &reqCount, before+1)

	waitForState(t, pool, "srv1", string(transport.StateRunning))
	rs, ok = pool.RecipeStatus("srv1")
	if !ok {
		t.Fatal("RecipeStatus after recovery: not found")
	}
	if rs.LastError != "" {
		t.Errorf("RecipeStatus after recovery: LastError = %q, want cleared", rs.LastError)
	}
}

// healthEvent is a race-safe record of one SetHealthObserver call —
// snapshot() pattern per CLAUDE.md's race-safe test-fake discipline
// (the callback fires from the probe goroutine; the test body reads
// it from the main goroutine).
type healthEvent struct {
	id, previous string
	current      transport.RecipeStatus
}

type healthEventLog struct {
	mu     sync.Mutex
	events []healthEvent
}

func (l *healthEventLog) record(id, previous string, current transport.RecipeStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, healthEvent{id: id, previous: previous, current: current})
}

func (l *healthEventLog) snapshot() []healthEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]healthEvent, len(l.events))
	copy(out, l.events)
	return out
}

// TestPoolSetHealthObserver_FiresOnTrip is AC-005b's Go half at the
// http.Pool level: a probe trip must produce an observer call the
// dispatch/rpc layers can turn into a PublishHealthChange (UNIT-8).
// Before UNIT-7/8, HealthProbe.OnFailure only logged — there was no
// hook here to fire at all.
func TestPoolSetHealthObserver_FiresOnTrip(t *testing.T) {
	t.Parallel()
	var reqCount int32
	var healthy int32 = 1
	srv := flippableServer(&reqCount, &healthy)
	defer srv.Close()

	ft := newFakeTicker()
	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: 5 * time.Millisecond,
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})

	log := &healthEventLog{}
	pool.SetHealthObserver(log.record)

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close(context.Background()) }()

	// First tick: "" -> running. Not the trip, but proves the
	// observer fires on the FIRST real transition too.
	ft.Tick()
	waitForRequestCount(t, &reqCount, 1)
	waitForState(t, pool, "srv1", string(transport.StateRunning))

	atomic.StoreInt32(&healthy, 0)
	before := atomic.LoadInt32(&reqCount)
	ft.Tick()
	waitForRequestCount(t, &reqCount, before+1)
	ft.Tick()
	waitForRequestCount(t, &reqCount, before+2)
	waitForState(t, pool, "srv1", string(transport.StateFailed))

	deadline := time.Now().Add(2 * time.Second)
	var events []healthEvent
	for time.Now().Before(deadline) {
		events = log.snapshot()
		if len(events) >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(events) < 2 {
		t.Fatalf("observer fired %d time(s), want at least 2 (running, then failed)", len(events))
	}
	last := events[len(events)-1]
	if last.id != "srv1" {
		t.Errorf("last event id = %q, want %q", last.id, "srv1")
	}
	if last.previous != string(transport.StateRunning) {
		t.Errorf("last event previous = %q, want %q", last.previous, transport.StateRunning)
	}
	if last.current.State != string(transport.StateFailed) {
		t.Errorf("last event current.State = %q, want %q", last.current.State, transport.StateFailed)
	}
}
