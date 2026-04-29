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

func TestEmit_RoundTripSessionCompactedPayload(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	payload := SessionCompactedPayload{
		SessionID:          "sess-1",
		AggressivenessTier: "balanced",
		ModelUsed:          "claude-haiku-4-7",
		TokensInSpan:       12000,
		TokensAfterSummary: 1500,
		CompressionRatio:   0.125,
	}
	if err := Emit(context.Background(), em, KindSessionCompacted, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(em.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(em.events))
	}
	e := em.events[0]
	if e.Kind != KindSessionCompacted {
		t.Errorf("Kind = %q, want %q", e.Kind, KindSessionCompacted)
	}
	var got SessionCompactedPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got != payload {
		t.Errorf("payload round-trip mismatch: got %+v, want %+v", got, payload)
	}
}

func TestEmit_RoundTripCompactionFailedPayload(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	payload := CompactionFailedPayload{
		SessionID:          "sess-2",
		AggressivenessTier: "aggressive",
		ModelUsed:          "claude-haiku-4-7",
		TokensInSpan:       240000,
		ErrorKind:          "model_too_small",
	}
	if err := Emit(context.Background(), em, KindCompactionFailed, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(em.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(em.events))
	}
	e := em.events[0]
	if e.Kind != KindCompactionFailed {
		t.Errorf("Kind = %q, want %q", e.Kind, KindCompactionFailed)
	}
	var got CompactionFailedPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got != payload {
		t.Errorf("payload round-trip mismatch: got %+v, want %+v", got, payload)
	}
}

func TestEmit_RoundTripCompactedOriginalsDeletedPayload(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	payload := CompactedOriginalsDeletedPayload{
		DeletedCount:     42,
		OldestArchivedAt: oldest,
		NewestArchivedAt: newest,
	}
	if err := Emit(context.Background(), em, KindCompactedOriginalsDeleted, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(em.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(em.events))
	}
	e := em.events[0]
	if e.Kind != KindCompactedOriginalsDeleted {
		t.Errorf("Kind = %q, want %q", e.Kind, KindCompactedOriginalsDeleted)
	}
	var got CompactedOriginalsDeletedPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got.DeletedCount != payload.DeletedCount {
		t.Errorf("DeletedCount = %d, want %d", got.DeletedCount, payload.DeletedCount)
	}
	if !got.OldestArchivedAt.Equal(payload.OldestArchivedAt) {
		t.Errorf("OldestArchivedAt = %v, want %v", got.OldestArchivedAt, payload.OldestArchivedAt)
	}
	if !got.NewestArchivedAt.Equal(payload.NewestArchivedAt) {
		t.Errorf("NewestArchivedAt = %v, want %v", got.NewestArchivedAt, payload.NewestArchivedAt)
	}
}
