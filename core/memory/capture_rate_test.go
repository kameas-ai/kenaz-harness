package memory

// capture_rate_test.go — integration tests for CaptureRateTracker (PR #90, v0.5.4).
//
// Covers:
//   1. ChunksPerMinute is zero on a fresh tracker.
//   2. Writes recorded within the 60s window contribute to the count.
//   3. Writes recorded > 60s ago do NOT contribute.
//   4. Burst write: 100 concurrent RecordWrite calls produce a non-zero,
//      bounded ChunksPerMinute — verifies the ring-buffer mutex is correct
//      under parallel pressure (the race detector catches racy access).
//   5. EmbedderHealth state machine: ok → slow → error.
//   6. GlobalCaptureTracker is wired into Store.Add via a fresh store.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCaptureRateTracker_Zero asserts that a fresh tracker reports 0/min.
func TestCaptureRateTracker_Zero(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	snap := tr.Snapshot(time.Now().UTC())
	if snap.ChunksPerMinute != 0 {
		t.Errorf("ChunksPerMinute = %v, want 0 on fresh tracker", snap.ChunksPerMinute)
	}
	if snap.EmbedderHealth != "ok" {
		t.Errorf("EmbedderHealth = %q, want ok on fresh tracker", snap.EmbedderHealth)
	}
}

// TestCaptureRateTracker_CountsWithinWindow asserts that writes timestamped
// within the 60s window contribute to ChunksPerMinute.
func TestCaptureRateTracker_CountsWithinWindow(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		tr.RecordWrite(now.Add(-time.Duration(i) * time.Second))
	}
	snap := tr.Snapshot(now)
	if snap.ChunksPerMinute != 10 {
		t.Errorf("ChunksPerMinute = %v, want 10", snap.ChunksPerMinute)
	}
}

// TestCaptureRateTracker_ExcludesOldWrites asserts that writes older than
// 60 seconds do not contribute to ChunksPerMinute.
func TestCaptureRateTracker_ExcludesOldWrites(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	now := time.Now().UTC()

	// 5 writes 90 seconds ago (outside 60s window).
	for i := 0; i < 5; i++ {
		tr.RecordWrite(now.Add(-90 * time.Second))
	}
	// 3 writes 30 seconds ago (inside window).
	for i := 0; i < 3; i++ {
		tr.RecordWrite(now.Add(-30 * time.Second))
	}

	snap := tr.Snapshot(now)
	if snap.ChunksPerMinute != 3 {
		t.Errorf("ChunksPerMinute = %v, want 3 (old writes excluded)", snap.ChunksPerMinute)
	}
}

// TestCaptureRateTracker_BurstWrites_Race writes 100 chunks concurrently and
// asserts that:
//   - ChunksPerMinute is non-zero.
//   - ChunksPerMinute is ≤ min(total, captureRingSize) — the ring never
//     overflows its slot count.
//
// The -race flag (used by `go test -race`) will catch any mutex issues.
func TestCaptureRateTracker_BurstWrites_Race(t *testing.T) {
	t.Parallel()
	const n = 100
	var tr CaptureRateTracker
	now := time.Now().UTC()

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			tr.RecordWrite(now)
		}()
	}
	wg.Wait()

	snap := tr.Snapshot(now)
	if snap.ChunksPerMinute == 0 {
		t.Errorf("ChunksPerMinute = 0 after %d burst writes, want > 0", n)
	}
	maxExpected := float64(captureRingSize)
	if n < captureRingSize {
		maxExpected = float64(n)
	}
	if snap.ChunksPerMinute > maxExpected {
		t.Errorf("ChunksPerMinute = %v, want ≤ %v (ring buffer cap)", snap.ChunksPerMinute, maxExpected)
	}
}

// TestCaptureRateTracker_EmbedderHealth_Slow verifies that a recent slow
// embed call drives health to "slow".
func TestCaptureRateTracker_EmbedderHealth_Slow(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	tr.RecordEmbedCall(6 * time.Second) // > embedSlowThreshold (5s)
	snap := tr.Snapshot(time.Now().UTC())
	if snap.EmbedderHealth != "slow" {
		t.Errorf("EmbedderHealth = %q, want slow after 6s embed call", snap.EmbedderHealth)
	}
}

// TestCaptureRateTracker_EmbedderHealth_Error verifies that >= embedErrorThreshold
// errors in the last embedErrorWindow drive health to "error" (beats "slow").
func TestCaptureRateTracker_EmbedderHealth_Error(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	now := time.Now().UTC()

	// Also record a slow call — error should take precedence.
	tr.RecordEmbedCall(10 * time.Second)

	// Record 3 errors within the 5-minute window.
	for i := 0; i < embedErrorThreshold; i++ {
		tr.RecordEmbedError(now.Add(-time.Duration(i+1) * time.Minute))
	}
	snap := tr.Snapshot(now)
	if snap.EmbedderHealth != "error" {
		t.Errorf("EmbedderHealth = %q, want error after %d errors", snap.EmbedderHealth, embedErrorThreshold)
	}
	if snap.RecentErrorCount < embedErrorThreshold {
		t.Errorf("RecentErrorCount = %d, want ≥ %d", snap.RecentErrorCount, embedErrorThreshold)
	}
}

// TestCaptureRateTracker_EmbedderHealth_OldErrorsExpire verifies that errors
// older than embedErrorWindow (5 minutes) are excluded.
func TestCaptureRateTracker_EmbedderHealth_OldErrorsExpire(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	now := time.Now().UTC()

	// 5 errors recorded 10 minutes ago (outside the 5-minute window).
	for i := 0; i < 5; i++ {
		tr.RecordEmbedError(now.Add(-10 * time.Minute))
	}
	snap := tr.Snapshot(now)
	if snap.EmbedderHealth != "ok" {
		t.Errorf("EmbedderHealth = %q, want ok — old errors expired", snap.EmbedderHealth)
	}
	if snap.RecentErrorCount != 0 {
		t.Errorf("RecentErrorCount = %d, want 0 after expiry", snap.RecentErrorCount)
	}
}

// TestCaptureRateTracker_LastErrorAt_IsSet verifies that LastErrorAt is
// populated when there are recent errors, and is nil when there are no
// errors in the window.
func TestCaptureRateTracker_LastErrorAt_IsSet(t *testing.T) {
	t.Parallel()
	var tr CaptureRateTracker
	now := time.Now().UTC()

	// No errors yet — LastErrorAt must be nil.
	snap := tr.Snapshot(now)
	if snap.LastErrorAt != nil {
		t.Errorf("LastErrorAt = %v, want nil on fresh tracker", snap.LastErrorAt)
	}

	// Record 3 errors at different times within the window.
	t1 := now.Add(-3 * time.Minute)
	t2 := now.Add(-2 * time.Minute)
	t3 := now.Add(-1 * time.Minute)
	tr.RecordEmbedError(t1)
	tr.RecordEmbedError(t2)
	tr.RecordEmbedError(t3)

	snap = tr.Snapshot(now)
	if snap.LastErrorAt == nil {
		t.Fatal("LastErrorAt is nil after recording errors; want non-nil")
	}
	// RecentErrorCount must include all three.
	if snap.RecentErrorCount != 3 {
		t.Errorf("RecentErrorCount = %d, want 3", snap.RecentErrorCount)
	}
	// LastErrorAt must be one of the three timestamps (the implementation
	// returns the first one found in the ring walk — the most-recently
	// inserted, which is t3).
	wantLast := t3
	if !snap.LastErrorAt.Equal(wantLast) {
		t.Errorf("LastErrorAt = %v, want %v (most recently inserted error)", snap.LastErrorAt, wantLast)
	}
}

// TestStore_Add_RecordsGlobalCaptureTracker verifies that Store.Add triggers
// GlobalCaptureTracker().RecordWrite, so ChunksPerMinute becomes non-zero
// after real writes via the store (the "pill polls 5s" path in the frontend).
//
// We use a process-isolated tracker via a fresh CaptureRateTracker rather
// than the global singleton to avoid interference between parallel tests.
func TestStore_Add_WiresGlobalCaptureTracker(t *testing.T) {
	t.Parallel()

	// Fresh store in a temp directory.
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "mem.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	defer store.Close()

	// Use the per-test approach: inspect the global tracker BEFORE and AFTER
	// so we detect whether RecordWrite was called without disturbing other tests.
	// We capture a before-snapshot, do writes, then verify the delta.
	before := GlobalCaptureTracker().Snapshot(time.Now().UTC())

	ctx := context.Background()
	const writes = 5
	now := time.Now().UTC()
	for i := 0; i < writes; i++ {
		if err := store.Add(ctx, makeTestChunk(fmt.Sprintf("cap-%d", i), now)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	after := GlobalCaptureTracker().Snapshot(now)
	// The after.ChunksPerMinute should be ≥ before + writes (allowing for
	// other parallel tests that might have also written).
	if after.ChunksPerMinute < before.ChunksPerMinute+float64(writes) {
		t.Errorf("ChunksPerMinute delta = %v, want ≥ %d (global tracker wired)",
			after.ChunksPerMinute-before.ChunksPerMinute, writes)
	}
}

// makeTestChunk constructs a minimal chunk suitable for Store.Add.
func makeTestChunk(id string, at time.Time) Chunk {
	return Chunk{
		ID:        id,
		ScopeKind: ScopeKindSession,
		ScopeID:   "sess-test",
		Content:   fmt.Sprintf("content for %s", id),
		Embedding: []float32{1, 0, 0},
		CreatedAt: at,
	}
}
