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
	"time"
)

// DedupWindow is the look-back window inside which an Add with a
// matching ContentHash + scope is rejected as a duplicate. Mirrors
// claude-mem's behaviour: the same content can be re-pinned later.
const DedupWindow = 30 * time.Second

// ErrDuplicate is returned by Add when a chunk with the same scope +
// content hash was added inside DedupWindow.
var ErrDuplicate = errors.New("memory: duplicate chunk within dedup window")

// Store is the long-term-memory persistence + retrieval contract.
type Store interface {
	Add(ctx context.Context, chunk Chunk) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, scopes ...ScopeFilter) ([]Chunk, error)
	Query(ctx context.Context, embedding []float32, k int, scopes ...ScopeFilter) ([]Result, error)
	Close() error
}

// ScopePromoter is the optional capability the rpc/memory view uses
// to implement move semantics for "promote to project/global". The
// chromem-go-replacement store implements it; future backends should
// satisfy this interface so the view layer keeps the same contract.
type ScopePromoter interface {
	PromoteScope(ctx context.Context, oldID, newID, newScopeKind, newScopeID string) error
}

// PruneCapable is the optional capability the prune sweep uses to
// mark recall hits, pin/unpin chunks, and bulk-delete based on the
// pruner's verdict. Stores that don't implement it fall through to
// per-id Delete and the slower path the inspector RPC uses today.
//
// SetPinned with pinned=true makes the chunk immune to the prune
// sweep (FR-028). MarkAccessed bumps RecallCount and updates
// LastAccessed atomically — the retriever should call it after every
// successful read so the prune sweep's recall-frequency signal
// reflects real usage.
type PruneCapable interface {
	SetPinned(ctx context.Context, id string, pinned bool) error
	MarkAccessed(ctx context.Context, ids []string, at time.Time) error
}

// chromemStore is the gob-snapshot-backed Store. The name is kept
// generic so a future chromem-go-backed implementation can replace it
// without touching callers.
type chromemStore struct {
	mu     sync.RWMutex
	path   string
	chunks []Chunk
	now    func() time.Time
	// gate is an optional Cedar memory-write check; consulted on
	// every Add. nil ⇒ no policy enforcement (the AllowAll fallback
	// is the boot-stage default; production callers swap in a real
	// Engine via SetGate). Bundle E bonus — gate-hook wiring per the
	// WP14 report.
	gate MemoryWriteGate
}

// MemoryWriteGate is the narrow interface chromemStore consults on
// every Add. core/policy/cedar.Gate satisfies it via CheckMemoryWrite
// adapter; tests can pass a stub. The interface lives here to avoid
// pulling cedar into core/memory's import graph (DIRECTIVE_001).
type MemoryWriteGate interface {
	CheckWrite(ctx context.Context, scope string) error
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
	s := &chromemStore{path: path, now: time.Now}
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
	for i := range chunks {
		backfillChunkDefaults(&chunks[i])
	}
	s.chunks = chunks
	return nil
}

// backfillChunkDefaults applies the WP06 schema defaults to a chunk
// loaded from a pre-WP06 gob: missing scope fields fall back to the
// session scope keyed on SessionID; missing content hash is computed.
//
// Bundle E WP15 addendum: greedy-memory metadata (LastAccessed) was
// added without a schema migration, so legacy chunks read back with a
// zero LastAccessed; default it to CreatedAt so the staleness signal
// treats them as "as old as their creation" rather than "infinitely
// stale" (which would prune everything on first sweep).
//
// Narrative-layer addendum (memory-narrative-layer-01KQ8TD1 WP01):
// Kind and RetrievalWeight were added without a gob schema migration.
// Empty Kind defaults to "raw"; zero RetrievalWeight defaults to 1.0
// so legacy chunks are transparent to the narrative-weighted retriever.
func backfillChunkDefaults(c *Chunk) {
	if c.ScopeKind == "" {
		c.ScopeKind = ScopeKindSession
		if c.ScopeID == "" {
			c.ScopeID = c.SessionID
		}
	}
	if c.ContentHash == "" {
		c.ContentHash = HashContent(c.Content)
	}
	if c.LastAccessed.IsZero() {
		c.LastAccessed = c.CreatedAt
	}
	// Narrative-layer backfill (WP01).
	if c.Kind == "" {
		c.Kind = "raw"
	}
	if c.RetrievalWeight == 0 {
		c.RetrievalWeight = 1.0
	}
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

func (s *chromemStore) Add(ctx context.Context, chunk Chunk) error {
	if chunk.ID == "" {
		return errors.New("memory: chunk id required")
	}
	if len(chunk.Embedding) == 0 {
		return errors.New("memory: chunk embedding required")
	}
	if chunk.ScopeKind == "" {
		chunk.ScopeKind = ScopeKindSession
		if chunk.ScopeID == "" {
			chunk.ScopeID = chunk.SessionID
		}
	}
	if chunk.ContentHash == "" {
		chunk.ContentHash = HashContent(chunk.Content)
	}
	if s.gate != nil {
		if err := s.gate.CheckWrite(ctx, chunk.ScopeKind); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-DedupWindow)
	for i, c := range s.chunks {
		if c.ID == chunk.ID {
			s.chunks[i] = chunk
			return s.saveLocked()
		}
		if c.ContentHash == chunk.ContentHash &&
			c.ScopeKind == chunk.ScopeKind &&
			c.ScopeID == chunk.ScopeID &&
			c.CreatedAt.After(cutoff) {
			return ErrDuplicate
		}
	}
	s.chunks = append(s.chunks, chunk)
	if err := s.saveLocked(); err != nil {
		return err
	}
	// Increment the capture-rate counter ONLY on a net-new write (not on
	// an ID-collision update above). The tracker is process-scoped; no
	// migration, no persistence.
	GlobalCaptureTracker().RecordWrite(s.now().UTC())
	return nil
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

func (s *chromemStore) List(_ context.Context, scopes ...ScopeFilter) ([]Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chunk, 0, len(s.chunks))
	for _, c := range s.chunks {
		if !matchesScope(c, scopes) {
			continue
		}
		out = append(out, c)
	}
	// Newest first so the management UI surfaces recent pins at the top.
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *chromemStore) Query(_ context.Context, embedding []float32, k int, scopes ...ScopeFilter) ([]Result, error) {
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
		if !matchesScope(c, scopes) {
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

// SetGate installs (or replaces) the memory-write policy gate. Called
// once at boot; passing nil disables the gate.
func (s *chromemStore) SetGate(g MemoryWriteGate) {
	s.mu.Lock()
	s.gate = g
	s.mu.Unlock()
}

// GateSetter is the optional capability tests + the rpc layer use to
// install a policy gate without re-opening the store. The chromem
// store implements it.
type GateSetter interface {
	SetGate(g MemoryWriteGate)
}

// PromoteScope deletes the chunk with oldID and inserts a copy with
// newID and the supplied (kind, id) scope. Atomic under s.mu — the
// store is observed in the pre- or post-state, never with both rows.
//
// TODO(audit-wired): emit a `memory.scoped` audit event after a
// successful promote. Payload: {old_chunk_id, new_chunk_id, scope_kind,
// scope_id}. Same emitter-not-wired blocker as projects.Create and
// attachments.Add — a process-wide event.Emitter has not been threaded
// through the rpc layer yet.
func (s *chromemStore) PromoteScope(_ context.Context, oldID, newID, newScopeKind, newScopeID string) error {
	if oldID == "" || newID == "" {
		return errors.New("memory: promote: ids required")
	}
	switch newScopeKind {
	case ScopeKindGlobal, ScopeKindProject, ScopeKindSession, ScopeKindLongTerm:
	default:
		return fmt.Errorf("memory: promote: invalid scope kind %q", newScopeKind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, c := range s.chunks {
		if c.ID == oldID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("memory: chunk %q not found", oldID)
	}
	moved := s.chunks[idx]
	moved.ID = newID
	moved.ScopeKind = newScopeKind
	moved.ScopeID = newScopeID
	if newScopeKind == ScopeKindProject {
		moved.ProjectID = newScopeID
	}
	prev := append([]Chunk(nil), s.chunks...)
	s.chunks = append(s.chunks[:idx], s.chunks[idx+1:]...)
	s.chunks = append(s.chunks, moved)
	if err := s.saveLocked(); err != nil {
		s.chunks = prev
		return err
	}
	return nil
}

// SetPinned sets or clears the Pinned flag on chunk id. Returns
// ErrNotFound when the id does not exist. Atomic under s.mu — the
// gob is rewritten before the call returns. Bundle E WP15.
func (s *chromemStore) SetPinned(_ context.Context, id string, pinned bool) error {
	if id == "" {
		return errors.New("memory: pin: id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.chunks {
		if s.chunks[i].ID == id {
			if s.chunks[i].Pinned == pinned {
				return nil
			}
			s.chunks[i].Pinned = pinned
			return s.saveLocked()
		}
	}
	return fmt.Errorf("memory: chunk %q not found", id)
}

// MarkAccessed bumps RecallCount + LastAccessed for every id in ids.
// Missing ids are silently skipped — recall is best-effort metadata,
// not a state machine; surfacing per-id errors here would force the
// retriever to single-thread its writes. Bundle E WP15.
func (s *chromemStore) MarkAccessed(_ context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dirty := false
	for i := range s.chunks {
		if _, ok := wanted[s.chunks[i].ID]; !ok {
			continue
		}
		s.chunks[i].RecallCount++
		s.chunks[i].LastAccessed = at
		dirty = true
	}
	if !dirty {
		return nil
	}
	return s.saveLocked()
}

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
