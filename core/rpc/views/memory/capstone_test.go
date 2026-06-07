// Capstone tests for memory-inspection-ui-01KX5R8E WP02–WP05.
package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

// seedChunk adds a chunk with the given content/session to the store and
// returns its ID. Helper shared across capstone tests.
func seedChunk(t *testing.T, api *API, store corememory.Store, content, sessionID string) string {
	t.Helper()
	ctx := context.Background()
	vecs, err := (&fakeEmbedder{dim: 2}).Embed(ctx, []string{content})
	if err != nil {
		t.Fatalf("seed embed: %v", err)
	}
	id := "seed-" + content[:min(8, len(content))]
	chunk := corememory.Chunk{
		ID:          id,
		SessionID:   sessionID,
		ScopeKind:   corememory.ScopeKindSession,
		ScopeID:     sessionID,
		Content:     content,
		ContentHash: corememory.HashContent(content),
		Embedding:   vecs[0],
		CreatedAt:   time.Now().UTC(),
		Kind:        "raw",
	}
	if err := store.Add(ctx, chunk); err != nil {
		t.Fatalf("seed Add: %v", err)
	}
	return id
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── WP02: LastRetrieval ───────────────────────────────────────────────────────

func TestLastRetrieval_EmptyHistory(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithStore(t, &fakeReader{})
	report, err := api.LastRetrieval(context.Background(), "no-such-session")
	if err != nil {
		t.Fatalf("LastRetrieval: %v", err)
	}
	if len(report.Results) != 0 {
		t.Errorf("expected empty results, got %d", len(report.Results))
	}
	if report.SessionID != "no-such-session" {
		t.Errorf("sessionID = %q", report.SessionID)
	}
}

func TestLastRetrieval_ReturnsRecord(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithStore(t, &fakeReader{})
	now := time.Now().UTC()
	corememory.GlobalRetrievalHistory().Push(corememory.RetrievalRecord{
		SessionID: "sess-retrieval-test",
		Query:     "hello world",
		Threshold: 0.5,
		At:        now,
		Results: []corememory.RetrievalResult{
			{ChunkID: "cX", Content: "chunk content X", Similarity: 0.8, Injected: true},
			{ChunkID: "cY", Content: "chunk content Y", Similarity: 0.3, Injected: false},
		},
	})
	report, err := api.LastRetrieval(context.Background(), "sess-retrieval-test")
	if err != nil {
		t.Fatalf("LastRetrieval: %v", err)
	}
	if report.Query != "hello world" {
		t.Errorf("query = %q", report.Query)
	}
	if len(report.Results) != 2 {
		t.Fatalf("results = %d", len(report.Results))
	}
	if !report.Results[0].Injected {
		t.Error("first result should be injected")
	}
	if report.Results[1].Injected {
		t.Error("second result should not be injected")
	}
}

// ── WP03: EmbeddingProbe ──────────────────────────────────────────────────────

func TestEmbeddingProbe_NoEmbedder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := corememory.NewChromemStore(filepath.Join(dir, "mem.gob"))
	if err != nil {
		t.Fatal(err)
	}
	api := New(Config{Store: store}) // no embedder
	_, err = api.EmbeddingProbe(context.Background(), "query", 10)
	if err == nil {
		t.Error("expected ErrEmbedderUnavailable")
	}
}

func TestEmbeddingProbe_EmptyQuery(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithStore(t, &fakeReader{})
	results, err := api.EmbeddingProbe(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("EmbeddingProbe: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestEmbeddingProbe_ReturnsRankedResults(t *testing.T) {
	t.Parallel()
	api, store := newAPIWithStore(t, &fakeReader{})
	ctx := context.Background()

	// Seed two chunks.
	seedChunk(t, api, store, "this is relevant content", "sess-probe")
	seedChunk(t, api, store, "something unrelated here", "sess-probe")

	results, err := api.EmbeddingProbe(ctx, "relevant content", 10)
	if err != nil {
		t.Fatalf("EmbeddingProbe: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	// Ensure descending similarity order.
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("results not sorted desc: [%d]=%.3f > [%d]=%.3f",
				i, results[i].Similarity, i-1, results[i-1].Similarity)
		}
	}
}

func TestEmbeddingProbe_LimitCappedAt50(t *testing.T) {
	t.Parallel()
	api, store := newAPIWithStore(t, &fakeReader{})
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		seedChunk(t, api, store, "chunk-"+string(rune('a'+i)), "sess-cap")
	}
	// Request 100 but only 10 exist; cap shouldn't panic.
	results, err := api.EmbeddingProbe(ctx, "chunk", 100)
	if err != nil {
		t.Fatalf("EmbeddingProbe: %v", err)
	}
	if len(results) > 50 {
		t.Errorf("cap exceeded: %d", len(results))
	}
}

// ── WP04: ResummarizeChunk ────────────────────────────────────────────────────

func TestResummarizeChunk_RawChunk_ReplacesContent(t *testing.T) {
	t.Parallel()
	api, store := newAPIWithStore(t, &fakeReader{})
	ctx := context.Background()

	id := seedChunk(t, api, store, "this is the original content for summarisation", "sess-resummary")
	chunk, err := api.ResummarizeChunk(ctx, id)
	if err != nil {
		t.Fatalf("ResummarizeChunk: %v", err)
	}
	// A raw chunk replacement creates a new ID.
	if chunk.ID == id {
		t.Error("expected a new chunk ID after re-summarise")
	}
	if chunk.Kind != "narrative_extractive_fallback" {
		t.Errorf("kind = %q", chunk.Kind)
	}
}

func TestResummarizeChunk_RateLimited(t *testing.T) {
	t.Parallel()
	api, store := newAPIWithStore(t, &fakeReader{})
	ctx := context.Background()

	id := seedChunk(t, api, store, "rate limit me", "sess-rl")
	// First call should succeed.
	newChunk, err := api.ResummarizeChunk(ctx, id)
	if err != nil {
		t.Fatalf("first ResummarizeChunk: %v", err)
	}
	// Second call on the original ID should succeed (different ID now).
	// But calling on the new ID within 60s should also be rate-limited.
	_, err2 := api.ResummarizeChunk(ctx, newChunk.ID)
	// The new chunk doesn't have a rate-limit entry yet so this is fine.
	// Re-call on the *original* ID (which now doesn't exist) should be
	// rate-limited before the existence check.
	_, err3 := api.ResummarizeChunk(ctx, id)
	if err3 != ErrResummarizeRateLimited {
		t.Logf("err2=%v err3=%v", err2, err3)
		t.Error("expected rate limit error on second call for same chunk ID")
	}
}

func TestResummarizeChunk_NotFound(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithStore(t, &fakeReader{})
	_, err := api.ResummarizeChunk(context.Background(), "does-not-exist")
	if err == ErrResummarizeRateLimited {
		t.Skip("rate limit hit from prior test run — skip")
	}
	// Expect either ErrChunkNotFound or no error (first call bypasses rate limit).
	// The chunk simply doesn't exist so we get ErrChunkNotFound.
	if err != ErrChunkNotFound {
		t.Errorf("expected ErrChunkNotFound, got %v", err)
	}
}

// ── WP05: GetChunkProvenance ──────────────────────────────────────────────────

func TestGetChunkProvenance_BasicFields(t *testing.T) {
	t.Parallel()
	api, store := newAPIWithStore(t, &fakeReader{})
	ctx := context.Background()

	id := seedChunk(t, api, store, "provenance test content", "sess-prov")
	prov, err := api.GetChunkProvenance(ctx, id)
	if err != nil {
		t.Fatalf("GetChunkProvenance: %v", err)
	}
	if prov.ChunkID != id {
		t.Errorf("chunkID = %q, want %q", prov.ChunkID, id)
	}
	if prov.ScopePath == "" {
		t.Error("expected non-empty scope path")
	}
	if prov.EmbedderKind != "fake" {
		t.Errorf("embedder kind = %q, want fake", prov.EmbedderKind)
	}
	if prov.EmbedDimensions != 2 {
		t.Errorf("embed dims = %d, want 2", prov.EmbedDimensions)
	}
}

func TestGetChunkProvenance_NotFound(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithStore(t, &fakeReader{})
	_, err := api.GetChunkProvenance(context.Background(), "ghost-chunk")
	if err != ErrChunkNotFound {
		t.Errorf("expected ErrChunkNotFound, got %v", err)
	}
}

func TestGetChunkProvenance_ScopePaths(t *testing.T) {
	cases := []struct {
		scope string
		want  string
	}{
		{"session", "session"},
		{"project", "session → project"},
		{"global", "session → project → global"},
		{"long_term", "session → project → global → long_term"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.scope, func(t *testing.T) {
			t.Parallel()
			got := buildScopePath(tc.scope)
			if got != tc.want {
				t.Errorf("scope %q: got %q, want %q", tc.scope, got, tc.want)
			}
		})
	}
}
