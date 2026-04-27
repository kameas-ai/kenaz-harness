package corpus

import (
	"context"
	"errors"
	"time"
)

// Scope discriminates the visibility of a corpus.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
	ScopeSession = "session"
)

// IngestState enumerates the lifecycle of a background ingest job.
const (
	IngestStatePending   = "pending"
	IngestStateRunning   = "running"
	IngestStateCompleted = "completed"
	IngestStateFailed    = "failed"
	IngestStateCanceled  = "canceled"
)

// Corpus is the metadata row for one named corpus.
type Corpus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	ScopeID   string    `json:"scopeId,omitempty"`
	Tag       string    `json:"tag,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CorpusFile is one ingested source file. SHA256 is the content hash
// used for re-ingest skip.
type CorpusFile struct {
	ID         string    `json:"id"`
	CorpusID   string    `json:"corpusId"`
	Path       string    `json:"path"`
	SHA256     string    `json:"sha256"`
	FileSize   int64     `json:"fileSize"`
	LineCount  int       `json:"lineCount"`
	IngestedAt time.Time `json:"ingestedAt"`
}

// ChunkProvenance captures the (file path + line range + hash) tuple a
// retrieved chunk carries so the chat surface can render a citation
// link.
type ChunkProvenance struct {
	FilePath  string `json:"filePath"`
	LineStart int    `json:"lineStart"`
	LineEnd   int    `json:"lineEnd"`
	SHA256    string `json:"sha256"`
}

// Chunk is one indexed text fragment with its provenance and embedding.
// Embedding is JSON-omitted; the frontend never reads vectors.
type Chunk struct {
	ID         string          `json:"id"`
	CorpusID   string          `json:"corpusId"`
	FileID     string          `json:"fileId"`
	ChunkSeq   int             `json:"chunkSeq"`
	Text       string          `json:"text"`
	Provenance ChunkProvenance `json:"provenance"`
	Embedding  []float32       `json:"-"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// RetrievalResult pairs a Chunk with its similarity score.
type RetrievalResult struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float32 `json:"similarity"`
}

// IngestStatus is what IngestPath returns and what Status polls. State
// transitions: pending -> running -> {completed | failed | canceled}.
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

// IngestOptions tunes the ingest pipeline at the edge.
type IngestOptions struct {
	// Recursive controls directory walk depth. False = scan top-level only.
	Recursive bool `json:"recursive"`
	// Extensions, when non-empty, restricts walk to files with these
	// (lowercased) suffixes. The default whitelist is applied when nil.
	Extensions []string `json:"extensions,omitempty"`
	// MaxFileBytes drops files larger than this; zero means default.
	MaxFileBytes int64 `json:"maxFileBytes,omitempty"`
	// ChunkLines is the line-based chunker's target window; zero means default.
	ChunkLines int `json:"chunkLines,omitempty"`
}

// RetrieveOptions narrows top-K retrieval.
type RetrieveOptions struct {
	TopK        int    `json:"topK,omitempty"`
	TokenBudget int    `json:"tokenBudget,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// Embedder is the seam this package depends on. core/llm wires the
// production OpenAI-backed embedder; tests pass a deterministic stub.
type Embedder interface {
	Dimensions() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Errors surfaced by the corpus package. Wrap with %w to preserve
// identity through the RPC boundary.
var (
	ErrCorpusNotFound      = errors.New("corpus: not found")
	ErrEmbedderUnavailable = errors.New("corpus: embedder unavailable")
	ErrInvalidScope        = errors.New("corpus: invalid scope")
	ErrInvalidName         = errors.New("corpus: invalid name")
	ErrJobNotFound         = errors.New("corpus: ingest job not found")
)
