package rpc

// wp11_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-6 / WP11. AC-027: a retrieval driven through the hooks path
// (retrieverAdapter.RetrieveScoped) makes Memory_LastRetrieval-equivalent
// state (corememory.GlobalRetrievalHistory) return a non-empty report for
// that session id.

import (
	"context"
	"testing"

	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

// wp11FakeStore is a minimal corememory.Store — memory chunks are not
// SQL-backed (this Store interface has no relationship to
// core/storage/sqlite), so this is not the persistence-fixture bypass
// CLAUDE.md's blind spot #2 warns about; it is the store under test's
// own natural fake seam.
type wp11FakeStore struct {
	chunks []corememory.Chunk
}

func (s *wp11FakeStore) Add(ctx context.Context, chunk corememory.Chunk) error {
	s.chunks = append(s.chunks, chunk)
	return nil
}
func (s *wp11FakeStore) Delete(ctx context.Context, id string) error { return nil }
func (s *wp11FakeStore) List(ctx context.Context, scopes ...corememory.ScopeFilter) ([]corememory.Chunk, error) {
	return s.chunks, nil
}
func (s *wp11FakeStore) Query(ctx context.Context, embedding []float32, k int, scopes ...corememory.ScopeFilter) ([]corememory.Result, error) {
	out := make([]corememory.Result, 0, len(s.chunks))
	for _, c := range s.chunks {
		out = append(out, corememory.Result{Chunk: c, Similarity: 0.9})
		if len(out) >= k {
			break
		}
	}
	return out, nil
}
func (s *wp11FakeStore) Close() error { return nil }

type wp11FakeEmbedder struct{ dims int }

func (f *wp11FakeEmbedder) Kind() string    { return "fake" }
func (f *wp11FakeEmbedder) Dimensions() int { return f.dims }
func (f *wp11FakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, f.dims)
	}
	return out, nil
}

// TestWP11_AC027_RetrieveScopedThreadsSessionIDIntoHistory drives a real
// retrieval through retrieverAdapter.RetrieveScoped (the hooks path) and
// asserts GlobalRetrievalHistory().Last(sessionID) — what
// core/rpc/views/memory/impl.go's LastRetrieval reads — returns the
// record for THAT session id.
//
// Mutation (performed by hand, see the WP11 commit body for the run):
// reverting retrieverAdapter.RetrieveScoped to call a.r.RetrieveScoped
// directly (skipping WithSessionID) makes this fail, because the base
// *corememory.Retriever's sessionID field is always empty, so
// retrieve()'s `if r.sessionID != ""` guard never pushes to
// GlobalRetrievalHistory.
func TestWP11_AC027_RetrieveScopedThreadsSessionIDIntoHistory(t *testing.T) {
	sessionID := "wp11-test-session-" + t.Name()
	store := &wp11FakeStore{chunks: []corememory.Chunk{
		{ID: "c1", Content: "relevant content", ScopeKind: "session", ScopeID: sessionID},
	}}
	embedder := &wp11FakeEmbedder{dims: 3}
	retriever := corememory.NewRetriever(store, embedder, func() bool { return true }, 0.1)
	adapter := &retrieverAdapter{r: retriever}

	_, err := adapter.RetrieveScoped(context.Background(), "what did we discuss", sessionID, "", 5)
	if err != nil {
		t.Fatalf("RetrieveScoped: %v", err)
	}

	rec, ok := corememory.GlobalRetrievalHistory().Last(sessionID)
	if !ok {
		t.Fatalf("GlobalRetrievalHistory().Last(%q) found no record — the hooks-path retrieval was not recorded", sessionID)
	}
	if rec.Query != "what did we discuss" {
		t.Fatalf("recorded query = %q, want the driven query", rec.Query)
	}
	if len(rec.Results) == 0 {
		t.Fatalf("recorded 0 results, expected the one seeded chunk to surface")
	}
}
