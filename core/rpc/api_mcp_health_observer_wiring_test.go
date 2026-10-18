package rpc

// api_mcp_health_observer_wiring_test.go — PR #336 review MUST FIX 3.
//
// The reviewer mutated the REAL production wiring site —
// core/rpc/api.go's `a.dispatchPool.SetHealthObserver(func(id,
// previousState string, current stdio.RecipeStatus) { ... })` closure,
// installed inside New() around api.go:2480 — and the entire
// core/rpc/... suite (53 packages) still passed. The PR's cited proof,
// TestHealthObserver_ProbeTripReachesSubscriber
// (core/rpc/views/mcp/health_integration_test.go), hand-reconstructs
// that same closure inline against a `captureBroker` fake
// (mcp.WithSubscriber(broker), NOT the real *rpc.StreamBroker rpc.New()
// builds) — it proves the closure's LOGIC is correct, not that api.go
// actually installs it on the real dispatch pool rpc.New() constructs.
//
// This test drives a real probe trip through the stack exactly as
// rpc.New() builds it: a real core.New chassis, New(c, ...) with no
// production behaviour changed (the one override below —
// mcpHTTPPoolOptions — swaps only the HTTP sub-pool's ping ticker so
// the test does not wait on the real 30s transport.DefaultPingPeriod;
// see options.mcpHTTPPoolOptions's doc comment), a real httptest server
// standing in for the remote MCP connector, and a real subscription on
// api.EventBus() — the exact sink core/serve's WS layer and any other
// production consumer reads from (busEmitter, wired at
// `a.broker = NewStreamBroker(NewMultiEmitter(WailsEmitter{},
// &busEmitter{bus: a.eventBus}))`, api.go ~:1524).
//
// Mutation proof (see this file's companion report — the fix commit
// message records the actual `go test` output): commenting out
// api.go:2480's `mcpImpl.PublishHealthChange(entry)` call turns this
// test red; TestHealthObserver_ProbeTripReachesSubscriber alone does
// not catch that mutation, because it never calls New() at all.
import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	mcphttp "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// healthWiringFakeTicker mirrors
// core/rpc/views/mcp/health_integration_test.go's fakeTicker — a
// manually-driven transport.Ticker so the probe cadence does not depend
// on wall-clock progress.
type healthWiringFakeTicker struct{ ch chan time.Time }

func newHealthWiringFakeTicker() *healthWiringFakeTicker {
	return &healthWiringFakeTicker{ch: make(chan time.Time, 16)}
}
func (t *healthWiringFakeTicker) C() <-chan time.Time { return t.ch }
func (t *healthWiringFakeTicker) Stop()               {}
func (t *healthWiringFakeTicker) Tick()               { t.ch <- time.Now() }

// waitForHealthWiringReqCount polls reqCount until it reaches want or the
// 2s deadline expires.
func waitForHealthWiringReqCount(t *testing.T, reqCount *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if atomic.LoadInt32(reqCount) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("request count never reached %d (stuck at %d)", want, atomic.LoadInt32(reqCount))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestSetHealthObserver_RealChassis_ProbeTripReachesEventBus is MUST
// FIX 3's regression test: it builds the REAL rpc.New() chassis (not a
// hand-reconstructed glue closure) and asserts a real probe trip
// through the real dispatch pool reaches a real EventBus subscriber —
// the exact path a served-mode WS client (or any other future
// api.EventBus() consumer) depends on.
func TestSetHealthObserver_RealChassis_ProbeTripReachesEventBus(t *testing.T) {
	var reqCount int32
	var healthy int32 = 1
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(&reqCount, 1)
		if atomic.LoadInt32(&healthy) == 0 {
			w.WriteHeader(stdhttp.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer srv.Close()

	ft := newHealthWiringFakeTicker()

	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	// The ONLY departure from production construction: a fast, manually
	// driven ticker on the HTTP MCP sub-pool so this test does not wait
	// on the real 30s transport.DefaultPingPeriod. Every other line of
	// New()'s construction — including the api.go:2480
	// SetHealthObserver closure this test exists to cover — runs
	// unmodified.
	api := New(c, func(o *options) {
		o.mcpHTTPPoolOptions = &mcphttp.PoolOptions{
			NewTicker: func(time.Duration) transport.Ticker { return ft },
		}
	})
	t.Cleanup(api.Shutdown)

	if api.dispatchPool == nil {
		t.Fatal("api.dispatchPool is nil — rpc.New() did not wire a dispatch pool; this test cannot exercise the real observer chain")
	}

	// PublishHealthChange only fans out to subscribers registered through
	// SubscribeHealthChanges (a.healthSubs) — it does not publish to
	// api.EventBus() directly. Driving THIS call, against the real
	// mcp.MCPAPI returned by api.MCP() (not a captureBroker fake), is
	// what exercises the real *rpc.StreamBroker.Subscribe path: the
	// goroutine it spawns is what actually calls
	// MultiEmitter.Emit -> busEmitter.Emit -> a.eventBus.Publish, which
	// the api.EventBus() subscription below observes.
	if _, err := api.MCP().SubscribeHealthChanges(context.Background()); err != nil {
		t.Fatalf("SubscribeHealthChanges: %v", err)
	}

	sub, cancelSub := api.EventBus().Subscribe(4, mcpview.TopicMCPHealthChanged)
	defer cancelSub()

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := api.dispatchPool.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}

	// First tick: "" -> running. Draining this first confirms the whole
	// chain is live even before the trip, and gives the trip assertion
	// below an unambiguous next event to read.
	ft.Tick()
	waitForHealthWiringReqCount(t, &reqCount, 1)

	select {
	case ev := <-sub:
		entry, ok := ev.Payload.(mcpview.HealthEntry)
		if !ok {
			t.Fatalf("initial event payload type = %T, want mcpview.HealthEntry", ev.Payload)
		}
		if entry.State != "running" {
			t.Errorf("initial event State = %q, want %q", entry.State, "running")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial running event on api.EventBus() — " +
			"the real SetHealthObserver closure at api.go:2480 never reached the bus")
	}

	// Trip: two consecutive 401s.
	atomic.StoreInt32(&healthy, 0)
	before := atomic.LoadInt32(&reqCount)
	ft.Tick()
	waitForHealthWiringReqCount(t, &reqCount, before+1)
	ft.Tick()
	waitForHealthWiringReqCount(t, &reqCount, before+2)

	select {
	case ev := <-sub:
		entry, ok := ev.Payload.(mcpview.HealthEntry)
		if !ok {
			t.Fatalf("tripped event payload type = %T, want mcpview.HealthEntry", ev.Payload)
		}
		if entry.ID != "srv1" {
			t.Errorf("entry.ID = %q, want %q", entry.ID, "srv1")
		}
		if entry.State == "running" {
			t.Errorf("entry.State = %q, want non-running after trip", entry.State)
		}
		if entry.LastError == "" {
			t.Error("entry.LastError is empty, want the probe's error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the tripped event on api.EventBus() — " +
			"the real SetHealthObserver closure at api.go:2480 never reached the bus after the trip")
	}
}
