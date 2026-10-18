package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type countingEmbedder struct{ calls int }

func (e *countingEmbedder) Kind() string    { return "fake" }
func (e *countingEmbedder) Dimensions() int { return 2 }
func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0, 1}
	}
	return out, nil
}

// TestEmbedPending_StoreQueryDrain pins memory-sync-01MEMSY01 WP06 (H5,
// AC-4) on the real gob store: an explicitly EmbedPending chunk stores
// without a vector (the user-path invariant still refuses one that is
// not), stays visible to List, is skipped — not crashed on — by Query, a
// NoopEmbedder device leaves it pending, and a real embedder drains it
// without scheduling a push. Reopened from disk at each step.
func TestEmbedPending_StoreQueryDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.gob")
	st, err := NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.Add(ctx, Chunk{ID: "no-vec", ScopeKind: ScopeKindGlobal, Content: "c", CreatedAt: now}); !errors.Is(err, ErrEmbeddingRequired) {
		t.Fatalf("user path without a vector: err = %v, want ErrEmbeddingRequired", err)
	}
	if err := st.Add(ctx, Chunk{ID: "pulled", ScopeKind: ScopeKindLongTerm, Content: "pulled from fleet",
		EmbedPending: true, CreatedAt: now}); err != nil {
		t.Fatalf("EmbedPending add: %v", err)
	}
	if err := st.Add(ctx, Chunk{ID: "local", ScopeKind: ScopeKindGlobal, Content: "local",
		Embedding: []float32{1, 0}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	res, err := st.Query(ctx, []float32{1, 0}, 10)
	if err != nil || len(res) != 1 || res[0].Chunk.ID != "local" {
		t.Fatalf("Query must skip the vector-less chunk: res=%+v err=%v", res, err)
	}
	lt, _ := st.List(ctx, ScopeFilter{Kind: ScopeKindLongTerm})
	if len(lt) != 1 || lt[0].ID != "pulled" {
		t.Fatalf("pending chunk must stay visible to List/prelude: %+v", lt)
	}

	// NoopEmbedder device: nothing drains.
	if n, err := DrainEmbedPending(ctx, st, NoopEmbedder{}, 100); n != 0 || err != nil {
		t.Fatalf("noop drain = %d, %v", n, err)
	}
	re, _ := NewChromemStore(path)
	if c := get(t, re, "pulled"); !c.EmbedPending || len(c.Embedding) != 0 {
		t.Fatalf("noop device must keep the chunk pending: %+v", c)
	}

	// Real embedder: drains, persisted, no push scheduled.
	before := get(t, st, "pulled")
	emb := &countingEmbedder{}
	if n, err := DrainEmbedPending(ctx, st, emb, 100); n != 1 || err != nil {
		t.Fatalf("drain = %d, %v", n, err)
	}
	re2, _ := NewChromemStore(path)
	after := get(t, re2, "pulled")
	if after.EmbedPending || len(after.Embedding) != 2 {
		t.Fatalf("drained chunk = %+v", after)
	}
	if after.SyncGen != before.SyncGen {
		t.Fatal("indexing must not schedule a push (embeddings never sync)")
	}
	if n, _ := DrainEmbedPending(ctx, re2, emb, 100); n != 0 || emb.calls != 1 {
		t.Fatalf("second drain re-embedded: n=%d calls=%d", n, emb.calls)
	}
	if res, _ := re2.Query(ctx, []float32{0, 1}, 1); len(res) != 1 || res[0].Chunk.ID != "pulled" {
		t.Fatalf("drained chunk must be queryable: %+v", res)
	}
}
