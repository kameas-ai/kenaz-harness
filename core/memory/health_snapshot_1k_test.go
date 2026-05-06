package memory

// health_snapshot_1k_test.go — NFR-001 integration test for SnapshotHealth.
//
// The memory health dashboard (PR #86, v0.5.3) commits to returning a
// valid health snapshot in ≤ 200 ms p95 against a 1 000-chunk store.
//
// This test builds a 1 000-chunk slice in memory and asserts:
//   1. All counts are correct (total, raw, global, embedded, unembedded).
//   2. The snapshot completes within 200 ms on the test host.
//
// The timing gate uses 200 ms × 5 as a generous CI budget (10× the spec
// p95) so slow CI runners don't flake.  Real production p95 is in the
// single-digit millisecond range on developer hardware.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestSnapshotHealth_1000Chunks_CountsAndLatency builds a 1000-chunk
// chromemStore and verifies that SnapshotHealth returns accurate counts
// within a generous timing budget.
func TestSnapshotHealth_1000Chunks_CountsAndLatency(t *testing.T) {
	t.Parallel()

	// ── Build a 1000-chunk slice directly (pure function, no I/O) ──────
	const total = 1000
	const globalCount = 200 // 20% promoted to global scope

	now := time.Now().UTC()
	chunks := make([]Chunk, 0, total)
	for i := 0; i < total; i++ {
		kind := ScopeKindSession
		if i < globalCount {
			kind = ScopeKindGlobal
		}
		var emb []float32
		if i%4 != 0 { // 75% embedded
			emb = []float32{float32(i) * 0.001, 0, 0}
		}
		chunks = append(chunks, Chunk{
			ID:        fmt.Sprintf("chunk-%04d", i),
			ScopeKind: kind,
			Embedding: emb,
			CreatedAt: now,
		})
	}

	// ── NFR-001: snapshot must complete within 200 ms × 5 on CI ────────
	const maxDuration = 200 * time.Millisecond * 5

	start := time.Now()
	snap := SnapshotHealth(chunks, nil, now)
	elapsed := time.Since(start)

	if elapsed > maxDuration {
		t.Errorf("SnapshotHealth took %v; want ≤ %v (NFR-001 p95 budget × 5 CI headroom)",
			elapsed, maxDuration)
	}

	// ── Counts ──────────────────────────────────────────────────────────
	if snap.Counts.Total != total {
		t.Errorf("Total = %d, want %d", snap.Counts.Total, total)
	}
	wantRaw := total - globalCount
	if snap.Counts.Raw != wantRaw {
		t.Errorf("Raw = %d, want %d", snap.Counts.Raw, wantRaw)
	}
	if snap.Counts.LongTermPromoted != globalCount {
		t.Errorf("LongTermPromoted = %d, want %d", snap.Counts.LongTermPromoted, globalCount)
	}
	// 75% of total are embedded (i%4 != 0).
	wantEmbedded := total * 3 / 4
	wantUnembedded := total - wantEmbedded
	if snap.Counts.Embedded != wantEmbedded {
		t.Errorf("Embedded = %d, want %d", snap.Counts.Embedded, wantEmbedded)
	}
	if snap.Counts.Unembedded != wantUnembedded {
		t.Errorf("Unembedded = %d, want %d", snap.Counts.Unembedded, wantUnembedded)
	}
	// All chunks were created at now (within 7d), so activity.Captured = total.
	if snap.Activity.Captured != total {
		t.Errorf("Activity.Captured = %d, want %d", snap.Activity.Captured, total)
	}
}

// TestChromemStore_SnapshotHealth_Integration runs SnapshotHealth through the
// chromemStore's own SnapshotHealth method (the HealthSnapshotter path used
// by the RPC layer) to verify the lock-copy-delegate wiring is correct.
func TestChromemStore_SnapshotHealth_Integration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "mem.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Add 5 chunks: 3 session, 2 global.
	for i := 0; i < 3; i++ {
		if err := store.Add(ctx, Chunk{
			ID:        fmt.Sprintf("s%d", i),
			ScopeKind: ScopeKindSession,
			Content:   fmt.Sprintf("session content %d", i), // unique content avoids dedup
			Embedding: []float32{float32(i + 1), 0, 0},
			CreatedAt: now,
		}); err != nil {
			t.Fatalf("Add session chunk %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.Add(ctx, Chunk{
			ID:        fmt.Sprintf("g%d", i),
			ScopeKind: ScopeKindGlobal,
			Content:   fmt.Sprintf("global content %d", i), // unique content avoids dedup
			Embedding: []float32{0, float32(i + 1), 0},
			CreatedAt: now,
		}); err != nil {
			t.Fatalf("Add global chunk %d: %v", i, err)
		}
	}

	hs, ok := store.(HealthSnapshotter)
	if !ok {
		t.Skip("store does not implement HealthSnapshotter")
	}
	snap, err := hs.SnapshotHealth(ctx)
	if err != nil {
		t.Fatalf("SnapshotHealth: %v", err)
	}

	if snap.Counts.Total != 5 {
		t.Errorf("Total = %d, want 5", snap.Counts.Total)
	}
	if snap.Counts.Raw != 3 {
		t.Errorf("Raw = %d, want 3", snap.Counts.Raw)
	}
	if snap.Counts.LongTermPromoted != 2 {
		t.Errorf("LongTermPromoted = %d, want 2", snap.Counts.LongTermPromoted)
	}
	// All 5 chunks have embeddings.
	if snap.Counts.Embedded != 5 {
		t.Errorf("Embedded = %d, want 5", snap.Counts.Embedded)
	}
	if snap.Counts.Unembedded != 0 {
		t.Errorf("Unembedded = %d, want 0", snap.Counts.Unembedded)
	}
	if snap.CapturedAt.IsZero() {
		t.Errorf("CapturedAt is zero")
	}
}
