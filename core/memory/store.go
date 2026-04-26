// Long-term memory vector store. The Store interface lets the rpc
// layer treat the persistence and the in-RAM index as a single seam;
// the default implementation is a gob-encoded snapshot with a flat
// cosine-similarity scan.
//
// Why a flat scan instead of an external vector DB: the design notes
// called for chromem-go (pure-Go, file-backed). The harness build
// environment cannot fetch new module proxies, so we ship an in-house
// equivalent with the same on-disk + interface contract. Swapping in a
// chromem-go-backed implementation later is a one-file change because
// every caller goes through Store.
//
// Privacy posture: the file is created mode 0600. The embedding column
// is JSON-omitted (`json:"-"`) so when a future export path round-trips
// chunks through JSON, the user's vector representations stay local.
package memory

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store is the long-term-memory persistence + retrieval contract.
type Store interface {
	Add(ctx context.Context, chunk Chunk) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]Chunk, error)
	Query(ctx context.Context, embedding []float32, k int) ([]Result, error)
	Close() error
}

// chromemStore is the gob-snapshot-backed Store. The name is kept
// generic so a future chromem-go-backed implementation can replace it
// without touching callers.
type chromemStore struct {
	mu     sync.RWMutex
	path   string
	chunks []Chunk
}

// NewChromemStore opens (or creates) the on-disk vector DB at path.
// A missing file is treated as an empty store; a corrupt file surfaces
// as an error so the user sees the problem instead of silently losing
// every memory.
func NewChromemStore(path string) (Store, error) {
	if path == "" {
		return nil, errors.New("memory: empty store path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("memory: mkdir parent: %w", err)
	}
	s := &chromemStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *chromemStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("memory: open %s: %w", s.path, err)
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var chunks []Chunk
	if err := dec.Decode(&chunks); err != nil {
		return fmt.Errorf("memory: decode %s: %w", s.path, err)
	}
	s.chunks = chunks
	return nil
}

// saveLocked writes the current chunk slice to disk atomically. The
// caller MUST hold s.mu (write).
func (s *chromemStore) saveLocked() error {
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("memory: write tmp: %w", err)
	}
	enc := gob.NewEncoder(f)
	if err := enc.Encode(s.chunks); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: close tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename: %w", err)
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

func (s *chromemStore) Add(_ context.Context, chunk Chunk) error {
	if chunk.ID == "" {
		return errors.New("memory: chunk id required")
	}
	if len(chunk.Embedding) == 0 {
		return errors.New("memory: chunk embedding required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.chunks {
		if c.ID == chunk.ID {
			s.chunks[i] = chunk
			return s.saveLocked()
		}
	}
	s.chunks = append(s.chunks, chunk)
	return s.saveLocked()
}

func (s *chromemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.chunks {
		if c.ID == id {
			s.chunks = append(s.chunks[:i], s.chunks[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("memory: chunk %q not found", id)
}

func (s *chromemStore) List(_ context.Context) ([]Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chunk, len(s.chunks))
	copy(out, s.chunks)
	// Newest first so the management UI surfaces recent pins at the top.
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *chromemStore) Query(_ context.Context, embedding []float32, k int) ([]Result, error) {
	if len(embedding) == 0 {
		return nil, errors.New("memory: empty query embedding")
	}
	if k <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	results := make([]Result, 0, len(s.chunks))
	for _, c := range s.chunks {
		if len(c.Embedding) != len(embedding) {
			// Mismatched dims usually means the user switched embedder
			// models; skip the row instead of crashing the query.
			continue
		}
		sim := cosineSimilarity(embedding, c.Embedding)
		results = append(results, Result{Chunk: c, Similarity: sim})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
	if k < len(results) {
		results = results[:k]
	}
	return results, nil
}

func (s *chromemStore) Close() error { return nil }

// cosineSimilarity computes the cosine of the angle between a and b.
// Returns 0 for zero-magnitude inputs (instead of NaN) so callers can
// safely sort the result.
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
