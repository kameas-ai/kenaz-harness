package serve_test

// wp01_session_scoping_test.go pins the fix for served-mode-is-a-real-
// mode-01PMZ707 WP01 — the cross-session authorization leak.
//
// Before this WP, a served WebSocket client received EVERY session's
// tool:confirm-pending, elicit:pending, *:permission-pending and
// llm:stream-chunk frames, and could resolve confirmations and
// elicitations it never should have seen: streamSessions ignored
// params.id and frameFor forwarded every bus event verbatim. These
// tests exercise the REAL server (real HTTP + WS, real core.New-backed
// rpc.API) with TWO independent sessions and assert the isolation
// directly — "a fan-out test driving a fake bus and a fake API proves
// nothing about whether the real Confirm().ListPending scoping branch
// is reached" (CLAUDE.md blind spot #2, applied to this mission's own
// fixtures per plan.md check 5).
//
// AC-705 (frontend/src/components/chat/__tests__/ConfirmToolModal.test.ts
// requiring zero edits) is verified separately, in the frontend suite —
// nothing here touches Vue.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// assertNoFrame reads with a short deadline and requires the read to time
// out — i.e. that nothing arrives on ws within wait. Any frame at all
// (even an unrelated one) is a hard failure: for these tests, silence is
// the assertion.
func assertNoFrame(t *testing.T, ws *websocket.Conn, wait time.Duration, context string) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(wait))
	var f wsTestFrame
	err := websocket.JSON.Receive(ws, &f)
	if err == nil {
		t.Fatalf("%s: expected no frame within %s, but received event=%q data=%s", context, wait, f.Event, f.Data)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("%s: expected a read timeout (no frame), got a different error: %v", context, err)
	}
}

// ─── AC-701: tool:confirm-pending is scoped to the subscribed session ──────

// TestWP01_ConfirmPending_ScopedToSubscribedSession is AC-701's literal
// text: publish tool:confirm-pending for session A over the REAL event
// bus; A's connection receives it, B's receives zero frames.
//
// Falsify: revert frameFor's session check (or its processWideTopics /
// sessionIDOf plumbing) and the B-receives-nothing assertion fails.
func TestWP01_ConfirmPending_ScopedToSubscribedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	req := toolloop.ConfirmRequest{
		SessionID:   "sess-A",
		CallID:      "call-1",
		BatchID:     "batch-1",
		Server:      "filesystem",
		Tool:        "write_file",
		ArgsSummary: "2 arguments: content (string), path (string)",
	}
	api.EventBus().Publish(toolloop.TopicToolConfirmPending, req)

	f := readUntil(t, wsA, "tool:confirm-pending", 5*time.Second)
	var got toolloop.ConfirmRequest
	if err := json.Unmarshal(f.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != "sess-A" || got.CallID != "call-1" {
		t.Errorf("session A got the wrong payload: %+v", got)
	}

	// The whole point: session B's connection must see NOTHING for a
	// call parked under session A. Before this WP, frameFor forwarded
	// every bus event verbatim regardless of which session subscribed,
	// so B would have received this same frame — a session B never
	// asked about could see, and resolve, session A's parked tool call.
	assertNoFrame(t, wsB, 500*time.Millisecond, "session B (foreign session)")
}

// ─── AC-702: the permission + stream + fallback topics ─────────────────────

// TestWP01_PermissionAndStreamTopics_ScopedToSubscribedSession is AC-702
// for the four *:permission-pending topics plus llm:stream-chunk,
// llm:stream-closed and llm:fallback-attempted. Every one of these is an
// interactive gate or carries raw model output — leaking any of them
// cross-session is the same class of defect AC-701 covers for confirm.
func TestWP01_PermissionAndStreamTopics_ScopedToSubscribedSession(t *testing.T) {
	for _, tc := range []struct {
		topic   string
		payload any
	}{
		{rpc.TopicBashPermissionPending, rpc.FlatPermissionRequest{SessionID: "sess-A", RequestID: "r1", Family: "bash"}},
		{rpc.TopicFSPermissionPending, rpc.FlatPermissionRequest{SessionID: "sess-A", RequestID: "r2", Family: "fs"}},
		{rpc.TopicCredPermissionPending, rpc.FlatPermissionRequest{SessionID: "sess-A", RequestID: "r3", Family: "cred"}},
		{rpc.TopicToolPermissionPending, rpc.FlatPermissionRequest{SessionID: "sess-A", RequestID: "r4", Family: "tool"}},
		{"llm:stream-chunk", llmview.StreamChunkPayload{SubID: "s1", SessionID: "sess-A", Chunk: corellm.StreamEvent{Kind: corellm.StreamText, Text: "secret answer"}}},
		{"llm:stream-closed", llmview.StreamClosedPayload{SubID: "s1", SessionID: "sess-A", Reason: "completed"}},
		{rpc.TopicLLMFallbackAttempted, map[string]any{"session_id": "sess-A", "from_model": "x", "to_model": "y"}},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			api, baseURL, cancel := newChatHarness(t)
			defer cancel()

			wsA := dialChatWS(t, baseURL, "sess-A")
			defer wsA.Close() //nolint:errcheck
			wsB := dialChatWS(t, baseURL, "sess-B")
			defer wsB.Close() //nolint:errcheck

			api.EventBus().Publish(tc.topic, tc.payload)

			f := readUntil(t, wsA, tc.topic, 5*time.Second)
			if !strings.Contains(string(f.Data), "sess-A") {
				t.Errorf("session A frame did not carry the expected payload: %s", f.Data)
			}

			assertNoFrame(t, wsB, 500*time.Millisecond, "session B (foreign session), topic "+tc.topic)
		})
	}
}

// ─── AC-702: the elicit leg, through the REAL producer ─────────────────────

// TestWP01_ElicitPending_FrameForScopesLivePush is AC-702 for the
// elicit:pending topic's LIVE PUSH leg, i.e. frameFor's filter — the
// actual WP01 change. It publishes directly on the real EventBus (same
// technique TestWP01_PermissionAndStreamTopics_ScopedToSubscribedSession
// and the pre-existing TestServedChat_ForwardsInteractiveGateTopics use)
// rather than driving it through elicitview.API.OpenDialog.
//
// That is a deliberate choice, not a shortcut: OpenDialog's own publish()
// path (core/rpc/views/elicit/api.go's Emitter field) is wired in
// production to a bare rpc.WailsEmitter{} — NOT to the
// MultiEmitter(WailsEmitter{}, busEmitter) chain core/rpc/api.go builds
// for a.broker (core/rpc/api.go:1284, core/rpc/emitter.go's own doc:
// "the MultiEmitter's EventBus sink still delivers, which is the sink
// served mode actually reads"). A bare WailsEmitter is a no-op under the
// `serve` build tag (core/rpc/emitter_serve.go:22), so elicit:pending's
// LIVE announcement never reaches core/serve's EventBus in production
// TODAY, independent of this WP — confirmed by this file's first attempt
// at this test, which timed out waiting for the frame with a correctly-
// wired OpenDialog(toolloop.WithSessionID(...)) call. That is a real,
// separate, pre-existing wiring gap (elicit:pending is effectively DEAD
// for served mode's live-push leg; only the reconnect snapshot —
// TestWP01_ElicitPendingSnapshot_ForeignSessionSeesNothing and
// TestWS_SessionsStream_ElicitPendingSnapshot in server_test.go, both of
// which read the registry directly and are unaffected — ever reaches a
// served client), reported in this WP's commit message and final report
// per E-701/E-702's "record it, do not resolve it" pattern. Fixing it
// belongs to whichever WP owns core/rpc/api.go's elicitAPI emitter
// wiring, not this one.
//
// This test therefore verifies what WP01 actually changed — frameFor's
// session filter — using the wire shape OpenDialog's requestOf() would
// produce if its announcement reached the bus.
func TestWP01_ElicitPending_FrameForScopesLivePush(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	api.EventBus().Publish("elicit:pending", map[string]any{
		"request_id": "req-1",
		"session_id": "sess-A",
		"question":   "Proceed with deploy?",
		"kind":       "radio",
	})

	f := readUntil(t, wsA, "elicit:pending", 5*time.Second)
	if !strings.Contains(string(f.Data), "sess-A") {
		t.Errorf("session A frame did not carry the expected payload: %s", f.Data)
	}

	assertNoFrame(t, wsB, 500*time.Millisecond, "session B (foreign session), elicit:pending")
}

// ─── AC-703: Sessions_Stream requires params.id ─────────────────────────────

// TestWP01_SessionsStream_RequiresParamsID is AC-703: an absent or empty
// params.id is a hard refusal — an error frame and NO sessions:snapshot —
// never a fall-back to an unscoped, all-sessions stream. That fall-back
// was the defect this WP closes.
//
// Falsify: restore the global fall-back (streamSessions(ctx, ws) with no
// sessionID) and this goes red.
func TestWP01_SessionsStream_RequiresParamsID(t *testing.T) {
	cases := []struct {
		name         string
		params       any // nil means "params" key is omitted entirely
		wantAccepted bool
	}{
		{name: "absent params object", params: nil, wantAccepted: false},
		{name: "empty params object", params: map[string]any{}, wantAccepted: false},
		{name: "empty id", params: map[string]any{"id": ""}, wantAccepted: false},
		// Sanity check on the other side of the same boundary: a
		// non-empty (if unusual) id is accepted — the refusal is
		// specifically for absent/empty, not a stricter validation of
		// what a session id may look like.
		{name: "non-empty id is accepted", params: map[string]any{"id": " "}, wantAccepted: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, baseURL, cancel := newChatHarness(t)
			defer cancel()

			wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
			cfg, err := websocket.NewConfig(wsURL, "http://localhost")
			if err != nil {
				t.Fatalf("ws config: %v", err)
			}
			cfg.Header.Set("Authorization", "Bearer tok")
			ws, err := websocket.DialConfig(cfg)
			if err != nil {
				t.Fatalf("ws dial: %v", err)
			}
			defer ws.Close() //nolint:errcheck

			msg := map[string]any{"method": "Sessions_Stream"}
			if tc.params != nil {
				msg["params"] = tc.params
			}
			if err := websocket.JSON.Send(ws, msg); err != nil {
				t.Fatalf("ws send: %v", err)
			}

			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			var frame wsTestFrame
			if err := websocket.JSON.Receive(ws, &frame); err != nil {
				t.Fatalf("ws receive: %v", err)
			}

			if tc.wantAccepted {
				if frame.Event != "sessions:snapshot" {
					t.Errorf("expected sessions:snapshot, got event=%q error=%q", frame.Event, frame.Error)
				}
				return
			}

			if frame.Event == "sessions:snapshot" {
				t.Fatalf("server accepted the stream with no session id — this is the leak's fall-back path")
			}
			if frame.Error == "" {
				t.Fatalf("expected an error frame, got event=%q with no error", frame.Event)
			}
		})
	}
}

// ─── AC-704: both WS-reconnect snapshots are scoped ─────────────────────────

// TestWP01_ConfirmPendingSnapshot_ScopedOnReconnect is AC-704: the
// wsstream.go:292 tool:confirm-pending:snapshot frame, sent the instant a
// client connects, must reflect only the connecting session's parked
// calls — exercised through the REAL confirm.API.ListPending (via
// rpc.API.ConfirmBus(), test-support-only, added alongside this test;
// see its doc comment), not a fake bus. Blind spot #2: a fake bus proves
// nothing about whether the real scoping branch
// (core/rpc/views/confirm/api.go's sessionID == "" check) is reached with
// the connection's actual session id rather than "".
//
// Falsify: change wsstream.go's ListPending call back to
// s.api.Confirm().ListPending(ctx, "") and session B's snapshot check
// below goes red (B would see A's parked call).
func TestWP01_ConfirmPendingSnapshot_ScopedOnReconnect(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	const sessionA = "sess-A"
	req := toolloop.ConfirmRequest{
		SessionID:   sessionA,
		CallID:      "call-snap-1",
		BatchID:     "batch-snap-1",
		Server:      "filesystem",
		Tool:        "write_file",
		ArgsSummary: "1 argument: path (string)",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = api.ConfirmBus().Pending(context.Background(), req)
	}()

	// Poll until the call is really parked (Pending registers before it
	// blocks) through the SAME ConfirmAPI wsstream.go reads.
	deadline := time.Now().Add(2 * time.Second)
	for {
		pending, _ := api.Confirm().ListPending(context.Background(), sessionA)
		if len(pending) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parked call never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Session A's connect-time snapshot must contain it.
	wsA := dialChatWS(t, baseURL, sessionA)
	defer wsA.Close() //nolint:errcheck
	fA := readUntil(t, wsA, "tool:confirm-pending:snapshot", 3*time.Second)
	var gotA []toolloop.ConfirmRequest
	if err := json.Unmarshal(fA.Data, &gotA); err != nil {
		t.Fatalf("unmarshal session A snapshot: %v", err)
	}
	if len(gotA) != 1 || gotA[0].CallID != "call-snap-1" {
		t.Errorf("session A snapshot = %+v, want the parked call", gotA)
	}

	// Session B's connect-time sequence must NOT contain a
	// tool:confirm-pending:snapshot frame at all — wsstream.go only
	// writes one when ListPending returns a non-empty slice, and B has
	// nothing parked.
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck
	assertNoFrame(t, wsB, 500*time.Millisecond, "session B connect-time snapshot sequence")

	// Clean up: resolve so the parked goroutine returns.
	if err := api.Confirm().Resolve(context.Background(), sessionA, "call-snap-1", true, "", false); err != nil {
		t.Errorf("Resolve: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("ConfirmBus.Pending did not return after Resolve")
	}
}

// TestWP01_ElicitPendingSnapshot_ForeignSessionSeesNothing is AC-704's
// elicit sibling: a session with no pending ask must not see ANY
// elicit:pending:snapshot frame in its connect-time sequence, even
// though another session genuinely has one parked. Complements
// TestWS_SessionsStream_ElicitPendingSnapshot (server_test.go), which
// pins the positive case (the owning session DOES see it).
func TestWP01_ElicitPendingSnapshot_ForeignSessionSeesNothing(t *testing.T) {
	elicit, _, baseURL, cancel := newTestServerWithElicit(t, "tok")
	defer cancel()

	const sessionA = "sess-A"
	askDone := make(chan struct{})
	go func() {
		defer close(askDone)
		_, _ = elicit.OpenDialog(toolloop.WithSessionID(context.Background(), sessionA), askuserquestion.AskArgs{
			Question: "Proceed with deploy?",
			Kind:     askuserquestion.KindRadio,
			Options: []askuserquestion.QuestionOption{
				{Value: "yes", Label: "Yes"},
				{Value: "no", Label: "No"},
			},
		}.ToQuestion())
	}()

	deadline := time.Now().Add(2 * time.Second)
	var requestID string
	for {
		pending, _ := elicit.ListPendingForSession(context.Background(), sessionA)
		if len(pending) > 0 {
			requestID = pending[0].RequestID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending ask was not registered in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck
	assertNoFrame(t, wsB, 500*time.Millisecond, "session B connect-time snapshot sequence (elicit)")

	if err := elicit.SubmitAnswer(context.Background(), requestID, json.RawMessage(`"yes"`), false); err != nil {
		t.Errorf("SubmitAnswer: %v", err)
	}
	select {
	case <-askDone:
	case <-time.After(2 * time.Second):
		t.Error("OpenDialog did not return after SubmitAnswer")
	}
}

// ─── D-705: a topic with no session id fails closed ────────────────────────

// TestWP01_CostThresholdCrossed_NeverForwarded pins D-705's "fail closed"
// disposition for rpc.TopicCostThresholdCrossed: its payload
// (usage.ThresholdCrossedPayload) is a calendar-month account aggregate
// with no session id at all, unlike every other passthrough topic. A
// connected client must never receive it — not because the topic was
// dropped from the subscription (it stays subscribed, or
// TestPassthroughTopics_MatchServedStreamTopicsTS would fail), but
// because frameFor's sessionIDOf probe always returns ok=false for it and
// D-705 says an unscopable payload is not forwarded, not "forwarded to
// everyone because it's probably account-wide."
func TestWP01_CostThresholdCrossed_NeverForwarded(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL, "sess-A")
	defer ws.Close() //nolint:errcheck

	api.EventBus().Publish(rpc.TopicCostThresholdCrossed, map[string]any{
		"pct":           80,
		"monthTotalUsd": 20.0,
		"thresholdUsd":  25.0,
	})

	assertNoFrame(t, ws, 500*time.Millisecond, "cost.threshold.crossed (no session id on the payload)")
}
