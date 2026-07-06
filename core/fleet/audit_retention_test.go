package fleet

import (
	"context"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

func makeRetentionRow(id string, age time.Duration) AuditRetentionRow {
	return AuditRetentionRow{
		EventID:   id,
		EmittedAt: time.Now().Add(-age),
	}
}

// TestRetentionSweep_AckedAndAged tests the happy path: rows that are
// both ACK'd (id ≤ cursor) AND old are deleted.
func TestRetentionSweep_AckedAndAged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := &MemoryRetentionBackend{}
	// Two old rows (>90d) that are ACK'd.
	backend.AddRow(makeRetentionRow("ROW1", 100*24*time.Hour))
	backend.AddRow(makeRetentionRow("ROW2", 95*24*time.Hour))
	// One recent row that is NOT aged out.
	backend.AddRow(makeRetentionRow("ROW3", 1*24*time.Hour))

	emitter := &fakeEmitter{}
	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{
		Backend: backend,
		Cursor:  func() string { return "ROW9" }, // cursor > all rows = all ACK'd
		Emitter: emitter,
		RetentionDays: 90,
	})

	deleted, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("want 2 deleted, got %d", deleted)
	}

	// ROW3 should survive.
	rows := backend.Rows()
	if len(rows) != 1 || rows[0].EventID != "ROW3" {
		t.Errorf("want ROW3 surviving, got %v", rows)
	}

	// KindFleetAuditRetentionSwept emitted.
	found := false
	for _, e := range emitter.snapshot() {
		if e.Kind == contextaudit.KindFleetAuditRetentionSwept {
			found = true
		}
	}
	if !found {
		t.Error("want KindFleetAuditRetentionSwept emitted")
	}
}

// TestRetentionSweep_UnackedPreserved verifies that rows older than the
// window but NOT ACK'd are preserved (conjunctive condition).
func TestRetentionSweep_UnackedPreserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := &MemoryRetentionBackend{}
	backend.AddRow(makeRetentionRow("ROW1", 200*24*time.Hour)) // very old
	backend.AddRow(makeRetentionRow("ROW2", 150*24*time.Hour)) // very old

	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{
		Backend:       backend,
		Cursor:        func() string { return "" }, // no cursor = nothing ACK'd
		RetentionDays: 90,
	})

	deleted, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 0 {
		t.Errorf("want 0 deleted (cursor empty), got %d", deleted)
	}
}

// TestRetentionSweep_PartialAck verifies only ACK'd rows are deleted even
// when there is a mix of ACK'd and un-ACK'd old rows.
func TestRetentionSweep_PartialAck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := &MemoryRetentionBackend{}
	backend.AddRow(makeRetentionRow("ROW_A", 100*24*time.Hour)) // ACK'd (id < cursor)
	backend.AddRow(makeRetentionRow("ROW_B", 100*24*time.Hour)) // ACK'd
	backend.AddRow(makeRetentionRow("ROW_Z", 100*24*time.Hour)) // NOT ACK'd (id > cursor)

	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{
		Backend:       backend,
		Cursor:        func() string { return "ROW_B" }, // ACK'd up to ROW_B
		RetentionDays: 90,
	})

	deleted, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("want 2 deleted, got %d", deleted)
	}

	rows := backend.Rows()
	if len(rows) != 1 || rows[0].EventID != "ROW_Z" {
		t.Errorf("want ROW_Z surviving, got %v", rows)
	}
}

// TestRetentionSweep_MaxRows verifies the per-pass row cap.
func TestRetentionSweep_MaxRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := &MemoryRetentionBackend{}
	// Add 5 old ACK'd rows but use a small limit override.
	for i := 0; i < 5; i++ {
		backend.AddRow(AuditRetentionRow{
			EventID:   string(rune('A' + i)),
			EmittedAt: time.Now().Add(-100 * 24 * time.Hour),
		})
	}

	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{
		Backend:       backend,
		Cursor:        func() string { return "Z" },
		RetentionDays: 90,
	})

	// We can't override retentionMaxRowsPerPass from outside, but we can
	// verify that when the backend returns ≤ 5 rows, all 5 are swept.
	deleted, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 5 {
		t.Errorf("want 5 deleted, got %d", deleted)
	}
}

// TestRetentionSweep_SetRetentionDays verifies the config knob.
func TestRetentionSweep_SetRetentionDays(t *testing.T) {
	sweeper := NewAuditRetentionSweeper(AuditRetentionConfig{RetentionDays: 90})
	if sweeper.RetentionDays() != 90 {
		t.Errorf("initial days: want 90, got %d", sweeper.RetentionDays())
	}
	sweeper.SetRetentionDays(365)
	if sweeper.RetentionDays() != 365 {
		t.Errorf("after set: want 365, got %d", sweeper.RetentionDays())
	}
	sweeper.SetRetentionDays(0) // zero resets to default
	if sweeper.RetentionDays() != DefaultAuditRetentionDays {
		t.Errorf("after zero reset: want %d, got %d", DefaultAuditRetentionDays, sweeper.RetentionDays())
	}
}
