package agentgraph

import (
	"context"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corecorpus "github.com/kameas-ai/kenaz-harness/core/corpus"
)

// CorpusBackendAdapter wraps core/corpus.Manager onto the
// agentgraph.CorpusBackend interface (Reader + Writer). Production
// wiring binds the real Manager here so the kernel's corpus_read /
// corpus_write executors retrieve from / ingest into the corpus
// subsystem instead of returning ErrNoCorpus.
type CorpusBackendAdapter struct {
	manager *corecorpus.Manager
}

// NewCorpusBackendAdapter constructs the adapter. A nil manager makes
// every method return ErrNoCorpus (matches the kernel's nilCorpus stub).
func NewCorpusBackendAdapter(m *corecorpus.Manager) *CorpusBackendAdapter {
	return &CorpusBackendAdapter{manager: m}
}

// Search satisfies agentgraph.Reader. Maps the kernel's flat (ids,
// query, topK) call onto corpus.Manager.Retrieve, called once per id
// because the manager retrieves per corpus. Hits from every corpus
// are merged in the order they're returned.
func (a *CorpusBackendAdapter) Search(ctx context.Context, ids []string, query string, topK int) ([]coreag.CorpusHit, error) {
	if a == nil || a.manager == nil {
		return nil, coreag.ErrNoCorpus
	}
	out := make([]coreag.CorpusHit, 0, topK)
	for _, id := range ids {
		if id == "" {
			continue
		}
		results, _, err := a.manager.Retrieve(ctx, id, query, corecorpus.RetrieveOptions{TopK: topK})
		if err != nil {
			// One bad corpus shouldn't fail the whole node — skip and
			// keep going so the model still sees what it can.
			continue
		}
		for _, r := range results {
			out = append(out, coreag.CorpusHit{
				CorpusID:   id,
				SourcePath: r.Chunk.Provenance.FilePath,
				ByteOffset: r.Chunk.Provenance.LineStart,
				Score:      r.Similarity,
				Snippet:    r.Chunk.Text,
			})
		}
		if topK > 0 && len(out) >= topK {
			out = out[:topK]
			break
		}
	}
	return out, nil
}

// Enqueue satisfies agentgraph.Writer. Maps the kernel's (corpusID,
// sourcePath) call onto corpus.Manager.IngestPath. Returns the job id
// the caller can poll for completion.
func (a *CorpusBackendAdapter) Enqueue(ctx context.Context, corpusID, sourcePath string) (string, error) {
	if a == nil || a.manager == nil {
		return "", coreag.ErrNoCorpus
	}
	st, err := a.manager.IngestPath(ctx, corpusID, sourcePath, corecorpus.IngestOptions{})
	if err != nil {
		return "", err
	}
	return st.JobID, nil
}

// Compile-time witness.
var _ coreag.CorpusBackend = (*CorpusBackendAdapter)(nil)
