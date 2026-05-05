package agentgraph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
)

// MemoryStoreAdapter wraps core/memory.Store + an embedder onto the
// agentgraph.MemoryStore interface. Production wiring binds the real
// store; tests can pass a stub implementing the same interface.
//
// Embedding is required for both Add (memory.Store.Add demands a
// non-empty Embedding slice) and Read (k-NN query needs a query
// vector). When the embedder is unavailable (e.g. NoopEmbedder), Read
// degrades to a flat List + first-N return so the kernel still gets
// SOMETHING when memory is enabled but no provider is configured.
type MemoryStoreAdapter struct {
	store    corememory.Store
	embedder corememory.Embedder
}

// NewMemoryStoreAdapter constructs the adapter. A nil store causes
// every method to return ErrNoMemory (matches the kernel nil stub).
func NewMemoryStoreAdapter(store corememory.Store, embedder corememory.Embedder) *MemoryStoreAdapter {
	return &MemoryStoreAdapter{store: store, embedder: embedder}
}

// Write satisfies agentgraph.MemoryStore. Maps the kernel's MemoryWrite
// onto memory.Chunk + Add. Dedup behaviour is owned by the store; the
// adapter surfaces ErrDuplicate as deduped=true on success.
func (a *MemoryStoreAdapter) Write(ctx context.Context, w coreag.MemoryWrite) (string, bool, error) {
	if a == nil || a.store == nil {
		return "", false, coreag.ErrNoMemory
	}
	id, err := allocChunkID()
	if err != nil {
		return "", false, fmt.Errorf("memory: id gen: %w", err)
	}
	scope := w.Scope
	if scope == "" {
		scope = corememory.ScopeKindSession
	}
	scopeID := w.ScopeID
	if scopeID == "" {
		switch scope {
		case corememory.ScopeKindSession:
			scopeID = w.SessionID
		case corememory.ScopeKindProject:
			scopeID = w.ProjectID
		}
	}
	chunk := corememory.Chunk{
		ID:          id,
		SessionID:   w.SessionID,
		ProjectID:   w.ProjectID,
		ScopeKind:   scope,
		ScopeID:     scopeID,
		Content:     w.Content,
		ContentHash: corememory.HashContent(w.Content),
		Title:       w.Title,
		Source:      w.Source,
		Pinned:      w.Pinned,
		CreatedAt:   time.Now().UTC(),
	}
	// Embed when available. The store will reject Add when Embedding is
	// empty, so this is required for the production path. Adapter
	// degrades gracefully when no embedder is configured.
	if a.embedder != nil {
		vecs, err := a.embedder.Embed(ctx, []string{w.Content})
		if err == nil && len(vecs) > 0 {
			chunk.Embedding = vecs[0]
		}
	}
	if len(chunk.Embedding) == 0 {
		// Without an embedding we can't write. Return a friendly error
		// so the kernel surfaces it instead of letting a missing-key
		// runtime error leak through.
		return "", false, errors.New("memory: embedder unavailable — Memory features need an embedder. Configure an OpenAI, OpenRouter, custom OpenAI-compatible, or local llama-server provider in Settings → Models.")
	}
	if err := a.store.Add(ctx, chunk); err != nil {
		if errors.Is(err, corememory.ErrDuplicate) {
			return id, true, nil
		}
		return "", false, err
	}
	return id, false, nil
}

// Read satisfies agentgraph.MemoryStore. Maps the kernel's filter
// onto memory.Store.Query. When the embedder is unavailable, the
// adapter falls back to List + first-K (no relevance ranking).
func (a *MemoryStoreAdapter) Read(ctx context.Context, f coreag.MemoryReadFilter) ([]coreag.MemoryHit, error) {
	if a == nil || a.store == nil {
		return nil, coreag.ErrNoMemory
	}
	scopes := []corememory.ScopeFilter{}
	for _, s := range f.Scopes {
		scopes = append(scopes, corememory.ScopeFilter{Kind: s})
	}
	topK := f.TopK
	if topK <= 0 {
		topK = 5
	}
	if a.embedder != nil && f.Query != "" {
		vecs, err := a.embedder.Embed(ctx, []string{f.Query})
		if err == nil && len(vecs) > 0 {
			results, qerr := a.store.Query(ctx, vecs[0], topK, scopes...)
			if qerr == nil {
				out := make([]coreag.MemoryHit, 0, len(results))
				for _, r := range results {
					out = append(out, coreag.MemoryHit{
						ID:         r.Chunk.ID,
						Content:    r.Chunk.Content,
						Title:      r.Chunk.Title,
						ScopeKind:  r.Chunk.ScopeKind,
						ScopeID:    r.Chunk.ScopeID,
						Similarity: r.Similarity,
					})
				}
				return out, nil
			}
		}
	}
	// Fallback: list-by-scope + first-K, no ranking.
	chunks, err := a.store.List(ctx, scopes...)
	if err != nil {
		return nil, err
	}
	if topK > 0 && len(chunks) > topK {
		chunks = chunks[:topK]
	}
	out := make([]coreag.MemoryHit, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, coreag.MemoryHit{
			ID:        c.ID,
			Content:   c.Content,
			Title:     c.Title,
			ScopeKind: c.ScopeKind,
			ScopeID:   c.ScopeID,
		})
	}
	return out, nil
}

// allocChunkID returns a 16-byte hex id.
func allocChunkID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "mem-" + hex.EncodeToString(b[:]), nil
}

// Compile-time witness.
var _ coreag.MemoryStore = (*MemoryStoreAdapter)(nil)
