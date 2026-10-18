package sse_test

import (
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/sse"
)

// connector-lifecycle-truth-01PMZ303 UNIT-7 (spec.md §1.4, FR-005,
// AC-005), sse half — "mirror in sse". Before this unit sse.Pool had
// no health probe of any kind (see the pre-UNIT-7 CloseOne doc
// comment) and dispatch.Pool synthesised a permanent "running" for
// every sse-owned id. These tests drive sse.Pool's own
// RecipeStatus/AllRecipeStatuses directly against a REAL SSE fixture
// server (spec.md test rule R-2) to prove the accessor is honest.

// fakeTicker is a transport.Ticker the test drives manually — mirrors
// core/mcp/transport/http/health_test.go's fakeTicker for the same
// reason: deterministic cadence with no reliance on wall-clock
// progress.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 16)} }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}
func (t *fakeTicker) Tick()               { t.ch <- time.Now() }

// flippableSSEServer is a fakeSSEServer variant (see connection_test.go
// for the shared protocol shape) whose POST /rpc handler answers
// tools/list with a real success event while healthy != 0, and with
// HTTP 401 (no SSE event pushed) otherwise — the same "flip the
// response, prove the transition" shape status_test.go uses for the
// http transport.
type flippableSSEServer struct {
	srv      *httptest.Server
	outCh    chan []byte
	reqCount int32
	healthy  int32
}

func newFlippableSSEServer(t *testing.T) *flippableSSEServer {
	t.Helper()
	f := &flippableSSEServer{
		outCh:   make(chan []byte, 32),
		healthy: 1,
	}
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/stream", f.handleStream)
	mux.HandleFunc("/rpc", f.handleRPC)
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *flippableSSEServer) streamURL() string { return f.srv.URL + "/stream" }
func (f *flippableSSEServer) postURL() string   { return f.srv.URL + "/rpc" }

func (f *flippableSSEServer) handleStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	flusher, ok := w.(stdhttp.Flusher)
	if !ok {
		stdhttp.Error(w, "streaming not supported", stdhttp.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(stdhttp.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-f.outCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (f *flippableSSEServer) handleRPC(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	atomic.AddInt32(&f.reqCount, 1)

	if atomic.LoadInt32(&f.healthy) == 0 {
		w.WriteHeader(stdhttp.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
		return
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	body, _ := readAll(r)
	_ = json.Unmarshal(body, &req)

	var resultJSON json.RawMessage
	switch req.Method {
	case "tools/list":
		resultJSON = jsonMust(map[string]any{
			"tools": []map[string]any{{"name": "echo"}},
		})
	default:
		resultJSON = jsonMust(map[string]any{"ok": true})
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultJSON),
	}
	event, err := json.Marshal(resp)
	if err != nil {
		stdhttp.Error(w, err.Error(), stdhttp.StatusInternalServerError)
		return
	}
	f.outCh <- event
	w.WriteHeader(stdhttp.StatusAccepted)
}

func readAll(r *stdhttp.Request) ([]byte, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

func waitForSSERequestCount(t *testing.T, f *flippableSSEServer, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if atomic.LoadInt32(&f.reqCount) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("request count never reached %d (stuck at %d) — the probe never ticked; this test proves nothing", want, atomic.LoadInt32(&f.reqCount))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitForSSEState(t *testing.T, pool *sse.Pool, id, want string) {
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

// TestPoolRecipeStatus_HonestState is AC-005 (FR-005), sse half: a
// healthy server reports "running" from the moment it opens (sse's
// Open does real network I/O, unlike http's — see health.go's type
// doc comment); a tripped probe (two consecutive 401s) reports a
// non-running state with a non-empty LastError at BOTH RecipeStatus
// and AllRecipeStatuses; recovery to 200 returns the state to
// "running".
func TestPoolRecipeStatus_HonestState(t *testing.T) {
	t.Parallel()
	fake := newFlippableSSEServer(t)
	t.Cleanup(fake.srv.Close)

	ft := newFakeTicker()
	pool := sse.NewPool(sse.PoolOptions{
		PingPeriod: 5 * time.Millisecond, // irrelevant: fakeTicker ignores its factory argument
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "sse", URL: fake.streamURL(), PostURL: fake.postURL()}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Open already performed a real GET (sse, unlike http, does
	// network I/O in Open) — RecipeStatus must be "running" even
	// before the first probe tick.
	rs, ok := pool.RecipeStatus("srv1")
	if !ok {
		t.Fatal("RecipeStatus: not found")
	}
	if rs.State != string(transport.StateRunning) {
		t.Errorf("initial State = %q, want %q (Open already succeeded)", rs.State, transport.StateRunning)
	}

	// --- Trip: flip to 401. Two consecutive failing ticks trip
	// OnFailure and must flip the derived state.
	atomic.StoreInt32(&fake.healthy, 0)
	before := atomic.LoadInt32(&fake.reqCount)
	ft.Tick()
	waitForSSERequestCount(t, fake, before+1)
	ft.Tick()
	waitForSSERequestCount(t, fake, before+2)

	waitForSSEState(t, pool, "srv1", string(transport.StateFailed))

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

	all := pool.AllRecipeStatuses()
	if len(all) != 1 {
		t.Fatalf("AllRecipeStatuses = %d entries, want 1", len(all))
	}
	if all[0].State == string(transport.StateRunning) {
		t.Errorf("AllRecipeStatuses[0].State = %q, want non-running", all[0].State)
	}

	// --- Recovery: flip back to 200; state returns to running.
	atomic.StoreInt32(&fake.healthy, 1)
	before = atomic.LoadInt32(&fake.reqCount)
	ft.Tick()
	waitForSSERequestCount(t, fake, before+1)

	waitForSSEState(t, pool, "srv1", string(transport.StateRunning))
}

// healthEvent and healthEventLog are a race-safe record of
// SetHealthObserver calls — snapshot() pattern per CLAUDE.md's
// race-safe test-fake discipline.
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

// TestPoolSetHealthObserver_FiresOnTrip is AC-005b's Go half, sse
// side: a probe trip must produce an observer call.
func TestPoolSetHealthObserver_FiresOnTrip(t *testing.T) {
	t.Parallel()
	fake := newFlippableSSEServer(t)
	t.Cleanup(fake.srv.Close)

	ft := newFakeTicker()
	pool := sse.NewPool(sse.PoolOptions{
		PingPeriod: 5 * time.Millisecond,
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	log := &healthEventLog{}
	pool.SetHealthObserver(log.record)

	spec := coremcp.ServerSpec{Name: "srv1", Transport: "sse", URL: fake.streamURL(), PostURL: fake.postURL()}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	atomic.StoreInt32(&fake.healthy, 0)
	before := atomic.LoadInt32(&fake.reqCount)
	ft.Tick()
	waitForSSERequestCount(t, fake, before+1)
	ft.Tick()
	waitForSSERequestCount(t, fake, before+2)
	waitForSSEState(t, pool, "srv1", string(transport.StateFailed))

	deadline := time.Now().Add(2 * time.Second)
	var events []healthEvent
	for time.Now().Before(deadline) {
		events = log.snapshot()
		if len(events) >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(events) < 1 {
		t.Fatal("observer never fired on trip")
	}
	last := events[len(events)-1]
	if last.id != "srv1" {
		t.Errorf("event id = %q, want %q", last.id, "srv1")
	}
	if last.previous != string(transport.StateRunning) {
		t.Errorf("event previous = %q, want %q", last.previous, transport.StateRunning)
	}
	if last.current.State != string(transport.StateFailed) {
		t.Errorf("event current.State = %q, want %q", last.current.State, transport.StateFailed)
	}
}
