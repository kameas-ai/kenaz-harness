package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestBackfillChunkDefaults_NarrativeFields verifies that chunks loaded from
// a pre-narrative-layer gob (empty Kind, zero RetrievalWeight) are backfilled
// correctly by backfillChunkDefaults (WP01 acceptance criterion).
func TestBackfillChunkDefaults_NarrativeFields(t *testing.T) {
	t.Parallel()

	c := Chunk{
		ID:        "x",
		ScopeKind: ScopeKindSession,
		ScopeID:   "sess-1",
		Content:   "hello",
		CreatedAt: time.Now(),
		// Kind and RetrievalWeight intentionally absent (legacy gob shape).
	}
	backfillChunkDefaults(&c)

	if c.Kind != "raw" {
		t.Errorf("Kind after backfill = %q, want %q", c.Kind, "raw")
	}
	if c.RetrievalWeight != 1.0 {
		t.Errorf("RetrievalWeight after backfill = %v, want 1.0", c.RetrievalWeight)
	}
}

// TestBackfillChunkDefaults_NarrativeFields_Explicit verifies that existing
// Kind and RetrievalWeight are NOT overwritten by backfillChunkDefaults.
func TestBackfillChunkDefaults_NarrativeFields_Explicit(t *testing.T) {
	t.Parallel()

	c := Chunk{
		ID:              "y",
		ScopeKind:       ScopeKindSession,
		ScopeID:         "sess-2",
		Content:         "world",
		CreatedAt:       time.Now(),
		Kind:            "narrative_synthesised",
		RetrievalWeight: 1.5,
	}
	backfillChunkDefaults(&c)

	if c.Kind != "narrative_synthesised" {
		t.Errorf("Kind after backfill = %q, want %q", c.Kind, "narrative_synthesised")
	}
	if c.RetrievalWeight != 1.5 {
		t.Errorf("RetrievalWeight after backfill = %v, want 1.5", c.RetrievalWeight)
	}
}

// TestChromemStore_NarrativeFieldRoundTrip verifies that Kind and
// RetrievalWeight survive an Add → close → reopen store cycle (WP01
// acceptance: "Round-trip: Add chunk with Kind=narrative_synthesised,
// RetrievalWeight=1.5, close+reopen store, verify fields persist").
func TestChromemStore_NarrativeFieldRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "memory.gob")

	store1, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	chunk := Chunk{
		ID:              "narr-1",
		ScopeKind:       ScopeKindSession,
		ScopeID:         "sess-rt",
		Content:         "narrative content",
		Embedding:       []float32{1, 0, 0},
		CreatedAt:       now,
		Kind:            "narrative_synthesised",
		RetrievalWeight: 1.5,
	}
	if err := store1.Add(ctx, chunk); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify fields persisted.
	store2, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("NewChromemStore reopen: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })

	list, err := store2.List(ctx)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	got := list[0]
	if got.Kind != "narrative_synthesised" {
		t.Errorf("Kind = %q, want %q", got.Kind, "narrative_synthesised")
	}
	if got.RetrievalWeight != 1.5 {
		t.Errorf("RetrievalWeight = %v, want 1.5", got.RetrievalWeight)
	}
}
