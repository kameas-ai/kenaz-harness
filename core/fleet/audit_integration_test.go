package fleet

// audit_integration_test.go — fleet-audit-archival-01NDFSEX13 WP07
//
// End-to-end test: event emission → batch POST → ACK → cursor advance →
// retention sweep deletes only ACK'd + aged rows, preserves unACK'd rows.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// auditIntegrationPoster records every POST and responds with 200 OK.
// race-safe via mu+snapshot.
type auditIntegrationPoster struct {
	mu     sync.Mutex
	bodies []auditBatchPayload
}

func (p *auditIntegrationPoster) Post(_ context.Context, _ string, _ string, body io.Reader) (*http.Response, error) {
	var batch auditBatchPayload
	if err := json.NewDecoder(body).Decode(&batch); err == nil {
		p.mu.Lock()
		p.bodies = append(p.bodies, batch)
		p.mu.Unlock()
	}
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(rec).Encode(map[string]string{"ok": "true"})
	return rec.Result(), nil
}

func (p *auditIntegrationPoster) snapshot() []auditBatchPayload {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]auditBatchPayload, len(p.bodies))
	copy(out, p.bodies)
	return out
}

// buildChainFor constructs n tail events forming a valid hash chain.
// Event 0 uses a zero PrevHash (checkpoint); subsequent events chain forward.
func buildChainFor(n int) []contextaudit.TailEvent {
	events := make([]contextaudit.TailEvent, n)
	var prevHash [32]byte
	for i := 0; i < n; i++ {
		// Use byte encoding to build deterministic IDs.
		id := string(rune('A'+i)) + "INTEG"
		e := makeTailEvent(id, prevHash)
		events[i] = e
		prevHash = e.PayloadHash
	}
	return events
}

// ── integration tests ─────────────────────────────────────────────────────────

// TestAuditArchival_EmitToACK_CursorAdvances verifies the full
// emit → batch → POST → cursor-advance path.
func TestAuditArchival_EmitToACK_CursorAdvances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	poster := &auditIntegrationPoster{}
	emitter := &fakeEmitter{}

	// Seed the tail with 5 events forming a valid hash chain.
	tr := &contextaudit.MemoryTailReader{}
	events := buildChainFor(5)
	for _, e := range events {
		tr.Append(e)
	}

	archiver := NewAuditArchiver(AuditArchiverConfig{
		Poster:        poster,
		DataDir:       dir,
		Tail:          tr,
		Signer:        fakeSigner{},
		Verifier:      &BatchChainVerifier{},
		Emitter:       emitter,
		CapCheck:      func() bool { return true },
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})

	ctx := context.Background()
	if err := archiver.flushOnce(ctx); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}

	// Exactly one POST with 5 events.
	batches := poster.snapshot()
	if len(batches) != 1 {
		t.Fatalf("want 1 batch, got %d", len(batches))
	}
	if len(batches[0].Events) != 5 {
		t.Errorf("want 5 events, got %d", len(batches[0].Events))
	}

	// Cursor advanced to last event.
	archiver.mu.RLock()
	cur := archiver.cursor
	archiver.mu.RUnlock()
	want := events[4].ID
	if cur != want {
		t.Errorf("cursor: want %q, got %q", want, cur)
	}

	// Persisted cursor on disk.
	saved, err := readAuditCursor(dir)
	if err != nil {
		t.Fatalf("readAuditCursor: %v", err)
	}
	if saved != want {
		t.Errorf("persisted cursor: want %q, got %q", want, saved)
	}

	// KindFleetAuditArchived emitted.
	emitted := emitter.snapshot()
	var found bool
	for _, e := range emitted {
		if e.Kind == contextaudit.KindFleetAuditArchived {
			found = true
		}
	}
	if !found {
		t.Error("want KindFleetAuditArchived in emitted events")
	}
}

// TestAuditArchival_RetentionSweep verifies that the sweeper deletes rows
// that are both ACK'd (id ≤ cursor) and aged past the retention window,
// while preserving rows that are not yet ACK'd or not yet aged.
func TestAuditArchival_RetentionSweep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	poster := &auditIntegrationPoster{}

	// Seed 3 events.
	tr := &contextaudit.MemoryTailReader{}
	events := buildChainFor(3)
	for _, e := range events {
		tr.Append(e)
	}

	archiver := NewAuditArchiver(AuditArchiverConfig{
		Poster:        poster,
		DataDir:       dir,
		Tail:          tr,
		Signer:        fakeSigner{},
		Verifier:      &BatchChainVerifier{},
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})

	ctx := context.Background()
	if err := archiver.flushOnce(ctx); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}

	// Cursor is now events[2].ID.
	archiver.mu.RLock()
	ackedCursor := archiver.cursor
	archiver.mu.RUnlock()

	// Build a MemoryRetentionBackend with rows emitted far in the past (aged),
	// plus one row emitted 1s ago (not yet aged even if ACK'd).
	backend := &MemoryRetentionBackend{}
	old := time.Now().Add(-100 * 24 * time.Hour)
	for _, e := range events {
		backend.AddRow(AuditRetentionRow{
			EventID:   e.ID,
			EmittedAt: old,
		})
	}
	backend.AddRow(AuditRetentionRow{
		EventID:   "EV_RECENT",
		EmittedAt: time.Now().Add(-1 * time.Second),
	})

	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{
		Backend:       backend,
		Cursor:        func() string { return ackedCursor },
		RetentionDays: 7, // short window so old rows qualify
	})

	if _, err := sweeper.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	// All 3 ACK'd+aged rows should be deleted; EV_RECENT (not aged) preserved.
	rows := backend.Rows()
	if len(rows) != 1 {
		t.Errorf("want 1 row remaining (recent), got %d", len(rows))
	}
	if len(rows) > 0 && rows[0].EventID != "EV_RECENT" {
		t.Errorf("remaining row should be EV_RECENT, got %q", rows[0].EventID)
	}
}

// TestAuditArchival_ChainBreak_HaltsAndRecovers tests the full break → halt
// → skip-to-ID → resume cycle.
func TestAuditArchival_ChainBreak_HaltsAndRecovers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	poster := &auditIntegrationPoster{}
	emitter := &fakeEmitter{}

	// First event has valid zero-prev (checkpoint), second has wrong prev_hash.
	e1 := makeTailEvent("IBREAK001", [32]byte{})
	e2 := makeTailEvent("IBREAK002", [32]byte{0xFF}) // wrong prev → break
	tr := &contextaudit.MemoryTailReader{}
	tr.Append(e1)
	tr.Append(e2)

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

	ctx := context.Background()
	// flushOnce detects the chain break and halts.
	_ = archiver.flushOnce(ctx)

	if !archiver.ChainBreakDetected() {
		t.Fatal("want chain break detected")
	}

	// KindFleetAuditChainBreak should be emitted.
	emitted := emitter.snapshot()
	var foundBreak bool
	for _, e := range emitted {
		if e.Kind == contextaudit.KindFleetAuditChainBreak {
			foundBreak = true
		}
	}
	if !foundBreak {
		t.Error("want KindFleetAuditChainBreak emitted")
	}

	// Recovery: skip-to-ID clears the break flag.
	if err := archiver.SkipToID(ctx, "IBREAK002"); err != nil {
		t.Fatalf("SkipToID: %v", err)
	}
	if archiver.ChainBreakDetected() {
		t.Error("chain break should be cleared after SkipToID")
	}

	// KindFleetAuditChainSkipped should be emitted.
	emitted2 := emitter.snapshot()
	var foundSkip bool
	for _, e := range emitted2 {
		if e.Kind == contextaudit.KindFleetAuditChainSkipped {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Error("want KindFleetAuditChainSkipped emitted after recovery")
	}
}
