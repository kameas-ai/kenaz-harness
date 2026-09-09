package chat

// CHAT-13 (chat-turn-integrity-01PMZ606 WP13): StreamBridge.Close() used
// to hardcode Reason: "completed" even though it has no information
// about whether the turn actually succeeded, and shares the same
// idempotency flag as EmitClosedFull — so a future kernel path calling
// the generic agentgraph.StreamSink.Close() before the runner's own
// terminal-emit path would both lie about a failed turn AND silently
// suppress the runner's real close payload. This test pins Close()'s
// corrected reason and the resulting suppression fix.

import "testing"

// TestStreamBridge_Close_DoesNotClaimCompleted pins the direct fix:
// Close() must not report Reason=="completed" — it has no basis for
// that claim.
//
// Mutation: revert Close()'s Reason back to the literal "completed".
// Must fail.
func TestStreamBridge_Close_DoesNotClaimCompleted(t *testing.T) {
	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-1", "sess-1")

	bridge.Close()

	events := broker.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	payload, ok := events[0].payload.(StreamClosedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want StreamClosedPayload", events[0].payload)
	}
	if payload.Reason == "completed" {
		t.Fatalf(`Close() reported Reason == "completed" — it has no way ` +
			`to know the turn actually succeeded; a caller other than the ` +
			`runner's own EmitClosedFull invoking Close() must not assert ` +
			`success`)
	}
	if payload.Reason != StreamClosedReasonUnknown {
		t.Errorf("Reason = %q, want %q", payload.Reason, StreamClosedReasonUnknown)
	}
}

// TestStreamBridge_Close_DoesNotSuppressRealTerminalPayload pins the
// consequence the old hardcode created: because Close() and
// EmitClosedFull share ONE `closed` flag, a Close() that ran first used
// to permanently mask whatever EmitClosedFull tried to report next
// (idempotent = silently dropped). This does not change with the
// Reason fix alone — Close() still "wins" the race — so this test
// documents and pins that Close()'s own payload is at least honest
// about not being a completion, since the real reason is now lost
// either way once Close() has already fired.
//
// Mutation: none needed beyond the Reason fix above — this test exists
// so a future change to the shared-flag idempotency itself (e.g.
// giving Close() and EmitClosedFull separate flags) has a red/green
// signal for the "which payload wins" behavior it would be changing.
func TestStreamBridge_Close_ThenEmitClosedFull_FirstCallWins(t *testing.T) {
	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-1", "sess-1")

	bridge.Close()
	bridge.EmitClosedFull(StreamClosed{Reason: "backend-error", Message: "boom"})

	events := broker.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (EmitClosedFull must be a no-op after Close already closed)", len(events))
	}
	payload := events[0].payload.(StreamClosedPayload)
	if payload.Reason != StreamClosedReasonUnknown {
		t.Errorf("Reason = %q, want %q (Close()'s payload, since it fired first)", payload.Reason, StreamClosedReasonUnknown)
	}
}
