package prune

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// TestApply_CollapseFoldIsDisplayOnly pins memory-sync-01MEMSY01 WP05 / AC-3
// against the real gob store: a prune collapse folds the dropped chunk's
// recall into the survivor's display-only RecallFolded (persisted, so the
// survivor's score keeps it), while the survivor's pushed G-counter
// RecallOwn stays at its own real recalls and the fold does not schedule a
// push. Reopened from disk.
func TestApply_CollapseFoldIsDisplayOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "memory.gob")
	store, err := memory.NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	// Survivor: 2 own recalls + 3 pulled from other devices. Dropped
	// near-duplicate: 1 own recall (lower total, so it collapses).
	add := func(id string, emb []float32, own, others int) {
		t.Helper()
		if err := store.Add(ctx, memory.Chunk{ID: id, ScopeKind: memory.ScopeKindGlobal, Content: id,
			Embedding: emb, CreatedAt: now, LastAccessed: now, RecallOwn: own, RecallOthers: others}); err != nil {
			t.Fatal(err)
		}
	}
	add("survivor", []float32{1, 0, 0.01}, 2, 3)
	add("dup", []float32{1, 0.001, 0.01}, 1, 0)
	before := mustGet(t, store, "survivor")

	p := New(store, Rules{CollapseCosine: 0.97, KeepThreshold: 0.5}, func() time.Time { return now })
	dec, err := p.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Collapsed["dup"] != "survivor" {
		t.Fatalf("collapse = %+v", dec.Collapsed)
	}
	reopened, err := memory.NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, reopened, "survivor")
	if got.RecallOwn != 2 {
		t.Fatalf("pushed counter RecallOwn = %d, want 2 (own recalls only)", got.RecallOwn)
	}
	if got.RecallFolded != 1 || got.RecallOthers != 3 || got.RecallCount != 6 {
		t.Fatalf("fold = folded %d others %d total %d, want 1/3/6", got.RecallFolded, got.RecallOthers, got.RecallCount)
	}
	if got.SyncGen != before.SyncGen {
		t.Fatal("a display-only fold must not schedule a push")
	}
	if all, _ := reopened.List(ctx); len(all) != 1 {
		t.Fatalf("dup not dropped: %d chunks", len(all))
	}
}

func mustGet(t *testing.T, st memory.Store, id string) memory.Chunk {
	t.Helper()
	all, _ := st.List(context.Background())
	for _, c := range all {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("%s missing", id)
	return memory.Chunk{}
}
