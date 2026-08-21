package log

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when a primary-key lookup misses.
var ErrNotFound = errors.New("log: not found")

// Row is the unexported-to-callers row representation. Only this
// package and core/event use it.
type Row struct {
	EventID          string
	SessionID        string // empty for non-session events
	EmitterID        string
	Kind             string
	EmittedAt        time.Time
	Payload          []byte
	PayloadHash      [32]byte
	PrevHash         [32]byte
	RedactionSummary string
	// SchemaVersion is the payload schema version stored with the row.
	// 0 means the field was not populated (legacy row); treated as 1.
	SchemaVersion int
}

// Backend abstracts the storage-foundations connection. The real
// implementation will be backed by libSQL via core/storage; this
// interface keeps the store package independent of any particular
// driver and lets tests run against an in-memory backend.
type Backend interface {
	// AppendRow inserts a row into the events table and updates the
	// chain head for its session under a single transaction. If
	// expectedHead does not match the cached head for the session
	// the call returns ErrChainHeadMismatch (optimistic concurrency).
	AppendRow(ctx context.Context, row Row, expectedHead [32]byte) error

	// GetRow returns a row by primary key.
	GetRow(ctx context.Context, eventID string) (Row, error)

	// HeadFor returns the cached chain head for a session. Returns
	// (zero, false) if the session has no events yet.
	HeadFor(ctx context.Context, sessionID string) ([32]byte, string, bool, error)

	// SelectBySession returns rows for sid, ordered by event_id.
	SelectBySession(ctx context.Context, sid string, after string, limit int, reverse bool) ([]Row, error)

	// SelectByKind returns rows matching kind.
	SelectByKind(ctx context.Context, kind string, after string, limit int, reverse bool) ([]Row, error)

	// SelectByEmitter returns rows matching emitter id.
	SelectByEmitter(ctx context.Context, emitter string, after string, limit int, reverse bool) ([]Row, error)

	// SelectByTimeRange returns rows whose emitted_at is within [from, to].
	SelectByTimeRange(ctx context.Context, from, to time.Time, after string, limit int, reverse bool) ([]Row, error)

	// SearchFTS performs a content-search over the redacted payloads.
	SearchFTS(ctx context.Context, query string, sessionFilter string, kindFilter []string, limit int) ([]Row, error)

	// AllSessionIDs returns the distinct set of session ids in the
	// table. Used by VerifyAll.
	AllSessionIDs(ctx context.Context) ([]string, error)

	// SizeBytes returns an approximation of total payload bytes.
	SizeBytes(ctx context.Context) (int64, error)
}

// ErrChainHeadMismatch is returned by AppendRow when the supplied
// expectedHead does not match the backend's cached head for the
// session — i.e. another writer raced ahead.
var ErrChainHeadMismatch = errors.New("log: chain head mismatch")

// Store is the unexported (lowercase) storage adapter. Construction
// is via NewStore; outside of core/event nothing should hold a
// pointer to a Store.
type Store struct {
	backend Backend
}

// NewStore wraps a backend. The backend must already have its
// migrations applied.
func NewStore(b Backend) *Store {
	if b == nil {
		panic("log: nil backend")
	}
	return &Store{backend: b}
}

// Append wraps Backend.AppendRow for callers in core/event.
func (s *Store) Append(ctx context.Context, row Row, expectedHead [32]byte) error {
	return s.backend.AppendRow(ctx, row, expectedHead)
}

// appendComputedMaxAttempts bounds AppendComputed's optimistic-
// concurrency retry loop. Found necessary, not theoretical: every
// existing Push call site (core/rpc/api.go's eight bridge types) has no
// SessionID, so every Push-sourced row shares ONE chain ("" — see
// rowFromEntry in core/rpc/views/audit/impl.go), and under concurrent
// pushes that chain is genuinely contended. Observed directly under
// -race with 20 concurrent Push calls: WITHOUT this retry loop, up to
// 15/20 (75%) lost their write to ErrChainHeadMismatch and were
// silently dropped per Push's D-5 contract — correct per that contract,
// but "most concurrent audit rows silently vanish under load" is not
// an acceptable property for the mission this unit exists to build.
//
// 8 was the first bound tried and was NOT enough — a 20-goroutine run
// under -race still exhausted it for 1/20 pushes (core/rpc/views/audit's
// TestPush_ConcurrentGoroutinesWithStore, which asserts exact parity
// between what was pushed and what persisted). Each attempt is one
// local SQLite transaction against a single-connection database with a
// 5s busy_timeout (sqlite.go) — cheap — so a generous bound costs
// little in the pathological case and buys real headroom for the
// common one.
const appendComputedMaxAttempts = 64

// AppendComputed inserts row, computing PrevHash from the session's
// current chain head and PayloadHash from PrevHash+Kind+EmittedAt+Payload
// (chain.go's canonicalSerialize formula — the same one VerifyChain
// recomputes later) instead of requiring the caller to track chain
// state itself. Any PrevHash/PayloadHash already set on row are
// IGNORED and overwritten.
//
// Retries up to appendComputedMaxAttempts times on ErrChainHeadMismatch
// — a concurrent writer to the same session raced ahead between this
// call's head read and its append; re-reading the (now current) head
// and retrying is the standard resolution for optimistic concurrency,
// and is cheap here (one local SQLite transaction per attempt, no
// network round trip). Any other error returns immediately.
//
// This is the write path core/rpc/views/audit.API.Push uses once a
// Store is configured (audit-that-tells-the-truth-01PMZA10 UNIT-4) —
// callers there (core/rpc/api.go's eight bridge types) have no session
// context and no notion of a hash chain; they supply an EventID, Kind,
// EmittedAt and a payload, and this method does the rest.
func (s *Store) AppendComputed(ctx context.Context, row Row) error {
	var lastErr error
	for attempt := 0; attempt < appendComputedMaxAttempts; attempt++ {
		prevHash, _, _, err := s.backend.HeadFor(ctx, row.SessionID)
		if err != nil {
			return fmt.Errorf("log: AppendComputed: HeadFor: %w", err)
		}
		candidate := row
		candidate.PrevHash = prevHash
		candidate.PayloadHash = recomputeHash(candidate)
		err = s.backend.AppendRow(ctx, candidate, prevHash)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrChainHeadMismatch) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("log: AppendComputed: exhausted %d attempts under chain-head contention: %w",
		appendComputedMaxAttempts, lastErr)
}

// Get is the primary-key point read.
func (s *Store) Get(ctx context.Context, eventID string) (Row, error) {
	return s.backend.GetRow(ctx, eventID)
}

// Head returns the cached head for a session.
func (s *Store) Head(ctx context.Context, sid string) ([32]byte, string, bool, error) {
	return s.backend.HeadFor(ctx, sid)
}

// BySession proxies to the backend.
func (s *Store) BySession(ctx context.Context, sid string, after string, limit int, reverse bool) ([]Row, error) {
	return s.backend.SelectBySession(ctx, sid, after, limit, reverse)
}

// ByKind proxies to the backend.
func (s *Store) ByKind(ctx context.Context, kind string, after string, limit int, reverse bool) ([]Row, error) {
	return s.backend.SelectByKind(ctx, kind, after, limit, reverse)
}

// ByEmitter proxies to the backend.
func (s *Store) ByEmitter(ctx context.Context, emitter string, after string, limit int, reverse bool) ([]Row, error) {
	return s.backend.SelectByEmitter(ctx, emitter, after, limit, reverse)
}

// ByTimeRange proxies to the backend.
func (s *Store) ByTimeRange(ctx context.Context, from, to time.Time, after string, limit int, reverse bool) ([]Row, error) {
	return s.backend.SelectByTimeRange(ctx, from, to, after, limit, reverse)
}

// Search proxies to the backend.
func (s *Store) Search(ctx context.Context, query string, sessionFilter string, kindFilter []string, limit int) ([]Row, error) {
	return s.backend.SearchFTS(ctx, query, sessionFilter, kindFilter, limit)
}

// AllSessionIDs returns the distinct sessions present in the store.
func (s *Store) AllSessionIDs(ctx context.Context) ([]string, error) {
	return s.backend.AllSessionIDs(ctx)
}

// SizeBytes returns a payload-size approximation. Used by retention.
func (s *Store) SizeBytes(ctx context.Context) (int64, error) {
	return s.backend.SizeBytes(ctx)
}

// VerifyChain walks the persisted hash chain for events with event_id
// in [fromID, toID] and reports whether it is intact. Proxies to the
// package-level VerifyChain (chain.go) against this Store's backend —
// the real, persisted-data verifier audit-that-tells-the-truth-01PMZA10
// UNIT-7 routes views/audit/impl.go's VerifyChain to, replacing the
// ring-only implementation that always reported Verified: true.
func (s *Store) VerifyChain(ctx context.Context, fromID, toID string) (VerifyChainResult, error) {
	return VerifyChain(ctx, s.backend, fromID, toID)
}

// MemoryBackend is an in-memory Backend used by tests and (until the
// real storage-foundations adapter lands) as the default development
// backend. It serializes writes per session via a sync.Map of mutexes
// so per-session chain-head locking matches the production semantics.
type MemoryBackend struct {
	mu        sync.RWMutex
	rows      map[string]Row // by event_id
	heads     map[string]headState
	sessLocks sync.Map // map[string]*sync.Mutex per session id (incl. "" for headless)
	totalSize int64
}

type headState struct {
	headID   string
	headHash [32]byte
}

// NewMemoryBackend constructs an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		rows:  make(map[string]Row),
		heads: make(map[string]headState),
	}
}

func (m *MemoryBackend) lockFor(sessionID string) *sync.Mutex {
	v, _ := m.sessLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// AppendRow implements Backend.
func (m *MemoryBackend) AppendRow(ctx context.Context, row Row, expectedHead [32]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mu := m.lockFor(row.SessionID)
	mu.Lock()
	defer mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.heads[row.SessionID]; ok {
		if h.headHash != expectedHead {
			return fmt.Errorf("%w: session %s", ErrChainHeadMismatch, row.SessionID)
		}
	} else if expectedHead != ([32]byte{}) {
		return fmt.Errorf("%w: session %s expected zero head", ErrChainHeadMismatch, row.SessionID)
	}
	if _, exists := m.rows[row.EventID]; exists {
		return fmt.Errorf("log: event_id collision: %s", row.EventID)
	}
	cp := row
	cp.Payload = append([]byte(nil), row.Payload...)
	m.rows[row.EventID] = cp
	m.heads[row.SessionID] = headState{headID: row.EventID, headHash: row.PayloadHash}
	m.totalSize += int64(len(cp.Payload))
	return nil
}

// GetRow implements Backend.
func (m *MemoryBackend) GetRow(ctx context.Context, eventID string) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rows[eventID]
	if !ok {
		return Row{}, ErrNotFound
	}
	cp := r
	cp.Payload = append([]byte(nil), r.Payload...)
	return cp, nil
}

// HeadFor implements Backend.
func (m *MemoryBackend) HeadFor(ctx context.Context, sessionID string) ([32]byte, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return [32]byte{}, "", false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.heads[sessionID]
	if !ok {
		return [32]byte{}, "", false, nil
	}
	return h.headHash, h.headID, true, nil
}

func (m *MemoryBackend) selectFiltered(filter func(Row) bool, after string, limit int, reverse bool) []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Row, 0, len(m.rows))
	for _, r := range m.rows {
		if !filter(r) {
			continue
		}
		if after != "" {
			if !reverse && r.EventID <= after {
				continue
			}
			if reverse && r.EventID >= after {
				continue
			}
		}
		cp := r
		cp.Payload = append([]byte(nil), r.Payload...)
		out = append(out, cp)
	}
	if reverse {
		sort.Slice(out, func(i, j int) bool { return out[i].EventID > out[j].EventID })
	} else {
		sort.Slice(out, func(i, j int) bool { return out[i].EventID < out[j].EventID })
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// SelectBySession implements Backend.
func (m *MemoryBackend) SelectBySession(ctx context.Context, sid string, after string, limit int, reverse bool) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.selectFiltered(func(r Row) bool { return r.SessionID == sid }, after, limit, reverse), nil
}

// SelectByKind implements Backend.
func (m *MemoryBackend) SelectByKind(ctx context.Context, kind string, after string, limit int, reverse bool) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.selectFiltered(func(r Row) bool { return r.Kind == kind }, after, limit, reverse), nil
}

// SelectByEmitter implements Backend.
func (m *MemoryBackend) SelectByEmitter(ctx context.Context, emitter string, after string, limit int, reverse bool) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.selectFiltered(func(r Row) bool { return r.EmitterID == emitter }, after, limit, reverse), nil
}

// SelectByTimeRange implements Backend.
func (m *MemoryBackend) SelectByTimeRange(ctx context.Context, from, to time.Time, after string, limit int, reverse bool) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.selectFiltered(func(r Row) bool {
		if !from.IsZero() && r.EmittedAt.Before(from) {
			return false
		}
		if !to.IsZero() && r.EmittedAt.After(to) {
			return false
		}
		return true
	}, after, limit, reverse), nil
}

// SearchFTS implements a naive substring search over the canonical
// payload bytes — not FTS5, but functionally compatible for tests.
// The real backend will dispatch to SQLite FTS5.
func (m *MemoryBackend) SearchFTS(ctx context.Context, query string, sessionFilter string, kindFilter []string, limit int) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	kf := make(map[string]struct{}, len(kindFilter))
	for _, k := range kindFilter {
		kf[k] = struct{}{}
	}
	rows := m.selectFiltered(func(r Row) bool {
		if sessionFilter != "" && r.SessionID != sessionFilter {
			return false
		}
		if len(kf) > 0 {
			if _, ok := kf[r.Kind]; !ok {
				return false
			}
		}
		return strings.Contains(strings.ToLower(string(r.Payload)), q)
	}, "", limit, false)
	return rows, nil
}

// AllSessionIDs implements Backend.
func (m *MemoryBackend) AllSessionIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]struct{}, len(m.rows))
	for _, r := range m.rows {
		seen[r.SessionID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// SizeBytes implements Backend.
func (m *MemoryBackend) SizeBytes(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalSize, nil
}

// TamperPayloadForTest mutates the payload bytes of a single row in
// place. ONLY for tamper-detection tests; never used outside _test.go.
// Build tag would be ideal; an exported method named TamperPayloadForTest
// keeps the use restricted by convention.
func (m *MemoryBackend) TamperPayloadForTest(eventID string, mutator func([]byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[eventID]
	if !ok {
		return ErrNotFound
	}
	mutator(r.Payload)
	m.rows[eventID] = r
	return nil
}

// DeleteForTest removes a row directly. ONLY for truncation-detection
// tests. Bypasses the normal append-only invariant on purpose.
func (m *MemoryBackend) DeleteForTest(eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[eventID]; !ok {
		return ErrNotFound
	}
	delete(m.rows, eventID)
	return nil
}

// DeleteRows removes rows by event_id. Implements SweepableBackend.
// This method bypasses the append-only invariant intentionally — it is
// the authorised path for retention sweeps and bulk purges which have
// already archived the rows.
func (m *MemoryBackend) DeleteRows(ctx context.Context, eventIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range eventIDs {
		delete(m.rows, id)
	}
	return nil
}
