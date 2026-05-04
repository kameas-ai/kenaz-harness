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

func TestEmit_RoundTripSessionAutoTitledPayload(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	payload := SessionAutoTitledPayload{
		SessionID:      "sess-auto-1",
		GeneratedTitle: "Rust basics",
		ModelUsed:      "claude-haiku-4-7",
		DurationMs:     1234,
		Trigger:        "first_turn",
		ErrorKind:      "",
	}
	if err := Emit(context.Background(), em, KindSessionAutoTitled, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(em.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(em.events))
	}
	e := em.events[0]
	if e.Kind != KindSessionAutoTitled {
		t.Errorf("Kind = %q, want %q", e.Kind, KindSessionAutoTitled)
	}
	var got SessionAutoTitledPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got != payload {
		t.Errorf("payload round-trip mismatch: got %+v, want %+v", got, payload)
	}
}

func TestEmit_RoundTripSessionAutoTitledPayload_FailurePath(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 4, 29, 13, 0, 0, 0, time.UTC)
	payload := SessionAutoTitledPayload{
		SessionID:  "sess-auto-2",
		ModelUsed:  "claude-haiku-4-7",
		DurationMs: 500,
		Trigger:    "first_turn",
		ErrorKind:  "provider_error",
	}
	if err := Emit(context.Background(), em, KindSessionAutoTitled, payload, now); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	e := em.events[0]
	var got SessionAutoTitledPayload
	if err := json.Unmarshal(e.Payload, &got); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if got.ErrorKind != "provider_error" {
		t.Errorf("ErrorKind = %q, want provider_error", got.ErrorKind)
	}
	if got.GeneratedTitle != "" {
		t.Errorf("GeneratedTitle = %q, want empty on failure path", got.GeneratedTitle)
	}
}

func TestEmit_RoundTripUpdateAttrs(t *testing.T) {
	// Six-kind round-trip for the auto-update mission (v0.4.0 WP06).
	// Verifies every Attrs payload survives Marshal/Unmarshal so the
	// audit consumer sees the fields the Service emitted.
	em := &recordingEmitter{}
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		kind    Kind
		payload any
	}{
		{"checked", KindUpdateChecked, UpdateCheckedAttrs{Channel: "stable", ResultVersion: "v0.4.0", Took: 42}},
		{"available", KindUpdateAvailable, UpdateAvailableAttrs{CurrentVersion: "v0.3.0", AvailableVersion: "v0.4.0", Channel: "stable"}},
		{"downloaded", KindUpdateDownloaded, UpdateDownloadedAttrs{Version: "v0.4.0", Bytes: 31_457_280, Sha256Match: true}},
		{"applied", KindUpdateApplied, UpdateAppliedAttrs{FromVersion: "v0.3.0", ToVersion: "v0.4.0", Platform: "darwin/arm64"}},
		{"skipped", KindUpdateSkipped, UpdateSkippedAttrs{Version: "v0.4.0", Reason: "user_clicked_skip"}},
		{"failed", KindUpdateFailed, UpdateFailedAttrs{Action: "download", ErrorClass: "sha_mismatch"}},
	}
	for _, c := range cases {
		em.events = nil
		if err := Emit(context.Background(), em, c.kind, c.payload, now); err != nil {
			t.Fatalf("Emit %s: %v", c.name, err)
		}
		if len(em.events) != 1 {
			t.Fatalf("emitted %d events, want 1", len(em.events))
		}
		e := em.events[0]
		if e.Kind != c.kind {
			t.Errorf("Kind = %q, want %q", e.Kind, c.kind)
		}
		if !e.TS.Equal(now) {
			t.Errorf("TS = %v, want %v", e.TS, now)
		}
		// Round-trip: the marshaled payload must contain the source
		// fields verbatim.
		raw, _ := json.Marshal(c.payload)
		if string(e.Payload) != string(raw) {
			t.Errorf("payload mismatch: got %s want %s", e.Payload, raw)
		}
		// Privacy invariant — none of the auto-update payload bytes
		// should contain a URL fragment.
		if bytesContains(e.Payload, "http://") || bytesContains(e.Payload, "https://") {
			t.Errorf("auto-update payload leaks a URL: %s", e.Payload)
		}
	}
}

// bytesContains is a tiny helper to keep the privacy assertion above
// readable without importing strings/bytes for one call.
func bytesContains(s json.RawMessage, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	b := []byte(sub)
	for i := 0; i+len(b) <= len(s); i++ {
		match := true
		for j := 0; j < len(b); j++ {
			if s[i+j] != b[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
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
