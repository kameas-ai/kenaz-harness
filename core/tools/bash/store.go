// In-memory bash output cache used by the kernel's read_bash_output
// executor. Each Tool.Call writes a Record keyed by run-id; the
// agentgraph BashOutputStore seam reads from the same store.
//
// The store is intentionally process-local: bash transcripts are
// transient (typical chat sessions retain output for a few minutes,
// not days). On-disk persistence is out of scope for this WP — when
// the harness restarts, prior bash run-ids are gone, which matches
// the spec's "best-effort" disposition for FR-057b.
package bash

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultStoreTTL is the per-record retention window. Records older
// than TTL are evicted on the next Get / Put pass so the in-memory
// table cannot grow unbounded across long-lived sessions.
const DefaultStoreTTL = 30 * time.Minute

// ErrNotFound is returned by Get when the requested run-id is not in
// the cache (never written, expired, or evicted).
var ErrNotFound = errors.New("bash: output not found")

// Record is one cached transcript. Mirrors agentgraph.BashOutput's
// shape but lives here so core/tools/bash has no dependency on
// core/agentgraph (DIRECTIVE_001 — wiring is one-directional).
type Record struct {
	RunID    string
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	StoredAt time.Time
}

// Store is the cache. Safe for concurrent use; the per-record write is
// O(1) and the eviction sweep is O(n) but runs only when an explicit
// Sweep / lookup-on-miss is invoked. The map is small in practice
// (single-digit entries per chat turn) so the linear sweep is fine.
type Store struct {
	mu      sync.Mutex
	records map[string]*Record
	ttl     time.Duration
	now     func() time.Time
}

// StoreOption tunes a Store at construction.
type StoreOption func(*Store)

// WithStoreTTL overrides the default eviction window.
func WithStoreTTL(ttl time.Duration) StoreOption {
	return func(s *Store) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// WithStoreClock pins the wall-clock source. Tests use this to
// deterministically expire records.
func WithStoreClock(now func() time.Time) StoreOption {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

// NewStore constructs an empty cache with default settings.
func NewStore(opts ...StoreOption) *Store {
	s := &Store{
		records: make(map[string]*Record),
		ttl:     DefaultStoreTTL,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Put records a transcript under runID. An existing record with the
// same id is overwritten (later runs win).
func (s *Store) Put(runID string, rec Record) {
	if s == nil || runID == "" {
		return
	}
	rec.RunID = runID
	rec.StoredAt = s.now()
	s.mu.Lock()
	s.records[runID] = &rec
	s.evictExpiredLocked()
	s.mu.Unlock()
}

// Get returns the record for runID. Returns ErrNotFound when the id
// is unknown or the record has expired (and is evicted in the same
// pass).
func (s *Store) Get(_ context.Context, runID string) (Record, error) {
	if s == nil {
		return Record{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[runID]
	if !ok {
		return Record{}, ErrNotFound
	}
	if s.expiredLocked(rec) {
		delete(s.records, runID)
		return Record{}, ErrNotFound
	}
	return *rec, nil
}

// Sweep evicts every expired record. Returns the count removed.
// Tests rely on this for deterministic eviction without churning the
// clock; production callers can ignore it.
func (s *Store) Sweep() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictExpiredLocked()
}

func (s *Store) evictExpiredLocked() int {
	removed := 0
	for id, r := range s.records {
		if s.expiredLocked(r) {
			delete(s.records, id)
			removed++
		}
	}
	return removed
}

func (s *Store) expiredLocked(r *Record) bool {
	if r == nil || s.ttl <= 0 {
		return false
	}
	return s.now().Sub(r.StoredAt) > s.ttl
}
