package memory

import (
	"testing"
	"time"
)

// fakeEmbedderForHealth satisfies the Embedder interface for health tests.
type fakeEmbedderForHealth struct {
	kind string
	dims int
}

func (f *fakeEmbedderForHealth) Kind() string    { return f.kind }
func (f *fakeEmbedderForHealth) Dimensions() int { return f.dims }
func (f *fakeEmbedderForHealth) Embed(_ interface{}, _ []string) ([][]float32, error) {
	return nil, nil
}

func TestSnapshotHealth_Empty(t *testing.T) {
	t.Parallel()
	snap := SnapshotHealth(nil, nil, time.Now().UTC())
	if snap.Counts.Total != 0 {
		t.Errorf("total = %d, want 0", snap.Counts.Total)
	}
	if snap.Counts.Embedded != 0 || snap.Counts.Unembedded != 0 {
		t.Errorf("embedded=%d unembedded=%d, want 0 0", snap.Counts.Embedded, snap.Counts.Unembedded)
	}
}

func TestSnapshotHealth_Counts(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	chunks := []Chunk{
		{ID: "s1", ScopeKind: ScopeKindSession, Embedding: []float32{1, 0}, CreatedAt: now},
		{ID: "s2", ScopeKind: ScopeKindSession, Embedding: []float32{0, 1}, CreatedAt: now},
		{ID: "g1", ScopeKind: ScopeKindGlobal, Embedding: []float32{1, 1}, CreatedAt: now},
		{ID: "p1", ScopeKind: ScopeKindProject, Embedding: []float32{0, 0}, CreatedAt: now},
		// unembedded session chunk
		{ID: "s3", ScopeKind: ScopeKindSession, Embedding: nil, CreatedAt: now},
	}
	snap := SnapshotHealth(chunks, nil, now)
	if snap.Counts.Total != 5 {
		t.Errorf("total = %d, want 5", snap.Counts.Total)
	}
	if snap.Counts.Raw != 3 {
		t.Errorf("raw = %d, want 3", snap.Counts.Raw)
	}
	if snap.Counts.LongTermPromoted != 1 {
		t.Errorf("longTermPromoted = %d, want 1", snap.Counts.LongTermPromoted)
	}
	if snap.Counts.Narrative != 0 {
		t.Errorf("narrative = %d, want 0", snap.Counts.Narrative)
	}
	// 4 embedded (including project with empty-slice embedding), 1 unembedded.
	if snap.Counts.Embedded != 4 {
		t.Errorf("embedded = %d, want 4", snap.Counts.Embedded)
	}
	if snap.Counts.Unembedded != 1 {
		t.Errorf("unembedded = %d, want 1", snap.Counts.Unembedded)
	}
}

func TestSnapshotHealth_Activity7d(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-8 * 24 * time.Hour)
	chunks := []Chunk{
		// recent session
		{ID: "s1", ScopeKind: ScopeKindSession, CreatedAt: now},
		// recent global (counts as promoted)
		{ID: "g1", ScopeKind: ScopeKindGlobal, CreatedAt: now},
		// old session — outside the 7d window
		{ID: "s2", ScopeKind: ScopeKindSession, CreatedAt: old},
	}
	snap := SnapshotHealth(chunks, nil, now)
	if snap.Activity.Captured != 2 {
		t.Errorf("captured = %d, want 2", snap.Activity.Captured)
	}
	if snap.Activity.Promoted != 1 {
		t.Errorf("promoted = %d, want 1", snap.Activity.Promoted)
	}
}

func TestSnapshotHealth_NarrativeScope(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	chunks := []Chunk{
		{ID: "n1", ScopeKind: "narrative", Embedding: []float32{1}, CreatedAt: now},
	}
	snap := SnapshotHealth(chunks, nil, now)
	if snap.Counts.Narrative != 1 {
		t.Errorf("narrative = %d, want 1", snap.Counts.Narrative)
	}
	if snap.Counts.Raw != 0 {
		t.Errorf("raw = %d, want 0", snap.Counts.Raw)
	}
}

func TestSnapshotHealth_CapturedAt(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	snap := SnapshotHealth(nil, nil, ts)
	if !snap.CapturedAt.Equal(ts) {
		t.Errorf("capturedAt = %v, want %v", snap.CapturedAt, ts)
	}
}
