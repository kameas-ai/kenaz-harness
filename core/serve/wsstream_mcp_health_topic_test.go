package serve_test

// wsstream_mcp_health_topic_test.go — PR #336 review MUST FIX 1
// regression test.
//
// Before this fix, mcp:health-changed (mcpview.TopicMCPHealthChanged)
// was published onto the real process-wide EventBus by
// mcp.API.PublishHealthChange fine — production wires
// NewStreamBroker(NewMultiEmitter(WailsEmitter{}, &busEmitter{bus:
// a.eventBus})), so WailsEmitter no-ops under `serve` but busEmitter
// still delivers — but core/serve/wsstream.go's passthroughTopics (the
// served-mode WS forwarding allowlist) omitted it. A served WS
// connection is only subscribed to the bus for topics in that slice
// (subscribedTopics()), so the bus-level Publish reached zero
// connections: not "delivered but dropped by session-scoping", but
// never even received by the pump goroutine.
//
// A second, independent gap existed alongside it: mcp.HealthEntry (the
// payload) carries no session id, because a connector's health state
// belongs to the whole process, not to any one session's conversation.
// Even with the topic added to passthroughTopics, frameFor's D-705
// fail-closed session filter (wsstream.go) would have silently dropped
// every frame — sessionIDOf returns ok=false for a payload with no
// session_id/sessionId field, and D-705 says an unscopable payload is
// not forwarded to a matching-session-only guess. The topic needed a
// processWideTopics entry too, the same exemption TopicMigrationDriftDetected
// already has for the same reason (a process-wide signal, not a missing
// field).
//
// This test drives the REAL production seam end to end — a real
// core.New + rpc.New chassis, a real served.Server, a real WebSocket
// connection, and api.EventBus().Publish (the exact call
// mcp.API.PublishHealthChange makes) — and asserts the frame actually
// arrives. It fails on either gap alone: revert either the
// passthroughTopics/SERVED_STREAM_TOPICS entries or the
// processWideTopics entry and this test times out waiting for the
// frame instead of receiving it.

import (
	"encoding/json"
	"testing"
	"time"

	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// TestServedMCPHealthChanged_ReachesAnyConnectedSession pins the fix:
// a served WS client subscribed to session "sess-A" receives an
// mcp:health-changed frame even though the payload names no session at
// all — because connector health is process-wide infrastructure, not
// scoped to the session that happens to be open when it changes.
func TestServedMCPHealthChanged_ReachesAnyConnectedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL, "sess-A")
	defer ws.Close() //nolint:errcheck

	// Exactly what mcp.API.PublishHealthChange does downstream of
	// dispatch.Pool.SetHealthObserver's real production trip (see
	// core/rpc/api.go's SetHealthObserver closure) — a HealthEntry with
	// no session field, published on mcpview.TopicMCPHealthChanged.
	api.EventBus().Publish(mcpview.TopicMCPHealthChanged, mcpview.HealthEntry{
		ID:              "srv1",
		State:           "failed",
		LastError:       "two consecutive tools/list probe failures",
		RestartAttempts: 0,
	})

	frame := readUntil(t, ws, mcpview.TopicMCPHealthChanged, 2*time.Second)

	var entry mcpview.HealthEntry
	if err := json.Unmarshal(frame.Data, &entry); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", mcpview.TopicMCPHealthChanged, err, frame.Data)
	}
	if entry.ID != "srv1" {
		t.Errorf("entry.ID = %q, want %q", entry.ID, "srv1")
	}
	if entry.State != "failed" {
		t.Errorf("entry.State = %q, want %q", entry.State, "failed")
	}
}
