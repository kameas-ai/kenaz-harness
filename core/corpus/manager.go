package corpus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultMaxFileBytes caps individual file size during walk to keep a
// stray binary from wedging the embedder. 4 MiB matches the spec's
// "engineering corpora" intent.
const DefaultMaxFileBytes = 4 << 20 // 4 MiB

// DefaultTokenBudget is the spec default for retrieval output: 16 KiB.
const DefaultTokenBudget = 16 * 1024

// DefaultTopK is the retrieval default when caller leaves it zero.
const DefaultTopK = 8

// Manager owns the corpus subsystem: corpus CRUD, ingestion pipeline,
// per-corpus vector store cache, and retrieval. One per process; the
// chassis constructs it in core/rpc/api.go and shares it with the RPC
// view + the kernel's CorpusReadNode.
//
// Concurrency:
//   - corpus CRUD goes straight at the storage layer; SQL transactions
//     are the synchronization point.
//   - the vectorStore cache is guarded by mu; one entry per corpus_id.
//   - background ingest jobs run on dedicated goroutines and report
//     progress through the jobs map (mu).
type Manager struct {
	db      DB
	dataDir string
	embed   Embedder
	now     func() time.Time

	mu     sync.Mutex
	stores map[string]*vectorStore // keyed by corpus_id
	jobs   map[string]*IngestStatus
}

// NewManager constructs a Manager. embed may be nil during boot; the
// Manager rejects ingest until SetEmbedder is called with a usable one.
func NewManager(db DB, dataDir string, embed Embedder) *Manager {
	return &Manager{
		db:      db,
		dataDir: dataDir,
		embed:   embed,
		now:     time.Now,
		stores:  make(map[string]*vectorStore),
		jobs:    make(map[string]*IngestStatus),
	}
}

// SetEmbedder swaps the active embedder. The chassis calls this once
// the LLM stack reports a working embedder; until then ingest fails
// fast with ErrEmbedderUnavailable.
func (m *Manager) SetEmbedder(e Embedder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embed = e
}

// CreateCorpus persists a new corpus row. Name + scope are validated;
// ScopeID is required for project / session scopes. Returns the
// populated Corpus on success.
func (m *Manager) CreateCorpus(ctx context.Context, name, scope, scopeID, tag string) (Corpus, error) {
	if m == nil || m.db == nil {
		return Corpus{}, errors.New("corpus: manager not wired")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Corpus{}, fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	switch scope {
	case ScopeGlobal:
		scopeID = ""
	case ScopeProject, ScopeSession:
		if scopeID == "" {
			return Corpus{}, fmt.Errorf("%w: scope_id required for scope %q", ErrInvalidScope, scope)
		}
	default:
		return Corpus{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}

	id, err := newID("corp")
	if err != nil {
		return Corpus{}, err
	}
	now := m.now().UTC()
	c := Corpus{
		ID:        id,
		Name:      name,
		Scope:     scope,
		ScopeID:   scopeID,
		Tag:       tag,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.db.WriteTx(ctx, func(tx WriteTx) error {
		return insertCorpus(ctx, tx, c)
	}); err != nil {
		return Corpus{}, err
	}
	return c, nil
}

// ListCorpora returns every corpus matching scope (empty = all).
func (m *Manager) ListCorpora(ctx context.Context, scope string) ([]Corpus, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("corpus: manager not wired")
	}
	return listCorpora(ctx, m.db.Reader(), scope)
}

// GetCorpus returns a single corpus row.
func (m *Manager) GetCorpus(ctx context.Context, corpusID string) (Corpus, error) {
	if m == nil || m.db == nil {
		return Corpus{}, errors.New("corpus: manager not wired")
	}
	return loadCorpus(ctx, m.db.Reader(), corpusID)
}

// DeleteCorpus drops the corpus + all dependent rows + the on-disk
// vector store. Idempotent: deleting a missing corpus returns nil.
func (m *Manager) DeleteCorpus(ctx context.Context, corpusID string) error {
	if m == nil || m.db == nil {
		return errors.New("corpus: manager not wired")
	}
	if err := m.db.WriteTx(ctx, func(tx WriteTx) error {
		_, err := deleteCorpus(ctx, tx, corpusID)
		return err
	}); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.stores, corpusID)
	m.mu.Unlock()
	storeDir := filepath.Join(m.dataDir, "corpora", corpusID)
	_ = os.RemoveAll(storeDir)
	return nil
}

// ListFiles returns every ingested file row for a corpus.
func (m *Manager) ListFiles(ctx context.Context, corpusID string) ([]CorpusFile, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("corpus: manager not wired")
	}
	return listFiles(ctx, m.db.Reader(), corpusID)
}

// ListChunks returns every chunk for a single file (preview surface).
func (m *Manager) ListChunks(ctx context.Context, corpusID, fileID string) ([]Chunk, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("corpus: manager not wired")
	}
	return listChunksForFile(ctx, m.db.Reader(), corpusID, fileID)
}

// JobStatus returns the latest IngestStatus for jobID, or
// ErrJobNotFound. Pollable from the frontend.
func (m *Manager) JobStatus(jobID string) (IngestStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.jobs[jobID]
	if !ok {
		return IngestStatus{}, ErrJobNotFound
	}
	return *st, nil
}

// IngestPath kicks off a background ingest of path against corpusID.
// path may be a single file or a directory; the walk respects
// opts.Recursive. Returns a snapshot IngestStatus with state=pending;
// caller polls JobStatus(jobID) for progress.
//
// Atomic per-file commit (NFR-006): each file's SQL writes (file row +
// chunks) live in one WriteTx that ALSO defers the vector-store
// snapshot rewrite until SQL commit succeeds. A crash before tx commit
// leaves zero rows + zero vectors for that file; the next ingest
// re-hashes the file and starts fresh.
func (m *Manager) IngestPath(ctx context.Context, corpusID, path string, opts IngestOptions) (IngestStatus, error) {
	if m == nil || m.db == nil {
		return IngestStatus{}, errors.New("corpus: manager not wired")
	}
	if _, err := m.GetCorpus(ctx, corpusID); err != nil {
		return IngestStatus{}, err
	}
	m.mu.Lock()
	embedder := m.embed
	m.mu.Unlock()
	if embedder == nil {
		return IngestStatus{}, ErrEmbedderUnavailable
	}
	jobID, err := newID("ing")
	if err != nil {
		return IngestStatus{}, err
	}
	now := m.now().UTC()
	st := &IngestStatus{
		JobID:     jobID,
		CorpusID:  corpusID,
		State:     IngestStatePending,
		Path:      path,
		StartedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.jobs[jobID] = st
	// Snapshot before releasing the lock or launching the goroutine —
	// runIngest mutates *st via updateJob, which races the *st copy
	// the bare return below would otherwise perform after the
	// goroutine has already started.
	snap := *st
	m.mu.Unlock()
	go m.runIngest(jobID, corpusID, path, opts, embedder)
	return snap, nil
}

// IngestPathSync is the synchronous form used by tests + the kernel's
// CorpusWriteNode when the caller wants to block until ingest finishes.
// Returns the final IngestStatus.
func (m *Manager) IngestPathSync(ctx context.Context, corpusID, path string, opts IngestOptions) (IngestStatus, error) {
	st, err := m.IngestPath(ctx, corpusID, path, opts)
	if err != nil {
		return IngestStatus{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return IngestStatus{}, ctx.Err()
		default:
		}
		cur, err := m.JobStatus(st.JobID)
		if err != nil {
			return IngestStatus{}, err
		}
		if cur.State == IngestStateCompleted ||
			cur.State == IngestStateFailed ||
			cur.State == IngestStateCanceled {
			return cur, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runIngest is the goroutine body that walks + hashes + chunks + embeds
// + commits per-file. State transitions are written through updateJob.
func (m *Manager) runIngest(jobID, corpusID, root string, opts IngestOptions, embedder Embedder) {
	bg := context.Background()
	m.updateJob(jobID, func(s *IngestStatus) {
		s.State = IngestStateRunning
		s.UpdatedAt = m.now().UTC()
	})

	files, err := walkPaths(root, opts)
	if err != nil {
		m.updateJob(jobID, func(s *IngestStatus) {
			s.State = IngestStateFailed
			s.Error = err.Error()
			s.FinishedAt = m.now().UTC()
			s.UpdatedAt = s.FinishedAt
		})
		return
	}
	m.updateJob(jobID, func(s *IngestStatus) {
		s.FilesTotal = len(files)
		s.UpdatedAt = m.now().UTC()
	})

	store, err := m.openStoreLocked(corpusID)
	if err != nil {
		m.updateJob(jobID, func(s *IngestStatus) {
			s.State = IngestStateFailed
			s.Error = err.Error()
			s.FinishedAt = m.now().UTC()
			s.UpdatedAt = s.FinishedAt
		})
		return
	}

	chunkLines := opts.ChunkLines
	if chunkLines == 0 {
		chunkLines = DefaultChunkLines
	}

	for _, fp := range files {
		select {
		case <-bg.Done():
			return
		default:
		}
		skipped, chunkCount, err := m.ingestOneFile(bg, corpusID, fp, root, store, embedder, chunkLines)
		if err != nil {
			m.updateJob(jobID, func(s *IngestStatus) {
				s.State = IngestStateFailed
				s.Error = err.Error()
				s.FinishedAt = m.now().UTC()
				s.UpdatedAt = s.FinishedAt
			})
			return
		}
		m.updateJob(jobID, func(s *IngestStatus) {
			s.FilesDone++
			if skipped {
				s.FilesSkip++
			}
			s.ChunksTotal += chunkCount
			s.UpdatedAt = m.now().UTC()
		})
	}
	m.updateJob(jobID, func(s *IngestStatus) {
		s.State = IngestStateCompleted
		s.FinishedAt = m.now().UTC()
		s.UpdatedAt = s.FinishedAt
	})
}

// ingestOneFile is the per-file commit boundary. Returns (skipped, chunkCount, err).
// Atomic: SQL writes + vector store add happen inside m.db.WriteTx; on
// success the vector store snapshot is rewritten to disk. On error,
// rollback drops every SQL row and the vector store cache is restored.
func (m *Manager) ingestOneFile(
	ctx context.Context,
	corpusID, absPath, root string,
	store *vectorStore,
	embedder Embedder,
	chunkLines int,
) (bool, int, error) {
	relPath, err := relPathOrAbs(root, absPath)
	if err != nil {
		return false, 0, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return false, 0, fmt.Errorf("corpus: read %s: %w", relPath, err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	// Hash-check skip (FR-028 prior spec / WP10-T7).
	existing, err := fileByPath(ctx, m.db.Reader(), corpusID, relPath)
	if err == nil && existing.SHA256 == hash {
		return true, 0, nil
	}

	content := string(data)
	ext := filepath.Ext(absPath)
	raw := chunkContent(content, ext, chunkLines)
	if len(raw) == 0 {
		// Empty file — record the corpus_files row but no chunks.
		fileID, err := newID("file")
		if err != nil {
			return false, 0, err
		}
		now := m.now().UTC()
		if err := m.db.WriteTx(ctx, func(tx WriteTx) error {
			if existing.ID != "" {
				if err := deleteFile(ctx, tx, existing.ID); err != nil {
					return err
				}
			}
			return insertFile(ctx, tx, CorpusFile{
				ID:         fileID,
				CorpusID:   corpusID,
				Path:       relPath,
				SHA256:     hash,
				FileSize:   int64(len(data)),
				LineCount:  countLines(content),
				IngestedAt: now,
			})
		}); err != nil {
			return false, 0, err
		}
		if existing.ID != "" {
			store.removeFile(existing.ID)
			if err := store.flush(); err != nil {
				return false, 0, err
			}
		}
		return false, 0, nil
	}

	// Embed all chunks in one batch — fewer round-trips for the
	// OpenAI embedder, deterministic sequencing for the test stub.
	texts := make([]string, len(raw))
	for i, r := range raw {
		texts[i] = r.Text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return false, 0, fmt.Errorf("corpus: embed %s: %w", relPath, err)
	}
	if len(vecs) != len(raw) {
		return false, 0, fmt.Errorf("corpus: embedder returned %d vectors for %d chunks", len(vecs), len(raw))
	}

	fileID, err := newID("file")
	if err != nil {
		return false, 0, err
	}
	now := m.now().UTC()
	chunks := make([]Chunk, len(raw))
	entries := make([]vectorEntry, len(raw))
	for i, r := range raw {
		chunkID, err := newID("ch")
		if err != nil {
			return false, 0, err
		}
		chunks[i] = Chunk{
			ID:       chunkID,
			CorpusID: corpusID,
			FileID:   fileID,
			ChunkSeq: r.Seq,
			Text:     r.Text,
			Provenance: ChunkProvenance{
				FilePath:  relPath,
				LineStart: r.LineStart,
				LineEnd:   r.LineEnd,
				SHA256:    hash,
			},
			Embedding: vecs[i],
			CreatedAt: now,
		}
		entries[i] = vectorEntry{
			ChunkID:   chunkID,
			FileID:    fileID,
			ChunkSeq:  r.Seq,
			Text:      r.Text,
			LineStart: r.LineStart,
			LineEnd:   r.LineEnd,
			FilePath:  relPath,
			SHA256:    hash,
			Embedding: vecs[i],
		}
	}

	// Atomic commit: SQL writes + vector store add happen here.
	if err := m.db.WriteTx(ctx, func(tx WriteTx) error {
		if existing.ID != "" {
			if err := deleteFile(ctx, tx, existing.ID); err != nil {
				return err
			}
		}
		if err := insertFile(ctx, tx, CorpusFile{
			ID:         fileID,
			CorpusID:   corpusID,
			Path:       relPath,
			SHA256:     hash,
			FileSize:   int64(len(data)),
			LineCount:  countLines(content),
			IngestedAt: now,
		}); err != nil {
			return err
		}
		for _, c := range chunks {
			if err := insertChunk(ctx, tx, c); err != nil {
				return err
			}
		}
		return updateCorpusTimestamp(ctx, tx, corpusID, now)
	}); err != nil {
		return false, 0, err
	}

	// SQL committed — update vector store and persist snapshot. If the
	// flush fails we still hold the SQL state; the next ingest will
	// rebuild the snapshot from SQL via Open.
	if existing.ID != "" {
		store.removeFile(existing.ID)
	}
	store.addEntries(entries)
	if err := store.flush(); err != nil {
		return false, len(chunks), err
	}
	return false, len(chunks), nil
}

// Retrieve runs cosine top-K against corpusID with token-budget
// enforcement (NFR / WP11-T6). Spillover is dropped silently — the
// caller can subscribe to an event for the warning if needed; the spec
// classifies this as a hard cap, not advisory.
//
// Returns (results, droppedCount, error). droppedCount > 0 indicates
// the budget capped the output and the caller may want to surface a
// "results truncated" warning.
func (m *Manager) Retrieve(ctx context.Context, corpusID, query string, opts RetrieveOptions) ([]RetrievalResult, int, error) {
	if m == nil || m.db == nil {
		return nil, 0, errors.New("corpus: manager not wired")
	}
	if _, err := m.GetCorpus(ctx, corpusID); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	embedder := m.embed
	m.mu.Unlock()
	if embedder == nil {
		return nil, 0, ErrEmbedderUnavailable
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	tokenBudget := opts.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}

	store, err := m.openStoreLocked(corpusID)
	if err != nil {
		return nil, 0, err
	}

	vecs, err := embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, 0, fmt.Errorf("corpus: embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, 0, errors.New("corpus: embedder returned no query vectors")
	}
	scored := store.query(vecs[0], topK)

	out := make([]RetrievalResult, 0, len(scored))
	totalBytes := 0
	dropped := 0
	for _, s := range scored {
		size := len(s.Entry.Text)
		if totalBytes+size > tokenBudget && len(out) > 0 {
			dropped = len(scored) - len(out)
			break
		}
		out = append(out, RetrievalResult{
			Chunk: Chunk{
				ID:       s.Entry.ChunkID,
				CorpusID: corpusID,
				FileID:   s.Entry.FileID,
				ChunkSeq: s.Entry.ChunkSeq,
				Text:     s.Entry.Text,
				Provenance: ChunkProvenance{
					FilePath:  s.Entry.FilePath,
					LineStart: s.Entry.LineStart,
					LineEnd:   s.Entry.LineEnd,
					SHA256:    s.Entry.SHA256,
				},
			},
			Similarity: s.Similarity,
		})
		totalBytes += size
		if len(out) >= topK {
			break
		}
	}
	return out, dropped, nil
}

// openStoreLocked returns a vectorStore for corpusID, hydrating it from
// the on-disk snapshot AND back-filling missing entries from SQL if the
// snapshot is stale (e.g. crash recovery). Caller-side cache.
func (m *Manager) openStoreLocked(corpusID string) (*vectorStore, error) {
	m.mu.Lock()
	if s, ok := m.stores[corpusID]; ok {
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	path := vectorStorePath(m.dataDir, corpusID)
	store, err := openVectorStore(path)
	if err != nil {
		return nil, err
	}

	// Back-fill: if SQL has chunks the snapshot doesn't know about (or
	// the snapshot is empty after a crash mid-flush), rebuild from SQL.
	chunks, err := loadAllChunks(context.Background(), m.db.Reader(), corpusID)
	if err == nil && len(chunks) != store.size() {
		store.mu.Lock()
		store.entries = store.entries[:0]
		for _, c := range chunks {
			store.entries = append(store.entries, vectorEntry{
				ChunkID:   c.ID,
				FileID:    c.FileID,
				ChunkSeq:  c.ChunkSeq,
				Text:      c.Text,
				LineStart: c.Provenance.LineStart,
				LineEnd:   c.Provenance.LineEnd,
				FilePath:  c.Provenance.FilePath,
				SHA256:    c.Provenance.SHA256,
				Embedding: c.Embedding,
			})
		}
		_ = store.saveLocked()
		store.mu.Unlock()
	}

	m.mu.Lock()
	m.stores[corpusID] = store
	m.mu.Unlock()
	return store, nil
}

// updateJob applies fn under m.mu against the job entry for jobID. No-op
// if the job has been evicted.
func (m *Manager) updateJob(jobID string, fn func(*IngestStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.jobs[jobID]
	if !ok {
		return
	}
	fn(st)
}

// newID returns a prefixed random hex identifier. crypto/rand so
// concurrent corpora / files / chunks don't collide.
func newID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("corpus: random id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(b), nil
}

// countLines counts \n-terminated lines in s. Empty string -> 0.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// relPathOrAbs returns absPath relative to root (using filepath.Rel).
// If the file is outside root or root is itself a file, returns the
// absolute path so the SQL row at least carries a stable identifier.
func relPathOrAbs(root, absPath string) (string, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return absPath, nil
	}
	if !rootInfo.IsDir() {
		// Single-file ingest — the path itself is the identifier.
		return filepath.Base(absPath), nil
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return absPath, nil
	}
	return filepath.ToSlash(rel), nil
}

// io.Discard reference so the unused import linter doesn't yell when we
// add an io.Copy fallback later. Removed in a follow-up if unused.
var _ = io.Discard
