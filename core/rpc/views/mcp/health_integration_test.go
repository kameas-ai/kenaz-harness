package mcp_test

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/dispatch"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	httptransport "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// TestHealthObserver_ProbeTripReachesSubscriber is AC-005b's Go half
// (connector-lifecycle-truth-01PMZ303 UNIT-8, spec.md §7): drives a
// REAL dispatch.Pool wired with a REAL http.Pool against a REAL
// httptest 401 server (spec.md test rule R-2 — a fake sub-pool proves
// nothing), wires dispatch.Pool.SetHealthObserver with the exact glue
// core/rpc/api.go installs (convert stdio.RecipeStatus ->
// mcp.HealthEntry, call PublishHealthChange), subscribes through
// mcp.API.SubscribeHealthChanges exactly as a frontend caller would,
// and asserts the subscriber's channel actually receives a HealthEntry
// once the probe trips.
//
// Before UNIT-8, TestPublishHealthChange_Delivers (health_test.go)
// only proved PublishHealthChange doesn't panic when called directly
// — it was never called from anywhere in production (spec.md §11
// R-8: "zero publishes"). This test proves the full chain a real
// probe trip actually drives.
//
// Mutation: remove dispatch.Pool.SetHealthObserver's forwarding call
// (or api.go's observer closure, when covered end-to-end) — this
// test must fail; TestPublishHealthChange_Delivers alone would not
// catch it.
func TestHealthObserver_ProbeTripReachesSubscriber(t *testing.T) {
	t.Parallel()

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

	ft := newFakeTicker()
	httpPool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod: 5 * time.Millisecond,
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})
	dispatchPool := dispatch.New(dispatch.Options{HTTP: httpPool})
	defer func() { _ = dispatchPool.Close(context.Background()) }()

	broker := &captureBroker{}
	api := mcp.NewAPI(mcp.WithSubscriber(broker), mcp.WithHealthPool(dispatchPool))

	// Mirrors core/rpc/api.go's wiring closure exactly: convert the
	// sub-pool's stdio.RecipeStatus to mcp.HealthEntry and publish.
	dispatchPool.SetHealthObserver(func(id, previousState string, current stdio.RecipeStatus) {
		api.PublishHealthChange(mcp.HealthEntry{
			ID:        current.ID,
			State:     current.State,
			LastError: current.LastError,
		})
	})

	subID, err := api.SubscribeHealthChanges(context.Background())
	if err != nil {
		t.Fatalf("SubscribeHealthChanges: %v", err)
	}
	if subID == "" {
		t.Fatal("SubscribeHealthChanges: empty id")
	}
	if broker.ch == nil {
		t.Fatal("broker never captured the subscribed channel")
	}

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "http", URL: srv.URL}
	if err := dispatchPool.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}

	// First tick: "" -> running. Proves the observer's whole chain is
	// live even before the trip.
	ft.Tick()
	waitForReqCount(t, &reqCount, 1)

	select {
	case ev := <-broker.ch:
		entry, ok := ev.(mcp.HealthEntry)
		if !ok {
			t.Fatalf("event type = %T, want mcp.HealthEntry", ev)
		}
		if entry.State != string(transport.StateRunning) {
			t.Errorf("first event State = %q, want %q", entry.State, transport.StateRunning)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial running event")
	}

	// --- Trip: two consecutive 401s.
	atomic.StoreInt32(&healthy, 0)
	before := atomic.LoadInt32(&reqCount)
	ft.Tick()
	waitForReqCount(t, &reqCount, before+1)
	ft.Tick()
	waitForReqCount(t, &reqCount, before+2)

	select {
	case ev := <-broker.ch:
		entry, ok := ev.(mcp.HealthEntry)
		if !ok {
			t.Fatalf("event type = %T, want mcp.HealthEntry", ev)
		}
		if entry.ID != "srv1" {
			t.Errorf("event ID = %q, want %q", entry.ID, "srv1")
		}
		if entry.State == string(transport.StateRunning) {
			t.Errorf("event State = %q, want non-running after trip", entry.State)
		}
		if entry.LastError == "" {
			t.Error("event LastError is empty, want the probe's error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PublishHealthChange to reach the subscriber after the trip")
	}
}

func waitForReqCount(t *testing.T, reqCount *int32, want int32) {
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

// captureBroker is a minimal mcp.Subscriber fake that captures the
// source channel Subscribe is given, so the test can read events off
// it exactly as the real desktop stream-broker delivery path would.
type captureBroker struct {
	ch <-chan any
}

func (c *captureBroker) Subscribe(_ context.Context, _, _ string, source <-chan any) (string, error) {
	c.ch = source
	return "health-sub-1", nil
}

func (c *captureBroker) Unsubscribe(_ string) error { return nil }

// fakeTicker mirrors core/mcp/transport/http/health_test.go's fake —
// deterministic cadence with no reliance on wall-clock progress.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 16)} }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}
func (t *fakeTicker) Tick()               { t.ch <- time.Now() }
