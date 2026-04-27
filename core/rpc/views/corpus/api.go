// Package corpus is the view-scoped accessor for the corpora subsystem
// (mission agent-kernel-graph; Bundle C WP10/WP11). Wraps core/corpus
// behind the rpc boundary so the frontend never imports the storage
// layout directly.
//
// The wire shapes here mirror core/corpus types but trim fields that
// must not cross the Wails boundary (chunk embeddings stay in core/).
package corpus

import (
	"context"
	"time"
)

// Corpus is the wire shape for one corpus row.
type Corpus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	ScopeID   string    `json:"scopeId,omitempty"`
	Tag       string    `json:"tag,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CorpusFile is the wire shape for one ingested file.
type CorpusFile struct {
	ID         string    `json:"id"`
	CorpusID   string    `json:"corpusId"`
	Path       string    `json:"path"`
	SHA256     string    `json:"sha256"`
	FileSize   int64     `json:"fileSize"`
	LineCount  int       `json:"lineCount"`
	IngestedAt time.Time `json:"ingestedAt"`
}

// ChunkProvenance is the wire shape for chunk citations.
type ChunkProvenance struct {
	FilePath  string `json:"filePath"`
	LineStart int    `json:"lineStart"`
	LineEnd   int    `json:"lineEnd"`
	SHA256    string `json:"sha256"`
}

// Chunk is the wire shape for one stored chunk (no embedding).
type Chunk struct {
	ID         string          `json:"id"`
	CorpusID   string          `json:"corpusId"`
	FileID     string          `json:"fileId"`
	ChunkSeq   int             `json:"chunkSeq"`
	Text       string          `json:"text"`
	Provenance ChunkProvenance `json:"provenance"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// RetrievalResult pairs a Chunk with its similarity score.
type RetrievalResult struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float32 `json:"similarity"`
}

// IngestStatus is the wire shape for poll responses.
type IngestStatus struct {
	JobID       string    `json:"jobId"`
	CorpusID    string    `json:"corpusId"`
	State       string    `json:"state"`
	Path        string    `json:"path"`
	FilesTotal  int       `json:"filesTotal"`
	FilesDone   int       `json:"filesDone"`
	FilesSkip   int       `json:"filesSkip"`
	ChunksTotal int       `json:"chunksTotal"`
	StartedAt   time.Time `json:"startedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// IngestOptions narrows ingest behavior.
type IngestOptions struct {
	Recursive    bool     `json:"recursive,omitempty"`
	Extensions   []string `json:"extensions,omitempty"`
	MaxFileBytes int64    `json:"maxFileBytes,omitempty"`
	ChunkLines   int      `json:"chunkLines,omitempty"`
}

// CreateRequest is the body for CreateCorpus.
type CreateRequest struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	ScopeID string `json:"scopeId,omitempty"`
	Tag     string `json:"tag,omitempty"`
}

// RetrieveRequest is the body for Retrieve.
type RetrieveRequest struct {
	Query       string `json:"query"`
	TopK        int    `json:"topK,omitempty"`
	TokenBudget int    `json:"tokenBudget,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// RetrieveResponse pairs the retrieval results with the dropped count
// when the token budget capped the output.
type RetrieveResponse struct {
	Results []RetrievalResult `json:"results"`
	Dropped int               `json:"dropped"`
}

// CorpusAPI is the view-scoped surface backing /corpora.
//
// The implementation lives in impl.go and wraps core/corpus.Manager. A
// nil manager is allowed; methods return ErrManagerUnavailable so the
// frontend can render an empty state without the chassis crashing.
type CorpusAPI interface {
	// ListCorpora returns every corpus matching scope (empty = all).
	ListCorpora(ctx context.Context, scope string) ([]Corpus, error)
	// CreateCorpus persists a new corpus. Returns the populated row.
	CreateCorpus(ctx context.Context, req CreateRequest) (Corpus, error)
	// GetCorpus returns a single corpus row.
	GetCorpus(ctx context.Context, corpusID string) (Corpus, error)
	// DeleteCorpus drops a corpus + all dependent rows + on-disk
	// vectors. Idempotent.
	DeleteCorpus(ctx context.Context, corpusID string) error
	// ListFiles returns every ingested file row for a corpus.
	ListFiles(ctx context.Context, corpusID string) ([]CorpusFile, error)
	// ListChunks returns every chunk for one file (preview surface).
	ListChunks(ctx context.Context, corpusID, fileID string) ([]Chunk, error)
	// IngestPath kicks off a background ingest. Returns the initial
	// status with state=pending; caller polls JobStatus.
	IngestPath(ctx context.Context, corpusID, path string, opts IngestOptions) (IngestStatus, error)
	// JobStatus returns the latest IngestStatus for jobID.
	JobStatus(ctx context.Context, jobID string) (IngestStatus, error)
	// Retrieve runs cosine top-K against corpusID with token-budget
	// enforcement. Empty corpusID returns ErrInvalidArg.
	Retrieve(ctx context.Context, corpusID string, req RetrieveRequest) (RetrieveResponse, error)
}
