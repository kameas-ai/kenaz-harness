package serve_test

// wsstream_gap_topics_test.go — served-topic-single-source, findings
// #62/#63. Regression tests for the five topics
// scripts/ci/allowlists/served-mode-topic-forwarding-gaps.txt froze
// (gate added on fix/z303-mcp-health-truth / PR #336, not yet on
// origin/main as of this change — see that allowlist's header): each had
// a real frontend useEventStream subscriber but was missing from
// core/serve/wsstream.go's passthroughTopics, so a served WS client
// never received it. All five are now wired.
//
// Each test drives a REAL production seam end to end — a real
// core.New + rpc.New chassis, a real served.Server, a real WebSocket
// connection, and api.EventBus().Publish (the exact call every real
// producer makes downstream of its own emitter) — and asserts the frame
// actually arrives, mirroring wsstream_mcp_health_topic_test.go's
// pattern for mcp:health-changed.
//
// Per-topic session-scoping disposition (see wsstream.go's
// passthroughTopics/processWideTopics comments for the full reasoning):
//
//   - contextbootstrap:progress   — process-wide (processWideTopics)
//   - elicit:deferred             — session-scoped (already had SessionID)
//   - elicit:deferred:answered    — session-scoped (SessionID field added
//     to DeferredAnsweredPayload in this change)
//   - fleet:lockdown:changed      — process-wide (processWideTopics)
//   - fleet:session:expired       — process-wide (processWideTopics)

import (
	"encoding/json"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
)

// ─── process-wide topics: delivered regardless of subscribed session ──────

// TestServedContextBootstrapProgress_ReachesAnyConnectedSession pins
// contextbootstrap:progress's process-wide disposition: a client
// subscribed to "sess-A" receives the frame even though
// contextbootstrap.RunStatus carries no session id field at all — the
// onboarding bootstrap engine runs at most one run process-wide, not
// scoped to any chat session.
func TestServedContextBootstrapProgress_ReachesAnyConnectedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL, "sess-A")
	defer ws.Close() //nolint:errcheck

	// Mirrors bootstrapProgressSink.Emit's payload shape
	// (core/rpc/contextbootstrap_wiring.go) using the raw map form so
	// this test does not need to import core/contextbootstrap just to
	// build a RunStatus — the wire shape (json field names) is what
	// matters for a forwarding test, not the Go struct.
	api.EventBus().Publish(rpc.TopicContextBootstrapProgress, map[string]any{
		"run_id":              "run-1",
		"phase":               "extraction",
		"total_nodes_written": 3,
	})

	frame := readUntil(t, ws, rpc.TopicContextBootstrapProgress, 2*time.Second)

	var status struct {
		RunID string `json:"run_id"`
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(frame.Data, &status); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", rpc.TopicContextBootstrapProgress, err, frame.Data)
	}
	if status.RunID != "run-1" {
		t.Errorf("status.RunID = %q, want %q", status.RunID, "run-1")
	}
	if status.Phase != "extraction" {
		t.Errorf("status.Phase = %q, want %q", status.Phase, "extraction")
	}
}

// TestServedFleetLockdownChanged_ReachesAnyConnectedSession pins
// fleet:lockdown:changed's process-wide disposition: LockdownChangedPayload
// carries no session id (a fleet lockdown affects the whole harness
// process), so it must reach every connected client regardless of which
// session it subscribed to.
func TestServedFleetLockdownChanged_ReachesAnyConnectedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL, "sess-A")
	defer ws.Close() //nolint:errcheck

	api.EventBus().Publish(corefleet.TopicFleetLockdownChanged, corefleet.LockdownChangedPayload{
		Active: true,
		Reason: "org policy",
	})

	frame := readUntil(t, ws, corefleet.TopicFleetLockdownChanged, 2*time.Second)

	var payload corefleet.LockdownChangedPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", corefleet.TopicFleetLockdownChanged, err, frame.Data)
	}
	if !payload.Active {
		t.Error("payload.Active = false, want true")
	}
	if payload.Reason != "org policy" {
		t.Errorf("payload.Reason = %q, want %q", payload.Reason, "org policy")
	}
}

// TestServedFleetSessionExpired_ReachesAnyConnectedSession pins
// fleet:session:expired's process-wide disposition. The allowlist's
// placeholder justification speculated this topic "plausibly carries a
// session id already" because of its name; verified false —
// SessionExpiredPayload (core/fleet/http.go) is {Reason} only, because
// the "session" here is the fleet control-plane AUTH session
// (access-token refresh failure), not a chat session.
func TestServedFleetSessionExpired_ReachesAnyConnectedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL, "sess-A")
	defer ws.Close() //nolint:errcheck

	api.EventBus().Publish(corefleet.TopicFleetSessionExpired, corefleet.SessionExpiredPayload{
		Reason: "refresh token rejected",
	})

	frame := readUntil(t, ws, corefleet.TopicFleetSessionExpired, 2*time.Second)

	var payload corefleet.SessionExpiredPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", corefleet.TopicFleetSessionExpired, err, frame.Data)
	}
	if payload.Reason != "refresh token rejected" {
		t.Errorf("payload.Reason = %q, want %q", payload.Reason, "refresh token rejected")
	}
}

// ─── session-scoped topics: D-705 fail-closed filter applies ──────────────

// TestServedElicitDeferred_ScopedToSubscribedSession pins
// elicit:deferred's session scoping: a client subscribed to "sess-A"
// receives a deferred ask registered for "sess-A", and a client
// subscribed to "sess-B" receives nothing for it — same D-705
// disposition as the already-forwarded elicit:pending, because both
// share the ElicitRequest wire shape (and its SessionID field).
func TestServedElicitDeferred_ScopedToSubscribedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	api.EventBus().Publish(elicitview.TopicElicitDeferred, elicitview.ElicitRequest{
		RequestID: "ask-1",
		SessionID: "sess-A",
		Question:  "Deploy now?",
		Kind:      "radio",
		Mode:      "deferred",
	})

	frame := readUntil(t, wsA, elicitview.TopicElicitDeferred, 2*time.Second)
	var req elicitview.ElicitRequest
	if err := json.Unmarshal(frame.Data, &req); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", elicitview.TopicElicitDeferred, err, frame.Data)
	}
	if req.RequestID != "ask-1" {
		t.Errorf("req.RequestID = %q, want %q", req.RequestID, "ask-1")
	}

	assertNoFrame(t, wsB, 300*time.Millisecond, "sess-B connection, elicit:deferred for sess-A")
}

// TestServedElicitDeferredAnswered_ScopedToSubscribedSession pins
// elicit:deferred:answered's session scoping. Before this change
// DeferredAnsweredPayload carried no session id at all; this test would
// have had no way to scope it and the topic would have needed a
// processWideTopics exemption instead (broadcasting one session's
// answered-deferred-ask pill dismissal to every connected client — a
// cross-session leak of the same shape D-705 exists to prevent). Fixed
// by adding SessionID to the payload (core/rpc/views/elicit/api.go
// AnswerDeferred) instead.
func TestServedElicitDeferredAnswered_ScopedToSubscribedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	api.EventBus().Publish(elicitview.TopicElicitDeferredAnswered, elicitview.DeferredAnsweredPayload{
		AskID:          "ask-1",
		SystemReminder: "the user answered: yes",
		SessionID:      "sess-A",
	})

	frame := readUntil(t, wsA, elicitview.TopicElicitDeferredAnswered, 2*time.Second)
	var payload elicitview.DeferredAnsweredPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", elicitview.TopicElicitDeferredAnswered, err, frame.Data)
	}
	if payload.AskID != "ask-1" {
		t.Errorf("payload.AskID = %q, want %q", payload.AskID, "ask-1")
	}

	assertNoFrame(t, wsB, 300*time.Millisecond, "sess-B connection, elicit:deferred:answered for sess-A")
}
