// Cross-session retrieval glue. The Retriever wraps Store + Embedder
// behind a single small surface the LLM impl can call without taking
// a hard dependency on either concrete type. The opt-in gate is read
// per-call via the EnabledFn so toggling memory in settings takes
// effect on the next send without restarting the harness.
package memory

import (
	"context"
	"fmt"
)

// Retriever is the surface buildMessages calls to inject relevant
// snippets into the prompt. The concrete implementation lives here;
// callers use the Retrieve method only.
type Retriever struct {
	store     Store
	embedder  Embedder
	enabledFn func() bool
	threshold float32
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

// Snippet is the shape returned to callers — a content string + score.
// Mirrors llm.MemorySnippet so the rpc/views/llm package can consume
// it without importing core/memory directly.
type Snippet struct {
	Content    string
	Similarity float32
}

// Retrieve embeds query and returns up to k snippets above the
// configured similarity threshold. When memory is disabled, the store
// is missing, or the embedder is the noop, an empty result is returned
// (no error) so the caller's "skip on empty" path is clean.
func (r *Retriever) Retrieve(ctx context.Context, query string, k int) ([]Snippet, error) {
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
	results, err := r.store.Query(ctx, vecs[0], k)
	if err != nil {
		return nil, err
	}
	out := make([]Snippet, 0, len(results))
	for _, res := range results {
		if res.Similarity < r.threshold {
			continue
		}
		out = append(out, Snippet{
			Content:    res.Chunk.Content,
			Similarity: res.Similarity,
		})
	}
	return out, nil
}
