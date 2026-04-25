package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

type recordingEmitter struct {
	events []Event
}

func (r *recordingEmitter) Emit(_ context.Context, e Event) error {
	r.events = append(r.events, e)
	return nil
}

func TestEmit_NilEmitterIsNoOp(t *testing.T) {
	if err := Emit(context.Background(), nil, KindResolutionStarted, ResolutionStartedPayload{}, time.Now()); err != nil {
		t.Fatalf("nil emitter must be a no-op; got %v", err)
	}
}

func TestEmit_RoundTripPayload(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	payload := PackVerifiedPayload{
		Pack:       pack.PackRef{Name: "p", Version: "1", Layer: pack.LayerOrg, ContentHash: "sha256:x"},
		AnchorID:   "trust://acme/root",
		Algorithm:  "sigstore-bundle",
		CacheState: "fresh",
		GraceState: "none",
	}
	if err := Emit(context.Background(), em, KindPackVerified, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(em.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(em.events))
	}
	e := em.events[0]
	if e.Kind != KindPackVerified {
		t.Errorf("Kind = %q", e.Kind)
	}
	if !e.TS.Equal(now) {
		t.Errorf("TS = %v, want %v", e.TS, now)
	}
	var got PackVerifiedPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got.AnchorID != payload.AnchorID {
		t.Errorf("payload round-trip lost AnchorID")
	}
}
