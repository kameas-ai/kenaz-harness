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
