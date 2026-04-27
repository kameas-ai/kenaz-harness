package corpus

import (
	"encoding/gob"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// vectorEntry is one row in the per-corpus on-disk index. Mirrors the
// SQL row but lives at <DataDir>/corpora/<corpus_id>/vectors.gob so the
// retrieval path can do cosine top-K without going through SQL.
type vectorEntry struct {
	ChunkID    string
	FileID     string
	ChunkSeq   int
	Text       string
	LineStart  int
	LineEnd    int
	FilePath   string
	SHA256     string
	Embedding  []float32
}

// vectorStore is the in-process index for one corpus. The on-disk gob
// is rewritten atomically after each ingested file commits so a crash
// between files leaves the snapshot at most one file behind. Loaded
// lazily by manager.Retrieve / Manager.Open from <DataDir>/corpora/
// <corpus_id>/vectors.gob.
type vectorStore struct {
	mu       sync.RWMutex
	path     string
	entries  []vectorEntry
}

// openVectorStore opens (or creates) the on-disk store at path. A
// missing file is treated as an empty store; corruption surfaces as an
// error so the operator notices instead of silently re-ingesting.
func openVectorStore(path string) (*vectorStore, error) {
	if path == "" {
		return nil, errors.New("corpus: empty vectorstore path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("corpus: mkdir vectorstore parent: %w", err)
	}
	s := &vectorStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *vectorStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("corpus: open vectorstore: %w", err)
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var entries []vectorEntry
	if err := dec.Decode(&entries); err != nil {
		return fmt.Errorf("corpus: decode vectorstore: %w", err)
	}
	s.entries = entries
	return nil
}

// saveLocked writes the snapshot atomically. Caller MUST hold s.mu.
func (s *vectorStore) saveLocked() error {
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("corpus: write vectorstore tmp: %w", err)
	}
	enc := gob.NewEncoder(f)
	if err := enc.Encode(s.entries); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("corpus: encode vectorstore: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("corpus: close vectorstore tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("corpus: rename vectorstore: %w", err)
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// removeFile drops every entry whose FileID matches fileID. Used by
// the re-ingest path before adding the new file's chunks.
func (s *vectorStore) removeFile(fileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.entries[:0]
	for _, e := range s.entries {
		if e.FileID == fileID {
			continue
		}
		out = append(out, e)
	}
	s.entries = out
}

// addEntries appends new entries without persisting. flush MUST be
// called when the file's SQL commit succeeds.
func (s *vectorStore) addEntries(entries []vectorEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entries...)
}

// flush atomically writes the snapshot to disk.
func (s *vectorStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// query runs cosine top-K over the entries, returning at most k results.
// query is the embedded query vector; an entry whose dimensions don't
// match query is skipped so a model swap doesn't crash the retrieval.
func (s *vectorStore) query(qVec []float32, k int) []scoredEntry {
	if len(qVec) == 0 || k <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	results := make([]scoredEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if len(e.Embedding) != len(qVec) {
			continue
		}
		sim := cosineSimilarity(qVec, e.Embedding)
		results = append(results, scoredEntry{Entry: e, Similarity: sim})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
	if k < len(results) {
		results = results[:k]
	}
	return results
}

// size returns the number of entries (testing aid).
func (s *vectorStore) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// scoredEntry is the internal retrieval result before RetrievalResult
// materialization.
type scoredEntry struct {
	Entry      vectorEntry
	Similarity float32
}

// cosineSimilarity over float32 with a float64 accumulator. Returns 0
// for zero-magnitude inputs so callers can sort safely (vs NaN).
func cosineSimilarity(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// vectorStorePath returns the on-disk path for a corpus's vector
// snapshot under root.
func vectorStorePath(root, corpusID string) string {
	return filepath.Join(root, "corpora", corpusID, "vectors.gob")
}
