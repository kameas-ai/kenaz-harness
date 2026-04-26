package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestChromemStore_AddListQueryDelete_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	chunks := []Chunk{
		{ID: "a", Content: "the user prefers vim keybindings",
			Embedding: []float32{1, 0, 0}, CreatedAt: now},
		{ID: "b", Content: "the user lives in Brooklyn",
			Embedding: []float32{0, 1, 0}, CreatedAt: now.Add(time.Second)},
		{ID: "c", Content: "the user dislikes onions",
			Embedding: []float32{0, 0, 1}, CreatedAt: now.Add(2 * time.Second)},
	}
	for _, c := range chunks {
		if err := store.Add(ctx, c); err != nil {
			t.Fatalf("Add %s: %v", c.ID, err)
		}
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("List len = %d, want 3", len(listed))
	}
	// Newest first.
	if listed[0].ID != "c" || listed[2].ID != "a" {
		t.Fatalf("List ordering: got %s..%s", listed[0].ID, listed[len(listed)-1].ID)
	}

	// Query should rank "a" first when the query vector matches its axis.
	results, err := store.Query(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Query len = %d, want 2", len(results))
	}
	if results[0].Chunk.ID != "a" {
		t.Fatalf("top result = %s, want a", results[0].Chunk.ID)
	}
	if results[0].Similarity < 0.99 {
		t.Fatalf("top similarity = %v, want ~1", results[0].Similarity)
	}

	// Delete and confirm the row is gone, no error on second delete-miss.
	if err := store.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "a"); err == nil {
		t.Fatal("Delete missing id should error")
	}
	listed, _ = store.List(ctx)
	if len(listed) != 2 {
		t.Fatalf("after delete len = %d, want 2", len(listed))
	}
}

func TestChromemStore_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.gob")
	ctx := context.Background()

	first, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Add(ctx, Chunk{
		ID: "x", Content: "persist me",
		Embedding: []float32{0.5, 0.5}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_ = first.Close()

	second, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	listed, _ := second.List(ctx)
	if len(listed) != 1 || listed[0].ID != "x" {
		t.Fatalf("did not round-trip: %+v", listed)
	}
}

func TestChromemStore_RejectsBadInputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	ctx := context.Background()
	if err := store.Add(ctx, Chunk{Content: "no id"}); err == nil {
		t.Fatal("expected error on empty id")
	}
	if err := store.Add(ctx, Chunk{ID: "x", Content: "no embedding"}); err == nil {
		t.Fatal("expected error on empty embedding")
	}
	if _, err := store.Query(ctx, nil, 5); err == nil {
		t.Fatal("expected error on empty query")
	}
}

// fakeEmbedder produces deterministic vectors keyed on the input text
// so tests can predict which chunk a query matches.
type fakeEmbedder struct {
	table map[string][]float32
	dims  int
}

func (f *fakeEmbedder) Kind() string    { return "fake" }
func (f *fakeEmbedder) Dimensions() int { return f.dims }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, ok := f.table[t]
		if !ok {
			// Default to a zero vector so missing keys don't match anything.
			v = make([]float32, f.dims)
		}
		out[i] = v
	}
	return out, nil
}

func TestRetriever_HonoursKAndThreshold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	mustAdd := func(id, content string, vec []float32) {
		t.Helper()
		if err := store.Add(ctx, Chunk{
			ID: id, Content: content, Embedding: vec, CreatedAt: now,
		}); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	mustAdd("near", "near match", []float32{1, 0, 0})
	mustAdd("mid", "mid match", []float32{0.6, 0.8, 0})
	mustAdd("far", "far match", []float32{0, 1, 0})

	emb := &fakeEmbedder{
		table: map[string][]float32{
			"do you remember vim?": {1, 0, 0},
		},
		dims: 3,
	}
	r := NewRetriever(store, emb, func() bool { return true }, 0.7)
	hits, err := r.Retrieve(ctx, "do you remember vim?", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Only "near" (sim 1.0) and "mid" (sim 0.6) — but mid is below the
	// 0.7 threshold so it must NOT appear.
	if len(hits) != 1 {
		t.Fatalf("hits len = %d, want 1: %+v", len(hits), hits)
	}
	if hits[0].Content != "near match" {
		t.Fatalf("top hit = %q", hits[0].Content)
	}

	// k=0 short-circuits.
	hits, _ = r.Retrieve(ctx, "do you remember vim?", 0)
	if len(hits) != 0 {
		t.Fatalf("k=0 hits len = %d, want 0", len(hits))
	}
}

func TestRetriever_DisabledReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	ctx := context.Background()
	if err := store.Add(ctx, Chunk{
		ID: "any", Content: "anything", Embedding: []float32{1, 0},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	emb := &fakeEmbedder{table: map[string][]float32{"q": {1, 0}}, dims: 2}
	r := NewRetriever(store, emb, func() bool { return false }, 0.5)
	hits, err := r.Retrieve(ctx, "q", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits when disabled, got %d", len(hits))
	}
}

func TestRetriever_NoopEmbedderReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	r := NewRetriever(store, NoopEmbedder{}, func() bool { return true }, 0.5)
	hits, err := r.Retrieve(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits with noop embedder, got %d", len(hits))
	}
}
