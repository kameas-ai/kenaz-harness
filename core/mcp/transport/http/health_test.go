package http_test

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	httptransport "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
)

// fakeTicker is a transport.Ticker the test drives manually — mirrors
// core/mcp/transport/stdio/health_test.go's fakeTicker for the same reason:
// deterministic cadence with no reliance on wall-clock progress.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 16)} }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}
func (t *fakeTicker) Tick()               { t.ch <- time.Now() }

// TestHealthProbe_TripsOnHTTP401 executes spec.md §11 ★2 — "does the http
// HealthProbe actually trip on a 401?" — which the mission spec explicitly
// marks as READ, not RUN: OnFailure's wiring was traced from the contract
// (health.go:51-54) but never driven against a real 401 response. This test
// drives a real httptest.Server that always answers 401, opens a real
// http.Connection against it, binds NewToolsListProbe, and asserts OnFailure
// fires after exactly two consecutive ticks — the observation UNIT-0 owes
// before any later unit assumes it.
func TestHealthProbe_TripsOnHTTP401(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	conn := httptransport.NewConnection(httptransport.Spec{
		ID:         "test-401",
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	probeFn := httptransport.NewToolsListProbe(conn, nil)

	// First: confirm a single probe call against the 401 server returns a
	// non-nil error — the structural claim ★2 asks about, executed rather
	// than read off pushHTTPError's contract.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := probeFn(probeCtx)
	cancel()
	if err == nil {
		t.Fatal("want NewToolsListProbe to return an error against a 401 response, got nil")
	}
	t.Logf("single probe call against 401 server returned: %v", err)

	// Second: confirm HealthProbe.OnFailure actually fires after two
	// consecutive ticks — not just that the bound probe function errors.
	var (
		mu     sync.Mutex
		reason string
		fired  int
	)
	ft := newFakeTicker()
	probe := &httptransport.HealthProbe{
		Period: 10 * time.Millisecond,
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
		Probe: probeFn,
		OnFailure: func(r string) {
			mu.Lock()
			defer mu.Unlock()
			fired++
			reason = r
		},
	}
	probe.Start()
	t.Cleanup(probe.Stop)

	ft.Tick() // 1st failure
	ft.Tick() // 2nd failure — should trip OnFailure

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := fired
		mu.Unlock()
		if got >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if fired < 1 {
		t.Fatal("OnFailure did not fire after two consecutive 401 probe ticks")
	}
	t.Logf("OnFailure fired %d time(s), reason=%q", fired, reason)
}
