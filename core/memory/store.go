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

// ErrEmbeddingRequired is returned by Add for a chunk with no embedding
// that is not explicitly EmbedPending.
var ErrEmbeddingRequired = errors.New("memory: chunk embedding required")

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

// ScopePromoter is the optional capability the rpc/memory view uses to
// change a chunk's scope ("promote to project/global/long_term", or a
// demotion). The scope changes in place and the id is kept — it is the
// chunk's origin id for Fleet memory sync (memory-sync-01MEMSY01 WP03).
type ScopePromoter interface {
	PromoteScope(ctx context.Context, id, newScopeKind, newScopeID string) error
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
	// clock stamps per-field HLCs on every local mutation
	// (memory-sync-01MEMSY01 WP02). nil ⇒ fields stay unstamped and the
	// sync client stamps them on first push.
	clock *HLC
}

// ClockSetter is the optional capability the rpc wiring uses to install
// the per-install HLC (memory-sync-01MEMSY01 WP02). The chromem store
// implements it; the clock is constructed by the rpc layer from the fleet
// node id so core/memory never imports core/fleet.
type ClockSetter interface {
	SetClock(c *HLC)
}

// SetClock installs (or replaces) the HLC. nil disables stamping.
func (s *chromemStore) SetClock(c *HLC) {
	s.mu.Lock()
	s.clock = c
	s.mu.Unlock()
}

// tickLocked returns a fresh HLC, or "" when no clock is installed.
func (s *chromemStore) tickLocked() string {
	if s.clock == nil {
		return ""
	}
	return s.clock.Tick()
}

// touchLocked records a local mutation for sync: the chunk needs a push
// and any in-flight push result must not clear that need.
func touchLocked(c *Chunk) {
	c.SyncDirty = true
	c.SyncGen++
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
	// Memory-sync addendum (memory-sync-01MEMSY01 WP02/WP05): legacy
	// RecallCount becomes RecallOwn; RecallCount is re-derived as the sum.
	// Every other sync field's zero value is already correct.
	normalizeRecall(c)
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
	// The embedding invariant holds for every user/capture path. The one
	// explicit exception (memory-sync-01MEMSY01 WP06, H5) is a chunk that
	// says so: EmbedPending — content that arrived without a vector
	// (embeddings never travel over Fleet memory sync) and will be indexed
	// by DrainEmbedPending when a real embedder exists.
	if len(chunk.Embedding) == 0 && !chunk.EmbedPending {
		return ErrEmbeddingRequired
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
	normalizeRecall(&chunk)
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
			s.chunks[i] = s.replaceLocked(c, chunk)
			return s.saveLocked()
		}
		if c.ContentHash == chunk.ContentHash &&
			c.ScopeKind == chunk.ScopeKind &&
			c.ScopeID == chunk.ScopeID &&
			c.CreatedAt.After(cutoff) {
			return ErrDuplicate
		}
	}
	s.stampCreateLocked(&chunk)
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

// stampCreateLocked stamps a net-new chunk: one tick becomes CreatedHLC and
// the HLC of every LWW field set at capture (title, pinned, scope_kind) —
// a new Fleet record must carry fields.scope_kind. Caller-supplied stamps
// are kept (the sync path inserts pulled chunks with their own HLCs).
func (s *chromemStore) stampCreateLocked(c *Chunk) {
	if c.CreatedHLC == "" {
		c.CreatedHLC = s.tickLocked()
	}
	if c.ScopeHLC == "" {
		c.ScopeHLC = c.CreatedHLC
	}
	if c.TitleHLC == "" && c.Title != "" {
		c.TitleHLC = c.CreatedHLC
	}
	if c.PinnedHLC == "" && c.Pinned {
		c.PinnedHLC = c.CreatedHLC
	}
	touchLocked(c)
}

// replaceLocked implements Add's same-id "replaces wholesale" contract
// without losing the sync identity of the row: the origin stamp, recall
// G-counter and Fleet bookkeeping carry over from the stored row, and each
// LWW field whose value changed is re-stamped so the edit wins on Fleet.
func (s *chromemStore) replaceLocked(old, in Chunk) Chunk {
	out := in
	out.CreatedHLC = old.CreatedHLC
	if out.CreatedHLC == "" {
		out.CreatedHLC = in.CreatedHLC
	}
	out.SyncSentAt, out.SyncedAt, out.SyncBlocked, out.SyncGen = old.SyncSentAt, old.SyncedAt, old.SyncBlocked, old.SyncGen
	if in.RecallOwn == 0 && in.RecallOthers == 0 && in.RecallFolded == 0 {
		out.RecallOwn, out.RecallOthers, out.RecallFolded = old.RecallOwn, old.RecallOthers, old.RecallFolded
	}
	normalizeRecall(&out)
	stamp := func(changed bool, cur *string, prev string) {
		switch {
		case changed:
			*cur = s.tickLocked()
		case prev != "":
			*cur = prev
		}
	}
	stamp(in.Title != old.Title, &out.TitleHLC, old.TitleHLC)
	stamp(in.Pinned != old.Pinned, &out.PinnedHLC, old.PinnedHLC)
	stamp(in.ScopeKind != old.ScopeKind, &out.ScopeHLC, old.ScopeHLC)
	touchLocked(&out)
	return out
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

// Remove deletes id and returns the removed row's final state, atomically
// under s.mu (ChunkRemover, memory-sync-01MEMSY01 WP04).
func (s *chromemStore) Remove(_ context.Context, id string) (Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.chunks {
		if c.ID == id {
			prev := s.chunks
			s.chunks = append(append([]Chunk(nil), s.chunks[:i]...), s.chunks[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.chunks = prev
				return Chunk{}, err
			}
			return c, nil
		}
	}
	return Chunk{}, fmt.Errorf("memory: chunk %q not found", id)
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
		if len(c.Embedding) == 0 || len(c.Embedding) != len(embedding) {
			// Mismatched dims usually means the user switched embedder
			// models; an empty vector is an EmbedPending chunk (pulled,
			// not yet indexed). Skip either instead of crashing the
			// query — the chunk stays visible to List / prelude.
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

// PromoteScope moves chunk id to the (kind, id) scope IN PLACE: the id is
// the chunk's origin id on Fleet and must survive a scope change
// (memory-sync-01MEMSY01 WP03, contract H1 — "promote keeps the id and
// pushes a scope_kind field with a fresh HLC"). Until v0.91.0 this minted a
// new id and deleted the old row, which on Fleet would have read as a new
// record plus an orphan. The scope change is stamped (ScopeHLC) and marked
// for push; content, embedding, recall and pin state are untouched. A
// no-op move (same kind + id) changes nothing.
//
// Demotion is the same operation: moving a synced chunk out of the synced
// scopes pushes the scope_kind change, Fleet tombstones it left_sync_scope
// and other devices delete their copies, while this device keeps its chunk
// (ruling OQ-B).
//
// TODO(audit-wired): emit a `memory.scoped` audit event after a
// successful promote. Payload: {chunk_id, scope_kind, scope_id}. Same
// emitter-not-wired blocker as projects.Create and attachments.Add — a
// process-wide event.Emitter has not been threaded through the rpc layer
// yet.
func (s *chromemStore) PromoteScope(_ context.Context, id, newScopeKind, newScopeID string) error {
	if id == "" {
		return errors.New("memory: promote: id required")
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
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("memory: chunk %q not found", id)
	}
	prev := s.chunks[idx]
	if prev.ScopeKind == newScopeKind && prev.ScopeID == newScopeID {
		return nil
	}
	moved := prev
	moved.ScopeKind = newScopeKind
	moved.ScopeID = newScopeID
	if newScopeKind == ScopeKindProject {
		moved.ProjectID = newScopeID
	}
	moved.ScopeHLC = s.tickLocked()
	touchLocked(&moved)
	s.chunks[idx] = moved
	if err := s.saveLocked(); err != nil {
		s.chunks[idx] = prev
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
			s.chunks[i].PinnedHLC = s.tickLocked()
			touchLocked(&s.chunks[i])
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
		// G-counter split (memory-sync-01MEMSY01): a recall on this
		// device bumps this device's own counter; RecallCount is the
		// derived total. Recall is pushed (recall_own) but never ticks
		// the HLC — counters merge by max, not by LWW.
		s.chunks[i].RecallOwn++
		normalizeRecall(&s.chunks[i])
		s.chunks[i].LastAccessed = at
		touchLocked(&s.chunks[i])
		dirty = true
	}
	if !dirty {
		return nil
	}
	return s.saveLocked()
}

// EmbeddingSetter is the optional capability DrainEmbedPending uses to
// index an EmbedPending chunk (memory-sync-01MEMSY01 WP06). It clears
// EmbedPending and does not schedule a push (embeddings never sync).
type EmbeddingSetter interface {
	SetEmbedding(ctx context.Context, id string, vec []float32) error
}

// SetEmbedding implements EmbeddingSetter.
func (s *chromemStore) SetEmbedding(_ context.Context, id string, vec []float32) error {
	if len(vec) == 0 {
		return errors.New("memory: set embedding: empty vector")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.chunks {
		if s.chunks[i].ID == id {
			s.chunks[i].Embedding = vec
			s.chunks[i].EmbedPending = false
			return s.saveLocked()
		}
	}
	return fmt.Errorf("memory: chunk %q not found", id)
}

// ScopeDeleter is the optional capability that deletes every chunk in one
// scope with a single save (memory-sync-01MEMSY01 WP09: session delete
// cascades to its session-scoped memory).
type ScopeDeleter interface {
	DeleteScope(ctx context.Context, scope ScopeFilter) ([]string, error)
}

// DeleteScope implements ScopeDeleter. An empty Kind matches nothing (a
// scope-less call must never wipe the store).
func (s *chromemStore) DeleteScope(_ context.Context, scope ScopeFilter) ([]string, error) {
	if scope.Kind == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	kept := make([]Chunk, 0, len(s.chunks))
	for _, c := range s.chunks {
		if matchesScope(c, []ScopeFilter{scope}) {
			gone = append(gone, c.ID)
			continue
		}
		kept = append(kept, c)
	}
	if len(gone) == 0 {
		return nil, nil
	}
	prev := s.chunks
	s.chunks = kept
	if err := s.saveLocked(); err != nil {
		s.chunks = prev
		return nil, err
	}
	return gone, nil
}

// DeleteSessionMemory removes the session-scoped chunks of a deleted
// session (memory-sync-01MEMSY01 WP09, H10). Chunks the user promoted
// out of the session (project / global / long_term) are not the
// session's anymore and are kept. Session scope never syncs, so nothing
// is forgotten on Fleet. Returns the removed ids.
func DeleteSessionMemory(ctx context.Context, store Store, sessionID string) ([]string, error) {
	if store == nil || sessionID == "" {
		return nil, nil
	}
	scope := ScopeFilter{Kind: ScopeKindSession, ID: sessionID}
	if d, ok := store.(ScopeDeleter); ok {
		return d.DeleteScope(ctx, scope)
	}
	chunks, err := store.List(ctx, scope)
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, c := range chunks {
		if err := store.Delete(ctx, c.ID); err != nil {
			return gone, err
		}
		gone = append(gone, c.ID)
	}
	return gone, nil
}

// RecallFolder is the optional capability prune.Apply uses to persist a
// collapse survivor's inherited metadata (memory-sync-01MEMSY01 WP05).
// FoldRecall adds n to the survivor's display-only RecallFolded and raises
// LastAccessed to at when later. It does not mark the chunk for push:
// nothing it changes is a pushed counter.
type RecallFolder interface {
	FoldRecall(ctx context.Context, id string, n int, at time.Time) error
}

// FoldRecall implements RecallFolder.
func (s *chromemStore) FoldRecall(_ context.Context, id string, n int, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.chunks {
		if s.chunks[i].ID != id {
			continue
		}
		if n > 0 {
			s.chunks[i].RecallFolded += n
			normalizeRecall(&s.chunks[i])
		}
		if at.After(s.chunks[i].LastAccessed) {
			s.chunks[i].LastAccessed = at
		}
		return s.saveLocked()
	}
	return fmt.Errorf("memory: chunk %q not found", id)
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
