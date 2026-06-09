// Concrete CorpusAPI implementation. Wraps core/corpus.Manager.
package corpus

import (
	"context"
	"errors"

	corecorpus "github.com/kameas-ai/kenaz-harness/core/corpus"
)

// API is the concrete CorpusAPI implementation.
type API struct {
	mgr *corecorpus.Manager
}

// New constructs a CorpusAPI. A nil manager is allowed; surface returns
// ErrManagerUnavailable so the frontend renders an empty state instead
// of a confusing "not wired" error.
func New(mgr *corecorpus.Manager) *API {
	return &API{mgr: mgr}
}

// ErrManagerUnavailable signals the chassis booted without the manager
// wired (e.g. an empty DataDir during tests). The frontend's empty
// state card is the user-visible behaviour.
var ErrManagerUnavailable = errors.New("corpus: manager unavailable")

// ErrInvalidArg covers the small handful of trivially-invalid request
// shapes (empty corpus id, empty path) the impl rejects before calling
// into core/corpus. Wrapped errors from core/corpus itself preserve
// their identity through %w.
var ErrInvalidArg = errors.New("corpus: invalid argument")

// ListCorpora returns every corpus matching scope (empty = all).
func (a *API) ListCorpora(ctx context.Context, scope string) ([]Corpus, error) {
	if a == nil || a.mgr == nil {
		return []Corpus{}, ErrManagerUnavailable
	}
	rows, err := a.mgr.ListCorpora(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make([]Corpus, 0, len(rows))
	for _, c := range rows {
		out = append(out, toWireCorpus(c))
	}
	return out, nil
}

// CreateCorpus persists a new corpus.
func (a *API) CreateCorpus(ctx context.Context, req CreateRequest) (Corpus, error) {
	if a == nil || a.mgr == nil {
		return Corpus{}, ErrManagerUnavailable
	}
	c, err := a.mgr.CreateCorpus(ctx, req.Name, req.Scope, req.ScopeID, req.Tag)
	if err != nil {
		return Corpus{}, err
	}
	return toWireCorpus(c), nil
}

// GetCorpus returns a single corpus row.
func (a *API) GetCorpus(ctx context.Context, corpusID string) (Corpus, error) {
	if a == nil || a.mgr == nil {
		return Corpus{}, ErrManagerUnavailable
	}
	if corpusID == "" {
		return Corpus{}, ErrInvalidArg
	}
	c, err := a.mgr.GetCorpus(ctx, corpusID)
	if err != nil {
		return Corpus{}, err
	}
	return toWireCorpus(c), nil
}

// DeleteCorpus removes a corpus.
func (a *API) DeleteCorpus(ctx context.Context, corpusID string) error {
	if a == nil || a.mgr == nil {
		return ErrManagerUnavailable
	}
	if corpusID == "" {
		return ErrInvalidArg
	}
	return a.mgr.DeleteCorpus(ctx, corpusID)
}

// ListFiles returns every ingested file for a corpus.
func (a *API) ListFiles(ctx context.Context, corpusID string) ([]CorpusFile, error) {
	if a == nil || a.mgr == nil {
		return []CorpusFile{}, ErrManagerUnavailable
	}
	if corpusID == "" {
		return nil, ErrInvalidArg
	}
	files, err := a.mgr.ListFiles(ctx, corpusID)
	if err != nil {
		return nil, err
	}
	out := make([]CorpusFile, 0, len(files))
	for _, f := range files {
		out = append(out, CorpusFile{
			ID:         f.ID,
			CorpusID:   f.CorpusID,
			Path:       f.Path,
			SHA256:     f.SHA256,
			FileSize:   f.FileSize,
			LineCount:  f.LineCount,
			IngestedAt: f.IngestedAt,
		})
	}
	return out, nil
}

// ListChunks returns chunk previews for one file.
func (a *API) ListChunks(ctx context.Context, corpusID, fileID string) ([]Chunk, error) {
	if a == nil || a.mgr == nil {
		return []Chunk{}, ErrManagerUnavailable
	}
	if corpusID == "" || fileID == "" {
		return nil, ErrInvalidArg
	}
	chunks, err := a.mgr.ListChunks(ctx, corpusID, fileID)
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, toWireChunk(c))
	}
	return out, nil
}

// IngestPath starts a background ingest job.
func (a *API) IngestPath(ctx context.Context, corpusID, path string, opts IngestOptions) (IngestStatus, error) {
	if a == nil || a.mgr == nil {
		return IngestStatus{}, ErrManagerUnavailable
	}
	if corpusID == "" || path == "" {
		return IngestStatus{}, ErrInvalidArg
	}
	st, err := a.mgr.IngestPath(ctx, corpusID, path, corecorpus.IngestOptions{
		Recursive:    opts.Recursive,
		Extensions:   opts.Extensions,
		MaxFileBytes: opts.MaxFileBytes,
		ChunkLines:   opts.ChunkLines,
	})
	if err != nil {
		return IngestStatus{}, err
	}
	return toWireStatus(st), nil
}

// JobStatus polls an ingest job.
func (a *API) JobStatus(ctx context.Context, jobID string) (IngestStatus, error) {
	if a == nil || a.mgr == nil {
		return IngestStatus{}, ErrManagerUnavailable
	}
	if jobID == "" {
		return IngestStatus{}, ErrInvalidArg
	}
	st, err := a.mgr.JobStatus(jobID)
	if err != nil {
		return IngestStatus{}, err
	}
	return toWireStatus(st), nil
}

// Retrieve runs cosine top-K with token-budget enforcement.
func (a *API) Retrieve(ctx context.Context, corpusID string, req RetrieveRequest) (RetrieveResponse, error) {
	if a == nil || a.mgr == nil {
		return RetrieveResponse{}, ErrManagerUnavailable
	}
	if corpusID == "" {
		return RetrieveResponse{}, ErrInvalidArg
	}
	if req.Query == "" {
		return RetrieveResponse{}, ErrInvalidArg
	}
	results, dropped, err := a.mgr.Retrieve(ctx, corpusID, req.Query, corecorpus.RetrieveOptions{
		TopK:        req.TopK,
		TokenBudget: req.TokenBudget,
		Tag:         req.Tag,
		Scope:       req.Scope,
	})
	if err != nil {
		return RetrieveResponse{}, err
	}
	wireResults := make([]RetrievalResult, 0, len(results))
	for _, r := range results {
		wireResults = append(wireResults, RetrievalResult{
			Chunk:      toWireChunk(r.Chunk),
			Similarity: r.Similarity,
		})
	}
	return RetrieveResponse{Results: wireResults, Dropped: dropped}, nil
}

func toWireCorpus(c corecorpus.Corpus) Corpus {
	return Corpus{
		ID:        c.ID,
		Name:      c.Name,
		Scope:     c.Scope,
		ScopeID:   c.ScopeID,
		Tag:       c.Tag,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

func toWireChunk(c corecorpus.Chunk) Chunk {
	return Chunk{
		ID:       c.ID,
		CorpusID: c.CorpusID,
		FileID:   c.FileID,
		ChunkSeq: c.ChunkSeq,
		Text:     c.Text,
		Provenance: ChunkProvenance{
			FilePath:  c.Provenance.FilePath,
			LineStart: c.Provenance.LineStart,
			LineEnd:   c.Provenance.LineEnd,
			SHA256:    c.Provenance.SHA256,
		},
		CreatedAt: c.CreatedAt,
	}
}

func toWireStatus(s corecorpus.IngestStatus) IngestStatus {
	return IngestStatus{
		JobID:       s.JobID,
		CorpusID:    s.CorpusID,
		State:       s.State,
		Path:        s.Path,
		FilesTotal:  s.FilesTotal,
		FilesDone:   s.FilesDone,
		FilesSkip:   s.FilesSkip,
		ChunksTotal: s.ChunksTotal,
		StartedAt:   s.StartedAt,
		UpdatedAt:   s.UpdatedAt,
		FinishedAt:  s.FinishedAt,
		Error:       s.Error,
	}
}

// Compile-time witness.
var _ CorpusAPI = (*API)(nil)
