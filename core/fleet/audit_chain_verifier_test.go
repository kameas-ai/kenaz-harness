package fleet

import (
	"crypto/sha256"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

func makeChainEvent(id string, prevHash [32]byte) contextaudit.TailEvent {
	payload := []byte(`{"chain":"test"}`)
	ph := sha256.Sum256(append([]byte(id), payload...))
	return contextaudit.TailEvent{
		ID:          id,
		Kind:        "chain.test",
		EmittedAt:   time.Now(),
		Payload:     payload,
		PayloadHash: ph,
		PrevHash:    prevHash,
	}
}

// buildChain creates a well-formed chain of n events.
func buildChain(n int) []contextaudit.TailEvent {
	events := make([]contextaudit.TailEvent, n)
	for i := 0; i < n; i++ {
		var prev [32]byte
		if i > 0 {
			prev = events[i-1].PayloadHash
		}
		events[i] = makeChainEvent(string(rune('A'+i)), prev)
	}
	return events
}

func TestBatchChainVerifier_ValidChain(t *testing.T) {
	events := buildChain(5)
	v := &BatchChainVerifier{}
	ok, broken := v.VerifyBatch(events)
	if !ok {
		t.Errorf("valid chain: want ok=true, got broken at %q", broken)
	}
}

func TestBatchChainVerifier_EmptyBatch(t *testing.T) {
	v := &BatchChainVerifier{}
	ok, broken := v.VerifyBatch(nil)
	if !ok {
		t.Errorf("empty batch: want ok=true, got broken at %q", broken)
	}
}

func TestBatchChainVerifier_BreakDetected(t *testing.T) {
	events := buildChain(4)
	// Corrupt the PrevHash of events[2].
	events[2].PrevHash = [32]byte{0xFF}

	v := &BatchChainVerifier{}
	ok, broken := v.VerifyBatch(events)
	if ok {
		t.Error("tampered chain: want ok=false")
	}
	if broken != events[2].ID {
		t.Errorf("broken at: want %q, got %q", events[2].ID, broken)
	}
}

func TestBatchChainVerifier_PredecessorMismatch(t *testing.T) {
	// Build a batch where the first event has a NON-zero PrevHash that
	// doesn't match the verifier's PredecessorHash.
	// This simulates a mid-session batch where the predecessor is wrong.
	predecessor := [32]byte{0x01, 0x02, 0x03}
	wrongPredecessor := [32]byte{0xAA}

	// events[0] has prev = predecessor (correct link), but we feed it
	// to a verifier that has a different PredecessorHash.
	e := makeChainEvent("MID", predecessor)

	v := &BatchChainVerifier{PredecessorHash: wrongPredecessor}
	ok, broken := v.VerifyBatch([]contextaudit.TailEvent{e})
	if ok {
		t.Error("predecessor mismatch: want ok=false")
	}
	if broken != "MID" {
		t.Errorf("broken at: want MID, got %q", broken)
	}
}

func TestBatchChainVerifier_ZeroPrevHashIsCheckpoint(t *testing.T) {
	// An event with PrevHash == [32]byte{} is a session-start / migration
	// checkpoint and is always accepted even if the predecessor is non-zero.
	v := &BatchChainVerifier{PredecessorHash: [32]byte{0xBB}}

	checkpoint := makeChainEvent("CP", [32]byte{}) // zero prev_hash
	ok, broken := v.VerifyBatch([]contextaudit.TailEvent{checkpoint})
	if !ok {
		t.Errorf("zero-prev checkpoint: want ok=true, got broken at %q", broken)
	}
}

func TestBatchChainVerifier_AdvancePredecessor(t *testing.T) {
	events := buildChain(3)
	v := &BatchChainVerifier{}
	ok, _ := v.VerifyBatch(events)
	if !ok {
		t.Fatal("expected valid chain")
	}
	v.AdvancePredecessor(events)

	// After advancing, the next batch's predecessor should equal the last
	// event's PayloadHash.
	if v.PredecessorHash != events[2].PayloadHash {
		t.Error("AdvancePredecessor did not set correct hash")
	}
}

func TestArchiverFlush_ChainBreakHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	poster := &stubPoster{}
	tr := &contextaudit.MemoryTailReader{}

	e1 := makeChainEvent("E1", [32]byte{})
	e2 := makeChainEvent("E2", e1.PayloadHash)
	// Corrupt e2's PrevHash to break the chain.
	e2.PrevHash = [32]byte{0xDE, 0xAD}
	tr.Append(e1)
	tr.Append(e2)

	emitter := &fakeEmitter{}
	verifier := &BatchChainVerifier{}
	archiver := NewAuditArchiver(AuditArchiverConfig{
		Poster:        poster,
		DataDir:       dir,
		Tail:          tr,
		Signer:        fakeSigner{},
		Verifier:      verifier,
		Emitter:       emitter,
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})

	_ = archiver.flushOnce(t.Context())

	// Archiver should be halted.
	if !archiver.ChainBreakDetected() {
		t.Error("want chain break detected")
	}

	// KindFleetAuditChainBreak should be emitted.
	found := false
	for _, e := range emitter.snapshot() {
		if e.Kind == contextaudit.KindFleetAuditChainBreak {
			found = true
		}
	}
	if !found {
		t.Error("want KindFleetAuditChainBreak emitted")
	}

	// No batch should have been posted.
	if len(poster.snapshot()) != 0 {
		t.Error("want no POST on chain-break")
	}
}

func TestArchiverSkipToID_ClearsChainBreak(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tr := &contextaudit.MemoryTailReader{}
	emitter := &fakeEmitter{}
	archiver := NewAuditArchiver(AuditArchiverConfig{
		DataDir:       dir,
		Tail:          tr,
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
		Emitter:       emitter,
	})
	archiver.chainErr.Store(true)

	if err := archiver.SkipToID(t.Context(), "SKIP01"); err != nil {
		t.Fatalf("SkipToID: %v", err)
	}

	if archiver.ChainBreakDetected() {
		t.Error("chain-break should be cleared after skip")
	}

	archiver.mu.RLock()
	cur := archiver.cursor
	archiver.mu.RUnlock()
	if cur != "SKIP01" {
		t.Errorf("cursor after skip: want SKIP01, got %q", cur)
	}

	found := false
	for _, e := range emitter.snapshot() {
		if e.Kind == contextaudit.KindFleetAuditChainSkipped {
			found = true
		}
	}
	if !found {
		t.Error("want KindFleetAuditChainSkipped emitted")
	}
}
