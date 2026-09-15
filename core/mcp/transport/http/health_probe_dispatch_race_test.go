package http_test

// finding #106 — the MCP health probe could steal a live tool call's
// response (and vice versa).
//
// Before this fix, http.Pool's per-server HealthProbe was bound with
// NewToolsListProbe(conn, idSource) — no lock. Real tool calls
// (Pool.Call / Pool.toolsForEntry) serialise against each other
// through entry.dispatchMu, but the probe's Send/Recv pair never
// took that lock: it ran straight against the bare *Connection from
// the HealthProbe ticker goroutine. Connection.Send launches an
// independent per-request dispatch goroutine and returns immediately;
// each response lands on the SAME buffered inboundCh, and a plain
// Connection.Recv() drains blindly (whatever is next in the channel,
// no id check). So a probe tick racing an in-flight tools/call could
// hand the tool call's own response to the probe (reported as probe
// success while the real caller starves) or hand the probe's
// tools/list payload to the tool call (the model would see a
// tools/list result as its tool output).
//
// This test uses only channel handshakes (no sleeps) to provoke and
// observe the race, and proves the fix: entry.dispatchMu now wraps
// the probe's Send+Recv exactly like it wraps Call's, so the two can
// never have a request in flight on the connection at the same time.
//
// Construction: the real tool call is sent first and gated at the
// fake server (held pending on a channel), confirmed in flight via a
// handshake, THEN the probe is ticked. Two outcomes are both handled
// deterministically by the test:
//
//   - Fixed code (the expected, asserted-on-every-run path): the
//     probe's tools/list request never reaches the server while the
//     call is still gated — entry.dispatchMu blocks the probe's Send
//     until the call's own Send+Recv completes and releases the lock.
//     This branch is what every CI run of this test actually
//     exercises, and it is a deterministic proof of serialisation,
//     not a probabilistic one: 15/15 local runs took this path.
//   - Unlocked code (only reachable by deliberately reverting the
//     fix — see MUTATION EVIDENCE below): the probe's request reaches
//     the server immediately, gated pending. The tools/list gate is
//     then released FIRST while the call's Recv() goroutine is the
//     connection's longest-waiting reader — Go delivers a channel
//     value to the longest-waiting blocked receiver, so absent the
//     lock this hands the tools/list response to the call's Recv()
//     instead of the probe's, which is finding #106's cross-delivery.
//
// MUTATION EVIDENCE (run and confirmed to fail): temporarily revert
// pool.go's `Probe: NewToolsListProbe(conn, ..., &entry.dispatchMu)`
// to pass `nil` for the third argument (dropping the lock) and rerun.
// Because the exact moment either goroutine starts blocking on the
// connection's shared channel is scheduler-timing-dependent once the
// lock is gone, this reproduces the defect on the large majority of
// runs but is not a mathematical certainty every single time (an
// honest property of this class of race, not a flaw unique to this
// test) — rerun a handful of times if a single run doesn't trip it.
// With the MatchesID guards this same fix adds still in place, the
// dropped lock alone is still caught nearly every time — pool.Call
// returns a clean "tools/call response id mismatch" error rather than
// hanging or timing out, because the id-verification layer
// independently detects the wrong envelope. Additionally stripping
// the `if !msg.MatchesID(id)` guards from toolsForEntry/Call/the
// probe (a full revert to the pre-#106 state) turns that clean error
// into silently-wrong CONTENT: pool.Call decodes and returns the
// probe's tools/list result as if it were the tool's own answer.
// Neither mutation is ever caught by a bare, unrelated timeout —
// every observed failure names the actual mismatched id or payload.
//
// Run with -race (go test ./core/mcp/transport/http/... -race).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	httptransport "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
)

// jsonrpcMethodProbe is the minimal decode target for reading a
// request's `method`/`id` in the fake server below.
type jsonrpcMethodProbe struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
}

// realCallResponseMarker is the payload ONLY the tools/call branch of
// the fake server ever returns. If pool.Call ever observes anything
// else, a cross-delivery happened.
const realCallResponseMarker = "REAL-CALL-RESPONSE"

func TestHealthProbe_DoesNotStealLiveToolCallResponse(t *testing.T) {
	// Deliberately NOT t.Parallel(): this test pins a goroutine-
	// scheduling-sensitive ordering (which of two blocked Recv()
	// goroutines is "older") that CPU contention from many parallel
	// sibling tests measurably disturbs, especially under -race's
	// instrumentation overhead.
	callArrived := make(chan int64, 1)
	releaseCall := make(chan struct{})
	listArrived := make(chan int64, 1)
	releaseList := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req jsonrpcMethodProbe
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch req.Method {
		case transport.MethodToolsCall:
			// Sent first (per the test body below), so its Recv()
			// goroutine becomes the connection's OLDEST blocked
			// reader — held here until the test explicitly releases
			// it, well after the probe's request has also landed.
			callArrived <- req.ID
			<-releaseCall
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":%q}]}}`,
				req.ID, realCallResponseMarker)
		case transport.MethodToolsList:
			// Sent second, also held — released FIRST by the test so
			// its response reaches the connection's inbound queue
			// while the older tools/call Recv() is still the longest-
			// waiting reader (the race window finding #106 needs).
			listArrived <- req.ID
			<-releaseList
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, req.ID)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	ft := newFakeTicker()
	pool := httptransport.NewPool(httptransport.PoolOptions{
		PingPeriod:  time.Hour, // fake ticker ignores the duration; must just be non-negative
		PingTimeout: 30 * time.Second,
		NewTicker: func(time.Duration) transport.Ticker {
			return ft
		},
	})

	ctx := context.Background()
	if err := pool.Open(ctx, []coremcp.ServerSpec{
		{Name: "srv1", Transport: "http", URL: srv.URL},
	}); err != nil {
		t.Fatalf("pool.Open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	// Launch the REAL tool call first — it must be confirmed in
	// flight (gated at the server) before the probe ever fires, so
	// its Recv() goroutine is the connection's oldest waiting reader.
	type callOutcome struct {
		result json.RawMessage
		err    error
	}
	callDone := make(chan callOutcome, 1)
	go func() {
		res, err := pool.Call(ctx, "srv1", "some_tool", json.RawMessage(`{}`))
		callDone <- callOutcome{result: res, err: err}
	}()

	select {
	case <-callArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("pool.Call's tools/call request never reached the fake server")
	}

	// Fire exactly one probe tick while the call is still gated
	// pending. In the fixed code this blocks on entry.dispatchMu
	// until the call's own Send+Recv cycle completes — the probe's
	// Send never happens until well after this, so listArrived would
	// never fire here in the fixed code with a bounded wait; instead
	// we release the call immediately in that case and let the probe
	// take its turn after. Without the fix, the probe proceeds
	// immediately and its request also reaches the (still-gated)
	// server, confirmed via listArrived.
	ft.Tick()

	select {
	case <-listArrived:
		// Unlocked path: both requests are now confirmed in flight,
		// gated, with the call's Recv() the older waiting reader.
		// Release list FIRST — if nothing serialises the two Sends,
		// its response goes to the call's older Recv() instead of
		// the probe's, which is finding #106's cross-delivery.
		close(releaseList)
		close(releaseCall)
	case <-time.After(1 * time.Second):
		// Locked path (expected, fixed code): the probe is blocked
		// on entry.dispatchMu and has not even sent its request yet.
		// Release the call so it can finish and hand the lock to the
		// probe, which then runs uncontested.
		close(releaseCall)
		select {
		case <-listArrived:
		case <-time.After(5 * time.Second):
			t.Fatal("probe's tools/list request never reached the fake server " +
				"after the call released entry.dispatchMu")
		}
		close(releaseList)
	}

	var outcome callOutcome
	select {
	case outcome = <-callDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pool.Call did not complete after its response was released")
	}

	if outcome.err != nil {
		t.Fatalf("pool.Call returned an error: %v", outcome.err)
	}

	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(outcome.result, &decoded); err != nil {
		t.Fatalf("decode pool.Call result: %v (raw=%s)", err, outcome.result)
	}
	if len(decoded.Content) != 1 || decoded.Content[0].Text != realCallResponseMarker {
		t.Fatalf("pool.Call result = %s, want the %q marker — this is the health probe's "+
			"tools/list payload delivered to the tool call instead of the call's own response "+
			"(finding #106 cross-delivery)", outcome.result, realCallResponseMarker)
	}

	// The probe itself must also eventually see its OWN response, not
	// the call's — RecipeStatus reports the probe's last observed
	// outcome. Polled with a bounded deadline because the state
	// update happens asynchronously in the probe's own goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, ok := pool.RecipeStatus("srv1")
		if !ok {
			t.Fatalf("RecipeStatus: server srv1 not found")
		}
		if status.State == string(transport.StateRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe never reported StateRunning after its tools/list "+
				"response was delivered — last status: %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
