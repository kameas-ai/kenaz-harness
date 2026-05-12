// Cross-session retrieval glue. The Retriever wraps Store + Embedder
// behind a single small surface the LLM impl can call without taking
// a hard dependency on either concrete type. The opt-in gate is read
// per-call via the EnabledFn so toggling memory in settings takes
// effect on the next send without restarting the harness.
package memory

import (
	"context"
	"fmt"
	"time"
)

// Retriever is the surface buildMessages calls to inject relevant
// snippets into the prompt. The concrete implementation lives here;
// callers use the Retrieve method only.
type Retriever struct {
	store     Store
	embedder  Embedder
	enabledFn func() bool
	threshold float32
	// sessionID is optionally set so the retriever can push records to
	// GlobalRetrievalHistory (memory-inspection-ui-01KX5R8E WP02).
	// Empty string disables history recording (e.g. in tests).
	sessionID string
}

// NewRetriever constructs a Retriever. enabledFn is the
// settings-backed opt-in gate; nil means always disabled (safe
// default). threshold is the minimum cosine similarity a snippet
// must clear to be injected.
func NewRetriever(store Store, embedder Embedder, enabledFn func() bool, threshold float32) *Retriever {
	if enabledFn == nil {
		enabledFn = func() bool { return false }
	}
	return &Retriever{
		store:     store,
		embedder:  embedder,
		enabledFn: enabledFn,
		threshold: threshold,
	}
}

// WithSessionID returns a copy of the Retriever that records retrieval
// decisions into GlobalRetrievalHistory under the given session ID.
// The production path calls this when wiring the kernel per-session;
// the bare constructor omits it for backwards compatibility.
func (r *Retriever) WithSessionID(sessionID string) *Retriever {
	if r == nil {
		return nil
	}
	out := *r
	out.sessionID = sessionID
	return &out
}

// Snippet is the shape returned to callers — a content string + score.
// Mirrors llm.MemorySnippet so the rpc/views/llm package can consume
// it without importing core/memory directly.
type Snippet struct {
	Content    string
	Similarity float32
}

// Retrieve embeds query and returns up to k snippets above the
// configured similarity threshold. No scope filter is applied — every
// chunk is a candidate. The scoped variant (RetrieveScoped) is the
// WP06 contract; this thin wrapper is what older callers (and the rpc
// adapter that has not yet been updated to thread project id) hit.
func (r *Retriever) Retrieve(ctx context.Context, query string, k int) ([]Snippet, error) {
	return r.retrieve(ctx, query, k, nil)
}

// RetrieveScoped runs a scope-union k-NN: global ∪ project:projectID ∪
// session:sessionID. Empty sessionID or projectID drops the matching
// filter so callers can opt into whichever scopes apply (the persist
// path before WP02 lands has no project yet, for example).
func (r *Retriever) RetrieveScoped(ctx context.Context, query, sessionID, projectID string, k int) ([]Snippet, error) {
	scopes := buildScopeUnion(sessionID, projectID)
	return r.retrieve(ctx, query, k, scopes)
}

func (r *Retriever) retrieve(ctx context.Context, query string, k int, scopes []ScopeFilter) ([]Snippet, error) {
	if r == nil || !r.enabledFn() {
		return nil, nil
	}
	if r.store == nil || r.embedder == nil {
		return nil, nil
	}
	if _, ok := r.embedder.(NoopEmbedder); ok {
		return nil, nil
	}
	if query == "" || k <= 0 {
		return nil, nil
	}
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	results, err := r.store.Query(ctx, vecs[0], k, scopes...)
	if err != nil {
		return nil, err
	}
	out := make([]Snippet, 0, len(results))
	// Build retrieval record for the inspector (WP02).
	var histResults []RetrievalResult
	for _, res := range results {
		injected := res.Similarity >= r.threshold
		histResults = append(histResults, RetrievalResult{
			ChunkID:    res.Chunk.ID,
			Content:    res.Chunk.Content,
			Kind:       res.Chunk.Kind,
			Pinned:     res.Chunk.Pinned,
			Similarity: res.Similarity,
			Injected:   injected,
		})
		if injected {
			out = append(out, Snippet{
				Content:    res.Chunk.Content,
				Similarity: res.Similarity,
			})
		}
	}
	// Push to global history when a session is bound.
	if r.sessionID != "" {
		GlobalRetrievalHistory().Push(RetrievalRecord{
			SessionID: r.sessionID,
			Query:     query,
			Results:   histResults,
			Threshold: r.threshold,
			At:        time.Now().UTC(),
		})
	}
	return out, nil
}

// buildScopeUnion returns the filter set for a session+project tuple.
// Always includes the global scope. Project / session filters are
// dropped when their id is empty so the caller still gets a usable
// (broader) result set.
func buildScopeUnion(sessionID, projectID string) []ScopeFilter {
	scopes := []ScopeFilter{{Kind: ScopeKindGlobal}}
	if projectID != "" {
		scopes = append(scopes, ScopeFilter{Kind: ScopeKindProject, ID: projectID})
	}
	if sessionID != "" {
		scopes = append(scopes, ScopeFilter{Kind: ScopeKindSession, ID: sessionID})
	}
	return scopes
}
