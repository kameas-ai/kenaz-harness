package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event"
	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	llmevents "github.com/sigil-tech/kaneaz-harness/core/llm/events"
)

// fakeEmitter is the smallest concrete event.Emitter that records
// Append inputs in arrival order. It exists in this test file so the
// SEAM 2 test does not depend on a concrete upstream emitter shipping
// before this wiring lands.
type fakeEmitter struct {
	mu     sync.Mutex
	inputs []event.AppendInput
}

func (f *fakeEmitter) Append(_ context.Context, in event.AppendInput) (event.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	return event.Event{
		EventID:   event.ULID("01HFAKE0000000000000000000"),
		EmitterID: in.EmitterID,
		Kind:      in.Kind,
		EmittedAt: time.Now(),
	}, nil
}

func (f *fakeEmitter) snapshot() []event.AppendInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]event.AppendInput, len(f.inputs))
	copy(out, f.inputs)
	return out
}

// TestEventLogSink_NilEmitterRejected pins the constructor's nil-guard.
// Wiring this seam without an emitter is a programming error, not a
// runtime no-op.
func TestEventLogSink_NilEmitterRejected(t *testing.T) {
	if _, err := llmevents.NewEventLogSink(nil); err == nil {
		t.Fatalf("NewEventLogSink(nil) returned nil error")
	}
}

// TestEventLogSink_RequestSubmittedFlows asserts the canonical SEAM 2
// contract: a connector RequestSubmitted call with a non-empty session
// id lands in the wrapped event.Emitter as a properly-shaped
// AppendInput carrying the kind, the session id, and the structured
// payload fields the connector built.
func TestEventLogSink_RequestSubmittedFlows(t *testing.T) {
	fe := &fakeEmitter{}
	sink, err := llmevents.NewEventLogSink(fe)
	if err != nil {
		t.Fatalf("NewEventLogSink: %v", err)
	}
	em := llmevents.New(sink)

	req := llm.GenerationRequest{
		SessionID: "01HFXY8B5VJ6T6T7AXJF9JT9F9",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentPart{llm.ContentPartFromText("hello")}},
		},
	}
	prof := llm.ProviderProfile{
		ID:    "anthropic-default",
		Kind:  "anthropic",
		Model: "claude-sonnet",
	}
	if err := em.RequestSubmitted(context.Background(), req, prof, "snap-123"); err != nil {
		t.Fatalf("RequestSubmitted: %v", err)
	}

	got := fe.snapshot()
	if len(got) != 1 {
		t.Fatalf("emitter received %d events, want 1", len(got))
	}
	in := got[0]
	if in.Kind != event.Kind(llmevents.KindRequestSubmitted) {
		t.Errorf("Kind=%q want %q", in.Kind, llmevents.KindRequestSubmitted)
	}
	if in.EmitterID != event.EmitterID("llm/connector") {
		t.Errorf("EmitterID=%q want llm/connector", in.EmitterID)
	}
	if in.SessionID == nil || string(*in.SessionID) != "01HFXY8B5VJ6T6T7AXJF9JT9F9" {
		t.Errorf("SessionID = %v, want pointer to ULID", in.SessionID)
	}
	payload, ok := in.Payload.(map[string]any)
	if !ok {
		t.Fatalf("Payload type=%T want map[string]any", in.Payload)
	}
	if payload["provider"] != "anthropic" {
		t.Errorf("payload provider=%v want anthropic", payload["provider"])
	}
	if payload["snapshot_id"] != "snap-123" {
		t.Errorf("payload snapshot_id=%v want snap-123", payload["snapshot_id"])
	}
}

// TestEventLogSink_HeadlessEventsHaveNilSession pins the seam's
// behaviour for events without a session id (preflight, capability
// gate). They must still flow, but with AppendInput.SessionID == nil
// so the upstream chain logic places them outside any session.
func TestEventLogSink_HeadlessEventsHaveNilSession(t *testing.T) {
	fe := &fakeEmitter{}
	sink, err := llmevents.NewEventLogSink(fe)
	if err != nil {
		t.Fatalf("NewEventLogSink: %v", err)
	}
	em := llmevents.New(sink)

	prof := llm.ProviderProfile{
		ID:    "anthropic-default",
		Kind:  "anthropic",
		Model: "claude-sonnet",
		Cred: llm.CredentialReference{
			Kind:    "env",
			Locator: "ANTHROPIC_API_KEY",
		},
	}
	if err := em.PreflightResolved(context.Background(), prof); err != nil {
		t.Fatalf("PreflightResolved: %v", err)
	}

	got := fe.snapshot()
	if len(got) != 1 {
		t.Fatalf("emitter received %d events, want 1", len(got))
	}
	if got[0].SessionID != nil {
		t.Errorf("expected nil SessionID for headless event, got %v", got[0].SessionID)
	}
	if got[0].Kind != event.Kind(llmevents.KindPreflightResolved) {
		t.Errorf("Kind=%q want %q", got[0].Kind, llmevents.KindPreflightResolved)
	}
}

// TestEventLogSink_AllKindsFlow exercises every connector-emitted kind
// against the sink and confirms the upstream Emitter sees one Append
// per kind. The point is structural: if a future connector method
// forgets to route through Emitter.emit (the chokepoint), this test
// catches the regression because the kind count drifts.
func TestEventLogSink_AllKindsFlow(t *testing.T) {
	fe := &fakeEmitter{}
	sink, err := llmevents.NewEventLogSink(fe)
	if err != nil {
		t.Fatalf("NewEventLogSink: %v", err)
	}
	em := llmevents.New(sink)

	req := llm.GenerationRequest{SessionID: "01HFXY8B5VJ6T6T7AXJF9JT9F9"}
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "m"}

	if err := em.RequestSubmitted(context.Background(), req, prof, ""); err != nil {
		t.Fatalf("RequestSubmitted: %v", err)
	}
	if err := em.PreflightResolved(context.Background(), prof); err != nil {
		t.Fatalf("PreflightResolved: %v", err)
	}
	if err := em.PreflightFailed(context.Background(), prof, errFake("boom")); err != nil {
		t.Fatalf("PreflightFailed: %v", err)
	}
	if err := em.CapabilityRejected(context.Background(), req, prof, []llm.Capability{"streaming"}); err != nil {
		t.Fatalf("CapabilityRejected: %v", err)
	}
	if err := em.RetryAttempted(context.Background(), req, prof, 1, 100, 120, "transient"); err != nil {
		t.Fatalf("RetryAttempted: %v", err)
	}
	if err := em.StreamChunk(context.Background(), req, prof, llm.StreamEvent{Kind: "delta", Text: "hi"}); err != nil {
		t.Fatalf("StreamChunk: %v", err)
	}
	if err := em.ResponseFinal(context.Background(), req, prof, llm.Response{}, 250); err != nil {
		t.Fatalf("ResponseFinal: %v", err)
	}
	if err := em.Cancelled(context.Background(), req, prof, "user-aborted"); err != nil {
		t.Fatalf("Cancelled: %v", err)
	}
	if err := em.Error(context.Background(), req, prof, errFake("non-transient")); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if err := em.PolicyDenied(context.Background(), req, prof, "policy-X"); err != nil {
		t.Fatalf("PolicyDenied: %v", err)
	}

	got := fe.snapshot()
	if len(got) != 10 {
		t.Fatalf("got %d events, want 10 (one per emitter method)", len(got))
	}
	wantKinds := []llmevents.Kind{
		llmevents.KindRequestSubmitted,
		llmevents.KindPreflightResolved,
		llmevents.KindPreflightFailed,
		llmevents.KindCapabilityRejected,
		llmevents.KindRetryAttempted,
		llmevents.KindStreamChunk,
		llmevents.KindResponseFinal,
		llmevents.KindCancelled,
		llmevents.KindError,
		llmevents.KindPolicyDenied,
	}
	for i, k := range wantKinds {
		if event.Kind(k) != got[i].Kind {
			t.Errorf("kind[%d]=%q want %q", i, got[i].Kind, k)
		}
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
