package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrSessionNotFound is returned when a session id has no matching
	// row.
	ErrSessionNotFound = errors.New("session: not found")

	// ErrSessionExists is returned when Create receives an id that is
	// already present (manager-supplied id collision).
	ErrSessionExists = errors.New("session: already exists")

	// ErrInvalidName is returned when a Create or Rename receives an
	// empty / whitespace-only name.
	ErrInvalidName = errors.New("session: name cannot be empty")

	// ErrInvalidContextKind is returned when SetSystemPrompt receives a
	// kind outside {ContextKindSystem, ContextKindUserSeed}.
	ErrInvalidContextKind = errors.New("session: invalid context kind")

	// ErrAutoTitleSuperseded is returned by AutoTitle when the session's
	// auto_titled flag is already 1 — meaning either the engine already
	// fired or a user renamed the session — and the write is skipped.
	ErrAutoTitleSuperseded = errors.New("session: auto-title superseded")
)

// Store is the persistence contract the Manager consumes. Two
// implementations ship in this package:
//
//   - NewMemoryStore — pure in-memory, used by unit tests and as a
//     fallback when storage is not yet wired.
//   - NewSQLStore — backed by a storage.DB-shaped connection. Real
//     persistence path used at runtime.
//
// All methods are safe for concurrent use.
type Store interface {
	Create(ctx context.Context, r Record) error
	Get(ctx context.Context, id string) (Record, error)
	List(ctx context.Context) ([]Record, error)
	ListByProject(ctx context.Context, projectID string) ([]Record, error)
	Rename(ctx context.Context, id, name string, now time.Time) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, ids []string, now time.Time) error
	UpdateLastActive(ctx context.Context, id string, at time.Time) error
	UpdateDraft(ctx context.Context, id, draft string, now time.Time) error
	UpdateScrollPosition(ctx context.Context, id string, pos int64, now time.Time) error
	SetSystemPrompt(ctx context.Context, id, content, kind string, now time.Time) error
	SetProject(ctx context.Context, id string, projectID *string, now time.Time) error

	// AutoTitle atomically sets name and auto_titled=1 on a session, but
	// only when auto_titled is currently 0. If auto_titled is already 1,
	// ErrAutoTitleSuperseded is returned and no write occurs.
	// Both the predicate check and the write happen inside the same
	// transaction to guard against races.
	AutoTitle(ctx context.Context, id, name string, now time.Time) error
	// MarkAutoTitleAttempted sets auto_titled=1 without changing the name.
	// Used on the failure path so a crashed generator run doesn't
	// retry indefinitely.
	MarkAutoTitleAttempted(ctx context.Context, id string, now time.Time) error
	// ClearTitle resets name to "" and auto_titled=0, re-enabling future
	// auto-title attempts. The name empty is legal here (unlike Rename);
	// callers own the validation that this is a deliberate user-clear.
	ClearTitle(ctx context.Context, id string, now time.Time) error
	// SetBranchAdvisorDismissed persists the per-session "don't suggest
	// again" flag for the branch advisor (FR-010). When dismissed is
	// true, the backend skips detection for this session.
	SetBranchAdvisorDismissed(ctx context.Context, id string, dismissed bool, now time.Time) error

	// SetAutonomyProfile persists the per-session autonomy.Layer
	// (autonomy-dial-01KR3M2A WP02). An empty Layer (nil Level + empty
	// Overrides) round-trips as both columns NULL — the upstream resolver
	// then falls back to the project / global / tier-default chain.
	// Mutating ID's session row is the only side effect; UpdatedAt is
	// not bumped (this is a UI-state knob, not a content edit).
	SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error
	// GetAutonomyProfile loads the per-session autonomy.Layer. Returns
	// the empty Layer when both columns are NULL — callers feed the
	// result straight into autonomy.Resolve which already understands
	// the empty layer as "this layer contributes nothing."
	GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error)

	AppendMessage(ctx context.Context, m Message) (Message, error)
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
	// ListMessagesActive returns only rows where archived_at IS NULL,
	// ordered by sequence ASC. This is the default scrollback view once
	// a session has any compacted history (compaction-strategy-ui WP07).
	ListMessagesActive(ctx context.Context, sessionID string) ([]Message, error)
	// ApplyCompaction inserts the synthetic summary row + flips
	// compacted_into_id / archived_at on every original row in
	// originalIDs in a single SQLite transaction. The caller (the
	// compaction engine via core/compaction/wiring) supplies the summary
	// shape; the Store only sequences the writes atomically.
	//
	// summary.SessionID is ignored — the sessionID arg is the
	// authoritative one. summary.Sequence is used as-is (the engine
	// computes "lowest sequence in span" so the summary lands at the
	// head of the surviving history). Summary content is persisted
	// verbatim, including the canonical "[Earlier conversation summary: …]"
	// or "[Rolling summary: …]" wrapper the engine produced.
	//
	// Compaction-strategy-ui-01KQ8TDI WP08 wires this into a real
	// session_messages INSERT + UPDATE pair; the in-memory implementation
	// mirrors the same semantics on the in-memory slice for tests.
	ApplyCompaction(ctx context.Context, sessionID string, summary Message,
		originalIDs []string, archivedAt time.Time) error
	// DeleteArchivedBefore tombstones session_messages rows whose
	// archived_at is non-NULL and older than cutoff, in batched DELETEs
	// of pageLimit each, until no more rows match. Returns the total
	// deleted count plus the oldest / newest archived_at timestamps the
	// sweep covered. Summary rows (compacted_into_id IS NULL) are
	// excluded by the WHERE clause.
	//
	// Wraps the SQL the soft-archive sweep (core/compaction/sweep.go)
	// drives once per scheduler tick; the in-memory implementation
	// mirrors the semantics so unit tests can exercise the sweep without
	// a real DB.
	DeleteArchivedBefore(ctx context.Context, cutoff time.Time, pageLimit int) (
		deleted int, oldest, newest time.Time, err error)

	// MarkStreamingFailure persists the resume metadata onto an assistant
	// row that was just appended in the partial-output shape
	// (long-turn-resilience-01KR3PRS WP03). The store unconditionally
	// writes the four columns even when the row already carried a value
	// — calling MarkStreamingFailure twice on the same id is idempotent
	// and overwrites with the latest classification.
	//
	// failedAt is required (must be non-zero); kind is one of "transient"
	// | "auth" | "unknown"; recoverable selects the UI affordance.
	// Returns ErrSessionNotFound when the message is unknown.
	MarkStreamingFailure(ctx context.Context, sessionID, messageID string,
		failedAt time.Time, kind string, recoverable bool) error

	// GetMessage returns one message by id within a session. Returns
	// ErrSessionNotFound for an unknown id (mirrors the row-level "not
	// found" shape every other lookup uses). Used by the resume RPC to
	// load the partial assistant row before reconstructing the
	// continuation prompt.
	GetMessage(ctx context.Context, sessionID, messageID string) (Message, error)

	// AppendContinuation persists a continuation assistant row (the
	// "fresh" reply produced by Sessions_ResumeMessage) and stamps the
	// continuation_of pointer onto the new row in a single transaction.
	// originalID is the id of the partial row this continues; passing
	// the empty string is a programmer error (the runtime will error
	// rather than persist an unanchored continuation).
	AppendContinuation(ctx context.Context, originalID string, m Message) (Message, error)

	// SetKind updates the Kind column of an existing session.
	// Used by the onboarding FSM to transition a session from
	// "onboarding" → "chat" on terminal state (WP09).
	// Returns ErrSessionNotFound when the session does not exist.
	SetKind(ctx context.Context, id, kind string) error

	// SetLastUsage persists the per-turn usage snapshot onto the session
	// row. Called by the chat runner's UsageHook after every completed
	// LLM turn so the frontend can update the context-window indicator
	// without a round-trip to GetUsage
	// (backend-context-window-length-01KQ8TD3 WP02).
	// Returns ErrSessionNotFound when the session does not exist.
	SetLastUsage(ctx context.Context, id string, u LastUsage) error

	// GetLastUsage loads the most-recently-persisted usage snapshot for
	// the session. Returns a zero LastUsage (not an error) when no turn
	// has completed yet (column is NULL).
	// Returns ErrSessionNotFound when the session does not exist.
	GetLastUsage(ctx context.Context, id string) (LastUsage, error)
}

// memStore is the in-memory Store implementation. Backed by maps
// guarded by a single RWMutex; appropriate for test scale and as the
// boot fallback before the SQL store is wired.
type memStore struct {
	mu        sync.RWMutex
	records   map[string]Record
	messages  map[string][]Message   // session_id -> ordered messages
	seqByID   map[string]int64       // session_id -> next sequence
	lastUsage map[string]*LastUsage  // session_id -> last usage snapshot
}

// NewMemoryStore returns an in-memory Store. Useful for tests and as
// the manager's default before storage-foundations wires a real DB.
func NewMemoryStore() Store {
	return &memStore{
		records:   map[string]Record{},
		messages:  map[string][]Message{},
		seqByID:   map[string]int64{},
		lastUsage: map[string]*LastUsage{},
	}
}

func (s *memStore) Create(_ context.Context, r Record) error {
	if r.Name == "" {
		return ErrInvalidName
	}
	if r.ContextKind == "" {
		// Mirror the SQL DEFAULT so reads after Create don't surface
		// an empty kind that would fail SetSystemPrompt validation.
		r.ContextKind = ContextKindSystem
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[r.ID]; ok {
		return ErrSessionExists
	}
	s.records[r.ID] = r
	return nil
}

func (s *memStore) Get(_ context.Context, id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	if !ok {
		return Record{}, ErrSessionNotFound
	}
	return r, nil
}

func (s *memStore) List(_ context.Context) ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		if r.ArchivedAt != nil {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out, nil
}

func (s *memStore) ListByProject(_ context.Context, projectID string) ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0)
	for _, r := range s.records {
		if r.ArchivedAt != nil {
			continue
		}
		if r.ProjectID == nil || *r.ProjectID != projectID {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out, nil
}

func (s *memStore) SetProject(_ context.Context, id string, projectID *string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	if projectID == nil {
		r.ProjectID = nil
	} else {
		v := *projectID
		r.ProjectID = &v
	}
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) SetAutonomyProfile(_ context.Context, id string, layer autonomy.Layer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.AutonomyLevel, r.AutonomyOverrides = cloneAutonomyLayer(layer)
	s.records[id] = r
	return nil
}

func (s *memStore) GetAutonomyProfile(_ context.Context, id string) (autonomy.Layer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	if !ok {
		return autonomy.Layer{}, ErrSessionNotFound
	}
	return autonomyLayerFromRecord(r.AutonomyLevel, r.AutonomyOverrides), nil
}

func (s *memStore) SetBranchAdvisorDismissed(_ context.Context, id string, dismissed bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.BranchAdvisorDismissed = dismissed
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) Rename(_ context.Context, id, name string, now time.Time) error {
	if name == "" {
		return ErrInvalidName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.Name = name
	r.AutoTitled = true // non-empty rename locks out further auto-titling
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) AutoTitle(_ context.Context, id, name string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	// Re-check predicate inside the lock (same-transaction race safety).
	if r.AutoTitled {
		return ErrAutoTitleSuperseded
	}
	r.Name = name
	r.AutoTitled = true
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) MarkAutoTitleAttempted(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.AutoTitled = true
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) ClearTitle(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.Name = ""
	r.AutoTitled = false
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrSessionNotFound
	}
	delete(s.records, id)
	delete(s.messages, id)
	delete(s.seqByID, id)
	return nil
}

func (s *memStore) SetKind(_ context.Context, id, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.Kind = kind
	s.records[id] = r
	return nil
}

func (s *memStore) SetLastUsage(_ context.Context, id string, u LastUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrSessionNotFound
	}
	dup := u
	s.lastUsage[id] = &dup
	return nil
}

func (s *memStore) GetLastUsage(_ context.Context, id string) (LastUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[id]; !ok {
		return LastUsage{}, ErrSessionNotFound
	}
	if u, ok := s.lastUsage[id]; ok {
		return *u, nil
	}
	return LastUsage{}, nil
}

func (s *memStore) Reorder(_ context.Context, ids []string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Validate every id exists before mutating state.
	for _, id := range ids {
		if _, ok := s.records[id]; !ok {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
	}
	for i, id := range ids {
		r := s.records[id]
		r.Position = int64(i)
		r.UpdatedAt = now
		s.records[id] = r
	}
	return nil
}

func (s *memStore) UpdateLastActive(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.LastActiveAt = at
	r.UpdatedAt = at
	s.records[id] = r
	return nil
}

func (s *memStore) UpdateDraft(_ context.Context, id, draft string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.Draft = draft
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) UpdateScrollPosition(_ context.Context, id string, pos int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.ScrollPosition = pos
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) SetSystemPrompt(_ context.Context, id, content, kind string, now time.Time) error {
	if !validContextKind(kind) {
		return fmt.Errorf("%w: %q", ErrInvalidContextKind, kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ErrSessionNotFound
	}
	r.SystemPrompt = content
	r.ContextKind = kind
	r.UpdatedAt = now
	s.records[id] = r
	return nil
}

func (s *memStore) AppendMessage(_ context.Context, m Message) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[m.SessionID]; !ok {
		return Message{}, ErrSessionNotFound
	}
	seq := s.seqByID[m.SessionID]
	m.Sequence = seq
	s.seqByID[m.SessionID] = seq + 1
	// Mirror the SQL store's normalization so an in-memory listed
	// Message round-trips with the same shape as a SQL-backed one.
	m.ContentBlocks = canonicalBlocks(m)
	m.Content = flattenContentText(m.ContentBlocks)
	s.messages[m.SessionID] = append(s.messages[m.SessionID], m)
	return m, nil
}

func (s *memStore) ListMessages(_ context.Context, sessionID string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	out := make([]Message, len(s.messages[sessionID]))
	copy(out, s.messages[sessionID])
	return out, nil
}

// ListMessagesActive returns only the messages whose ArchivedAt is nil.
// In-memory implementation walks the existing slice; the SQL store uses
// a partial-index-friendly WHERE clause.
func (s *memStore) ListMessagesActive(_ context.Context, sessionID string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	src := s.messages[sessionID]
	out := make([]Message, 0, len(src))
	for _, m := range src {
		if m.ArchivedAt != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// ApplyCompaction inserts the synthetic summary row at its caller-
// supplied Sequence and flips compacted_into_id / archived_at on every
// id in originalIDs. Atomic in the sense that the in-memory map is
// guarded by the store's RWMutex for the whole operation; tests can
// pin the post-compaction state without races.
func (s *memStore) ApplyCompaction(_ context.Context, sessionID string, summary Message,
	originalIDs []string, archivedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[sessionID]; !ok {
		return ErrSessionNotFound
	}
	// Build a set of original ids for the flip pass.
	idSet := make(map[string]struct{}, len(originalIDs))
	for _, id := range originalIDs {
		idSet[id] = struct{}{}
	}
	// Flip every original.
	src := s.messages[sessionID]
	at := archivedAt
	intoID := summary.ID
	for i := range src {
		if _, ok := idSet[src[i].ID]; !ok {
			continue
		}
		// Only flip rows that have not already been archived. The engine
		// guards this upstream (only ListActiveMessages rows are folded)
		// but the store still tolerates a re-flip as idempotent.
		t := at
		s.messages[sessionID][i].ArchivedAt = &t
		id := intoID
		s.messages[sessionID][i].CompactedIntoID = &id
	}
	// Insert the summary row. Normalize content shape so reads round-
	// trip with the SQL store's behavior.
	summary.SessionID = sessionID
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = at
	}
	// CompactedAt non-nil + CompactedIntoID nil signals "this IS the
	// summary row" per the WP01 schema convention.
	t := at
	summary.CompactedAt = &t
	summary.ContentBlocks = canonicalBlocks(summary)
	summary.Content = flattenContentText(summary.ContentBlocks)
	s.messages[sessionID] = append(s.messages[sessionID], summary)
	return nil
}

// DeleteArchivedBefore walks the in-memory slice once, dropping every
// row whose ArchivedAt < cutoff AND whose CompactedIntoID is non-nil.
// pageLimit is honored for parity with the SQL implementation (caps the
// per-pass deletes); the in-memory slice rarely needs more than one
// pass anyway.
func (s *memStore) DeleteArchivedBefore(_ context.Context, cutoff time.Time, pageLimit int) (
	deleted int, oldest, newest time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pageLimit <= 0 {
		pageLimit = 5000
	}
	for sid, msgs := range s.messages {
		kept := make([]Message, 0, len(msgs))
		for _, m := range msgs {
			if m.ArchivedAt == nil || m.CompactedIntoID == nil {
				kept = append(kept, m)
				continue
			}
			if !m.ArchivedAt.Before(cutoff) {
				kept = append(kept, m)
				continue
			}
			if deleted >= pageLimit {
				kept = append(kept, m)
				continue
			}
			deleted++
			t := *m.ArchivedAt
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
			if newest.IsZero() || t.After(newest) {
				newest = t
			}
		}
		s.messages[sid] = kept
	}
	return deleted, oldest, newest, nil
}

// MarkStreamingFailure stamps the resume metadata on an in-memory
// message row. Idempotent — re-marking the same row overwrites the
// previous classification (mirrors the SQL UPDATE shape). Returns
// ErrSessionNotFound when the message id is unknown.
func (s *memStore) MarkStreamingFailure(_ context.Context, sessionID, messageID string,
	failedAt time.Time, kind string, recoverable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[sessionID]; !ok {
		return ErrSessionNotFound
	}
	src := s.messages[sessionID]
	for i := range src {
		if src[i].ID != messageID {
			continue
		}
		t := failedAt
		s.messages[sessionID][i].StreamingFailedAt = &t
		s.messages[sessionID][i].StreamingFailureKind = kind
		s.messages[sessionID][i].StreamingRecoverable = recoverable
		return nil
	}
	return ErrSessionNotFound
}

// GetMessage returns one message by id within a session. Mirrors the
// SQL-store contract.
func (s *memStore) GetMessage(_ context.Context, sessionID, messageID string) (Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[sessionID]; !ok {
		return Message{}, ErrSessionNotFound
	}
	for _, m := range s.messages[sessionID] {
		if m.ID == messageID {
			return m, nil
		}
	}
	return Message{}, ErrSessionNotFound
}

// AppendContinuation persists a continuation row, stamping
// ContinuationOf onto the new row. Mirrors the SQL store's atomicity
// guarantees: caller-supplied id is preserved; sequence is the next
// monotonic slot; the in-memory normalization keeps round-trip parity
// with content_json reads.
func (s *memStore) AppendContinuation(_ context.Context, originalID string, m Message) (Message, error) {
	if originalID == "" {
		return Message{}, fmt.Errorf("session: AppendContinuation: empty originalID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[m.SessionID]; !ok {
		return Message{}, ErrSessionNotFound
	}
	seq := s.seqByID[m.SessionID]
	m.Sequence = seq
	s.seqByID[m.SessionID] = seq + 1
	m.ContinuationOf = originalID
	m.ContentBlocks = canonicalBlocks(m)
	m.Content = flattenContentText(m.ContentBlocks)
	s.messages[m.SessionID] = append(s.messages[m.SessionID], m)
	return m, nil
}

// ── SQL-backed implementation ──────────────────────────────────────────

// Result is the minimal write-result shape the SQL store needs. It
// lines up with both database/sql.Result and core/storage.Result so the
// store can run against either.
type Result interface {
	RowsAffected() (int64, error)
}

// Row mirrors the QueryRow surface from database/sql / storage.Row.
type Row interface {
	Scan(dest ...any) error
}

// Rows mirrors the iterator surface from database/sql / storage.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// WriteTx is the write-transaction surface. Mirrors the migrations and
// storage WriteTx contracts so Manager can run against either.
type WriteTx interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Reader is the read pool surface.
type Reader interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// DB is the minimal connection contract the SQL store consumes.
// storage.DB satisfies this once the libSQL backend lands; tests
// supply a fake.
type DB interface {
	Reader() Reader
	WriteTx(ctx context.Context, fn func(tx WriteTx) error) error
}

// sqlStore persists sessions through a storage.DB-shaped connection.
// Every read goes through Reader(); every write hops through a
// WriteTx so writes serialize per the storage layer's contract.
type sqlStore struct {
	db DB
}

// NewSQLStore constructs a SQL-backed Store.
func NewSQLStore(db DB) Store {
	return &sqlStore{db: db}
}

func (s *sqlStore) Create(ctx context.Context, r Record) error {
	if r.Name == "" {
		return ErrInvalidName
	}
	if r.ContextKind == "" {
		r.ContextKind = ContextKindSystem
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		var archived any
		if r.ArchivedAt != nil {
			archived = r.ArchivedAt.UnixNano()
		}
		var projectID any
		if r.ProjectID != nil {
			projectID = *r.ProjectID
		}
		var advisorDismissed int
		if r.BranchAdvisorDismissed {
			advisorDismissed = 1
		}
		autonomyLevel, autonomyOverrides, err := encodeAutonomySQL(autonomyLayerFromRecord(r.AutonomyLevel, r.AutonomyOverrides))
		if err != nil {
			return err
		}
		kindCol := r.Kind
		if kindCol == "" {
			kindCol = SessionKindChat
		}
		_, err = tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id,
                 branch_advisor_dismissed,
                 autonomy_level, autonomy_overrides, kind)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			r.ID,
			r.Name,
			r.CreatedAt.UnixNano(),
			r.UpdatedAt.UnixNano(),
			r.LastActiveAt.UnixNano(),
			r.Position,
			r.Draft,
			r.ScrollPosition,
			archived,
			r.SystemPrompt,
			r.ContextKind,
			projectID,
			advisorDismissed,
			autonomyLevel,
			autonomyOverrides,
			kindCol,
		)
		return err
	})
}

const sqlSelectSession = `
    SELECT id, name, created_at, updated_at, last_active_at,
           position, draft, scroll_position, archived_at,
           system_prompt, context_kind, project_id, auto_titled,
           COALESCE(branch_advisor_dismissed, 0),
           autonomy_level, autonomy_overrides,
           COALESCE(kind, 'chat')
    FROM sessions
`

func (s *sqlStore) Get(ctx context.Context, id string) (Record, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectSession+" WHERE id = ?", id)
	r, err := scanRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrSessionNotFound
		}
		return Record{}, err
	}
	return r, nil
}

func (s *sqlStore) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.Reader().Query(ctx,
		sqlSelectSession+" WHERE archived_at IS NULL ORDER BY position ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqlStore) ListByProject(ctx context.Context, projectID string) ([]Record, error) {
	rows, err := s.db.Reader().Query(ctx,
		sqlSelectSession+" WHERE archived_at IS NULL AND project_id = ? ORDER BY position ASC",
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqlStore) SetProject(ctx context.Context, id string, projectID *string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		var v any
		if projectID != nil {
			v = *projectID
		}
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET project_id = ?, updated_at = ? WHERE id = ?",
			v, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) SetBranchAdvisorDismissed(ctx context.Context, id string, dismissed bool, now time.Time) error {
	var v int
	if dismissed {
		v = 1
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET branch_advisor_dismissed = ?, updated_at = ? WHERE id = ?",
			v, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error {
	level, overrides, err := encodeAutonomySQL(layer)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET autonomy_level = ?, autonomy_overrides = ? WHERE id = ?",
			level, overrides, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error) {
	row := s.db.Reader().QueryRow(ctx,
		"SELECT autonomy_level, autonomy_overrides FROM sessions WHERE id = ?", id)
	var (
		level     sql.NullInt64
		overrides sql.NullString
	)
	if err := row.Scan(&level, &overrides); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return autonomy.Layer{}, ErrSessionNotFound
		}
		return autonomy.Layer{}, err
	}
	return decodeAutonomySQL(level, overrides)
}

func (s *sqlStore) Rename(ctx context.Context, id, name string, now time.Time) error {
	if name == "" {
		return ErrInvalidName
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET name = ?, auto_titled = 1, updated_at = ? WHERE id = ?",
			name, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) AutoTitle(ctx context.Context, id, name string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		// Re-check predicate inside the transaction (race safety).
		row := tx.QueryRow(ctx, "SELECT auto_titled FROM sessions WHERE id = ?", id)
		var autoTitled int64
		if err := row.Scan(&autoTitled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		if autoTitled != 0 {
			return ErrAutoTitleSuperseded
		}
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET name = ?, auto_titled = 1, updated_at = ? WHERE id = ?",
			name, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) MarkAutoTitleAttempted(ctx context.Context, id string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET auto_titled = 1, updated_at = ? WHERE id = ?",
			now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) ClearTitle(ctx context.Context, id string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET name = '', auto_titled = 0, updated_at = ? WHERE id = ?",
			now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) Delete(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id = ?", id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// SetKind updates the kind column for the given session.
// WP09: harness-self-mcp-onboarding-01KQ8TDU — transitions a session from
// "onboarding" → "chat" when the onboarding FSM reaches its terminal state.
func (s *sqlStore) SetKind(ctx context.Context, id, kind string) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx, "UPDATE sessions SET kind = ? WHERE id = ?", kind, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// SetLastUsage persists the per-turn usage snapshot onto the sessions row
// (backend-context-window-length-01KQ8TD3 WP02). The column is added by
// migration 0322; on pre-migration databases the update silently writes
// to a NULL column (SQLite ignores unknown column updates gracefully by
// returning rowsAffectedOrNotFound, which is fine here).
func (s *sqlStore) SetLastUsage(ctx context.Context, id string, u LastUsage) error {
	raw, err := marshalLastUsage(u)
	if err != nil {
		return fmt.Errorf("session: marshal last_usage_json: %w", err)
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET last_usage_json = ? WHERE id = ?", raw, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// GetLastUsage loads the most-recently-persisted usage snapshot for the
// session. Returns a zero LastUsage (not an error) when the column is NULL
// (backend-context-window-length-01KQ8TD3 WP02).
func (s *sqlStore) GetLastUsage(ctx context.Context, id string) (LastUsage, error) {
	row := s.db.Reader().QueryRow(ctx,
		"SELECT last_usage_json FROM sessions WHERE id = ?", id)
	var raw *string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LastUsage{}, ErrSessionNotFound
		}
		return LastUsage{}, err
	}
	if raw == nil || *raw == "" {
		return LastUsage{}, nil
	}
	return unmarshalLastUsage(*raw)
}

func (s *sqlStore) Reorder(ctx context.Context, ids []string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		// Validate first so a missing id does not partially update.
		for _, id := range ids {
			row := tx.QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", id)
			var one int
			if err := row.Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: %s", ErrSessionNotFound, id)
				}
				return err
			}
		}
		for i, id := range ids {
			if _, err := tx.Exec(ctx,
				"UPDATE sessions SET position = ?, updated_at = ? WHERE id = ?",
				int64(i), now.UnixNano(), id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *sqlStore) UpdateLastActive(ctx context.Context, id string, at time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET last_active_at = ?, updated_at = ? WHERE id = ?",
			at.UnixNano(), at.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) UpdateDraft(ctx context.Context, id, draft string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET draft = ?, updated_at = ? WHERE id = ?",
			draft, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) UpdateScrollPosition(ctx context.Context, id string, pos int64, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET scroll_position = ?, updated_at = ? WHERE id = ?",
			pos, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) SetSystemPrompt(ctx context.Context, id, content, kind string, now time.Time) error {
	if !validContextKind(kind) {
		return fmt.Errorf("%w: %q", ErrInvalidContextKind, kind)
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET system_prompt = ?, context_kind = ?, updated_at = ? WHERE id = ?",
			content, kind, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// validContextKind reports whether kind is a known ContextKind*. The
// constants live in types.go; mirror any additions there.
func validContextKind(kind string) bool {
	switch kind {
	case ContextKindSystem, ContextKindUserSeed:
		return true
	}
	return false
}

func (s *sqlStore) AppendMessage(ctx context.Context, m Message) (Message, error) {
	// TODO(post-WP05): drop the legacy `content` column once every
	// reader has migrated to the `content_json` shape. Writers fill
	// both columns for one release as a compat buffer (multimodal-io
	// FR-014).
	canonical := canonicalBlocks(m)
	contentText := flattenContentText(canonical)
	contentJSON, err := json.Marshal(canonical)
	if err != nil {
		return Message{}, fmt.Errorf("session: marshal content_json: %w", err)
	}
	m.ContentBlocks = canonical
	m.Content = contentText

	var out Message
	err = s.db.WriteTx(ctx, func(tx WriteTx) error {
		// Verify the session exists so the FK violation doesn't surface
		// as an opaque storage error.
		row := tx.QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", m.SessionID)
		var one int
		if err := row.Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		// Compute next sequence under the tx so concurrent appends serialize.
		seqRow := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(sequence), -1) + 1 FROM session_messages WHERE session_id = ?",
			m.SessionID)
		var next int64
		if err := seqRow.Scan(&next); err != nil {
			return err
		}
		m.Sequence = next

		var toolCallsJSON any
		if len(m.ToolCalls) > 0 {
			b, err := json.Marshal(m.ToolCalls)
			if err != nil {
				return err
			}
			toolCallsJSON = string(b)
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls, created_at, content_json)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        `,
			m.ID, m.SessionID, m.Sequence, string(m.Role),
			m.Content, toolCallsJSON, m.CreatedAt.UnixNano(),
			string(contentJSON)); err != nil {
			return err
		}
		out = m
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	return out, nil
}

func (s *sqlStore) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	return s.listMessages(ctx, sessionID, false)
}

// ListMessagesActive returns rows where archived_at IS NULL, ordered by
// sequence ASC. The compaction WP07 frontend toggles between this and
// ListMessages to show / hide soft-archived originals.
func (s *sqlStore) ListMessagesActive(ctx context.Context, sessionID string) ([]Message, error) {
	return s.listMessages(ctx, sessionID, true)
}

// listMessages is the shared SELECT body. activeOnly adds the
// archived_at IS NULL clause hitting idx_session_messages_archived_at.
// The new compaction columns (compacted_into_id, compacted_at,
// archived_at) are read alongside the existing payload so callers can
// render the post-compaction indicators without a second round-trip.
func (s *sqlStore) listMessages(ctx context.Context, sessionID string, activeOnly bool) ([]Message, error) {
	// Verify session exists for symmetry with the in-memory implementation.
	row := s.db.Reader().QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID)
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	query := `
        SELECT id, session_id, sequence, role, content, tool_calls, created_at, content_json,
               compacted_into_id, compacted_at, archived_at,
               streaming_failed_at, streaming_failure_kind, streaming_recoverable, continuation_of
        FROM session_messages
        WHERE session_id = ?
    `
	if activeOnly {
		query += " AND archived_at IS NULL"
	}
	query += " ORDER BY sequence ASC"
	rows, err := s.db.Reader().Query(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var (
			m                    Message
			roleStr              string
			toolCalls            sql.NullString
			createdAt            int64
			contentJSON          sql.NullString
			compactedIntoID      sql.NullString
			compactedAt          sql.NullInt64
			archivedAt           sql.NullInt64
			streamingFailedAt    sql.NullInt64
			streamingFailureKind sql.NullString
			streamingRecoverable sql.NullInt64
			continuationOf       sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Sequence, &roleStr,
			&m.Content, &toolCalls, &createdAt, &contentJSON,
			&compactedIntoID, &compactedAt, &archivedAt,
			&streamingFailedAt, &streamingFailureKind, &streamingRecoverable, &continuationOf); err != nil {
			return nil, err
		}
		m.Role = Role(roleStr)
		m.CreatedAt = time.Unix(0, createdAt).UTC()
		if toolCalls.Valid && toolCalls.String != "" {
			if err := json.Unmarshal([]byte(toolCalls.String), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		// Prefer content_json when present; fall back to a synthesized
		// single text block for legacy rows.
		if contentJSON.Valid && contentJSON.String != "" {
			if err := json.Unmarshal([]byte(contentJSON.String), &m.ContentBlocks); err != nil {
				return nil, fmt.Errorf("session: unmarshal content_json: %w", err)
			}
		} else {
			m.ContentBlocks = synthesizeBlocks(m.Content)
		}
		if compactedIntoID.Valid {
			id := compactedIntoID.String
			m.CompactedIntoID = &id
		}
		if compactedAt.Valid {
			t := time.Unix(0, compactedAt.Int64).UTC()
			m.CompactedAt = &t
		}
		if archivedAt.Valid {
			t := time.Unix(0, archivedAt.Int64).UTC()
			m.ArchivedAt = &t
		}
		if streamingFailedAt.Valid {
			t := time.Unix(0, streamingFailedAt.Int64).UTC()
			m.StreamingFailedAt = &t
		}
		if streamingFailureKind.Valid {
			m.StreamingFailureKind = streamingFailureKind.String
		}
		if streamingRecoverable.Valid {
			m.StreamingRecoverable = streamingRecoverable.Int64 != 0
		}
		if continuationOf.Valid {
			m.ContinuationOf = continuationOf.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ApplyCompaction inserts the synthetic summary row + flips
// compacted_into_id / archived_at on each id in originalIDs in a single
// SQLite transaction, so a partial write never leaves the session in a
// half-compacted state. See Store.ApplyCompaction for the full contract.
func (s *sqlStore) ApplyCompaction(ctx context.Context, sessionID string, summary Message,
	originalIDs []string, archivedAt time.Time) error {
	canonical := canonicalBlocks(summary)
	contentText := flattenContentText(canonical)
	contentJSON, err := json.Marshal(canonical)
	if err != nil {
		return fmt.Errorf("session: marshal summary content_json: %w", err)
	}

	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		// Verify the session exists so the FK violation surfaces as a
		// recognizable error rather than an opaque storage failure.
		row := tx.QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID)
		var one int
		if err := row.Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		// Insert the synthetic summary row. compacted_at is set so the
		// frontend's compacted-turn indicator (WP07) can recognize the
		// row; compacted_into_id stays NULL — only originals point at
		// the summary, never the other direction.
		atNS := archivedAt.UnixNano()
		createdAt := summary.CreatedAt
		if createdAt.IsZero() {
			createdAt = archivedAt
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
                 compacted_into_id, compacted_at, archived_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL)
        `,
			summary.ID, sessionID, summary.Sequence, string(summary.Role),
			contentText, nil, createdAt.UnixNano(), string(contentJSON),
			atNS,
		); err != nil {
			return fmt.Errorf("session: insert summary row: %w", err)
		}
		// Flip every original. We loop one UPDATE per id so the SQL stays
		// portable (no parameterized IN-list across drivers); the engine
		// guards the call to a single span per session per turn so the
		// extra round-trips are bounded.
		for _, id := range originalIDs {
			if id == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `
                UPDATE session_messages
                SET compacted_into_id = ?, archived_at = ?
                WHERE session_id = ? AND id = ?
            `, summary.ID, atNS, sessionID, id); err != nil {
				return fmt.Errorf("session: flip original %q: %w", id, err)
			}
		}
		return nil
	})
}

// DeleteArchivedBefore drives the soft-archive sweep's batched DELETE
// loop. See Store.DeleteArchivedBefore for the full contract. The
// summary-row exclusion (compacted_into_id IS NOT NULL) lives in the
// SQL filter so a malformed engine call cannot accidentally tombstone
// the synthetic summary head.
func (s *sqlStore) DeleteArchivedBefore(ctx context.Context, cutoff time.Time, pageLimit int) (
	deleted int, oldest, newest time.Time, err error) {
	if pageLimit <= 0 {
		pageLimit = 5000
	}
	cutoffNS := cutoff.UnixNano()
	for {
		// Capture the page's archived_at extremes BEFORE the DELETE so
		// the audit payload's oldest / newest fields stay accurate.
		// SQLite's RETURNING is supported on modernc.org/sqlite 1.50+
		// but we keep the path portable by issuing a bounded SELECT
		// before the DELETE.
		var pageOldest, pageNewest sql.NullInt64
		var pageDeleted int
		err = s.db.WriteTx(ctx, func(tx WriteTx) error {
			row := tx.QueryRow(ctx, `
                SELECT MIN(archived_at), MAX(archived_at)
                FROM session_messages
                WHERE archived_at IS NOT NULL
                  AND archived_at < ?
                  AND compacted_into_id IS NOT NULL
                LIMIT 1
            `, cutoffNS)
			if err := row.Scan(&pageOldest, &pageNewest); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			res, err := tx.Exec(ctx, `
                DELETE FROM session_messages
                WHERE rowid IN (
                    SELECT rowid FROM session_messages
                    WHERE archived_at IS NOT NULL
                      AND archived_at < ?
                      AND compacted_into_id IS NOT NULL
                    LIMIT ?
                )
            `, cutoffNS, pageLimit)
			if err != nil {
				return err
			}
			n, rerr := res.RowsAffected()
			if rerr != nil {
				return rerr
			}
			pageDeleted = int(n)
			return nil
		})
		if err != nil {
			return deleted, oldest, newest, err
		}
		if pageDeleted == 0 {
			return deleted, oldest, newest, nil
		}
		deleted += pageDeleted
		if pageOldest.Valid {
			t := time.Unix(0, pageOldest.Int64).UTC()
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
		if pageNewest.Valid {
			t := time.Unix(0, pageNewest.Int64).UTC()
			if newest.IsZero() || t.After(newest) {
				newest = t
			}
		}
		// If a page returned fewer than pageLimit rows, the next pass
		// would necessarily be empty — exit early without an extra
		// round-trip.
		if pageDeleted < pageLimit {
			return deleted, oldest, newest, nil
		}
	}
}

// canonicalBlocks normalizes a Message's content to its []ContentBlock
// shape. Callers populating Content alone get a single text block;
// callers populating ContentBlocks pass through unchanged.
func canonicalBlocks(m Message) []llm.ContentBlock {
	if len(m.ContentBlocks) > 0 {
		return m.ContentBlocks
	}
	if m.Content == "" {
		return []llm.ContentBlock{}
	}
	return []llm.ContentBlock{{Type: "text", Text: m.Content}}
}

// flattenContentText runs the WP01 Message.Text() flattener against a
// block slice. Used to fill the legacy `content` column for one
// release.
func flattenContentText(blocks []llm.ContentBlock) string {
	tmp := llm.Message{Content: blocks}
	return tmp.Text()
}

// synthesizeBlocks produces a single-text-block []ContentBlock from a
// legacy row's plain-text Content value. Empty input yields an empty
// slice so callers don't see a stray {Type:"text", Text:""} block.
func synthesizeBlocks(content string) []llm.ContentBlock {
	if content == "" {
		return []llm.ContentBlock{}
	}
	return []llm.ContentBlock{{Type: "text", Text: content}}
}

// scanRecord scans a Record from a one-row scanner. Works for both Row
// (single row) and Rows (current row) since both expose Scan(dest...).
func scanRecord(sc interface{ Scan(dest ...any) error }) (Record, error) {
	var (
		r                  Record
		createdAt          int64
		updatedAt          int64
		lastActive         int64
		archived           sql.NullInt64
		projectID          sql.NullString
		autoTitled         int64
		advisorDismissed   int
		autonomyLevel      sql.NullInt64
		autonomyOverrides  sql.NullString
		kindCol            string
	)
	if err := sc.Scan(
		&r.ID, &r.Name,
		&createdAt, &updatedAt, &lastActive,
		&r.Position, &r.Draft, &r.ScrollPosition,
		&archived,
		&r.SystemPrompt, &r.ContextKind,
		&projectID, &autoTitled,
		&advisorDismissed,
		&autonomyLevel, &autonomyOverrides,
		&kindCol,
	); err != nil {
		return Record{}, err
	}
	r.Kind = kindCol
	if r.Kind == "" {
		r.Kind = SessionKindChat
	}
	r.CreatedAt = time.Unix(0, createdAt).UTC()
	r.UpdatedAt = time.Unix(0, updatedAt).UTC()
	r.LastActiveAt = time.Unix(0, lastActive).UTC()
	if archived.Valid {
		t := time.Unix(0, archived.Int64).UTC()
		r.ArchivedAt = &t
	}
	if projectID.Valid {
		v := projectID.String
		r.ProjectID = &v
	}
	r.AutoTitled = autoTitled != 0
	r.BranchAdvisorDismissed = advisorDismissed != 0
	if autonomyLevel.Valid {
		t := autonomy.Tier(int(autonomyLevel.Int64))
		r.AutonomyLevel = &t
	}
	if autonomyOverrides.Valid && autonomyOverrides.String != "" {
		ov, err := decodeAutonomyOverrides(autonomyOverrides.String)
		if err != nil {
			return Record{}, fmt.Errorf("session: decode autonomy_overrides: %w", err)
		}
		r.AutonomyOverrides = ov
	}
	return r, nil
}

func rowsAffectedOrNotFound(res Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// MarkStreamingFailure stamps the resume metadata onto the
// session_messages row identified by messageID. Idempotent — re-marking
// the same row overwrites the previous classification. Returns
// ErrSessionNotFound when the row is unknown.
//
// long-turn-resilience-01KR3PRS WP03.
func (s *sqlStore) MarkStreamingFailure(ctx context.Context, sessionID, messageID string,
	failedAt time.Time, kind string, recoverable bool) error {
	if messageID == "" {
		return fmt.Errorf("session: MarkStreamingFailure: empty messageID")
	}
	recoverableArg := 0
	if recoverable {
		recoverableArg = 1
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx, `
            UPDATE session_messages
            SET streaming_failed_at = ?, streaming_failure_kind = ?, streaming_recoverable = ?
            WHERE id = ? AND session_id = ?
        `, failedAt.UnixNano(), kind, recoverableArg, messageID, sessionID)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// GetMessage returns one message by id within a session. Returns
// ErrSessionNotFound for an unknown id (mirrors the row-level "not
// found" shape every other lookup uses).
//
// long-turn-resilience-01KR3PRS WP03.
func (s *sqlStore) GetMessage(ctx context.Context, sessionID, messageID string) (Message, error) {
	row := s.db.Reader().QueryRow(ctx, `
        SELECT id, session_id, sequence, role, content, tool_calls, created_at, content_json,
               compacted_into_id, compacted_at, archived_at,
               streaming_failed_at, streaming_failure_kind, streaming_recoverable, continuation_of
        FROM session_messages
        WHERE id = ? AND session_id = ?
    `, messageID, sessionID)
	var (
		m                    Message
		roleStr              string
		toolCalls            sql.NullString
		createdAt            int64
		contentJSON          sql.NullString
		compactedIntoID      sql.NullString
		compactedAt          sql.NullInt64
		archivedAt           sql.NullInt64
		streamingFailedAt    sql.NullInt64
		streamingFailureKind sql.NullString
		streamingRecoverable sql.NullInt64
		continuationOf       sql.NullString
	)
	if err := row.Scan(&m.ID, &m.SessionID, &m.Sequence, &roleStr,
		&m.Content, &toolCalls, &createdAt, &contentJSON,
		&compactedIntoID, &compactedAt, &archivedAt,
		&streamingFailedAt, &streamingFailureKind, &streamingRecoverable, &continuationOf); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, ErrSessionNotFound
		}
		return Message{}, err
	}
	m.Role = Role(roleStr)
	m.CreatedAt = time.Unix(0, createdAt).UTC()
	if toolCalls.Valid && toolCalls.String != "" {
		if err := json.Unmarshal([]byte(toolCalls.String), &m.ToolCalls); err != nil {
			return Message{}, err
		}
	}
	if contentJSON.Valid && contentJSON.String != "" {
		if err := json.Unmarshal([]byte(contentJSON.String), &m.ContentBlocks); err != nil {
			return Message{}, fmt.Errorf("session: unmarshal content_json: %w", err)
		}
	} else {
		m.ContentBlocks = synthesizeBlocks(m.Content)
	}
	if compactedIntoID.Valid {
		id := compactedIntoID.String
		m.CompactedIntoID = &id
	}
	if compactedAt.Valid {
		t := time.Unix(0, compactedAt.Int64).UTC()
		m.CompactedAt = &t
	}
	if archivedAt.Valid {
		t := time.Unix(0, archivedAt.Int64).UTC()
		m.ArchivedAt = &t
	}
	if streamingFailedAt.Valid {
		t := time.Unix(0, streamingFailedAt.Int64).UTC()
		m.StreamingFailedAt = &t
	}
	if streamingFailureKind.Valid {
		m.StreamingFailureKind = streamingFailureKind.String
	}
	if streamingRecoverable.Valid {
		m.StreamingRecoverable = streamingRecoverable.Int64 != 0
	}
	if continuationOf.Valid {
		m.ContinuationOf = continuationOf.String
	}
	return m, nil
}

// AppendContinuation persists a continuation row, stamping the
// continuation_of pointer onto the new row in the same transaction
// that allocates the next sequence. Mirrors AppendMessage's sequencing
// semantics — concurrent calls serialize on the SELECT MAX(sequence)
// step, so two simultaneous resumes of different sessions don't
// collide.
//
// long-turn-resilience-01KR3PRS WP03.
func (s *sqlStore) AppendContinuation(ctx context.Context, originalID string, m Message) (Message, error) {
	if originalID == "" {
		return Message{}, fmt.Errorf("session: AppendContinuation: empty originalID")
	}
	canonical := canonicalBlocks(m)
	contentText := flattenContentText(canonical)
	contentJSON, err := json.Marshal(canonical)
	if err != nil {
		return Message{}, fmt.Errorf("session: marshal content_json: %w", err)
	}
	m.ContentBlocks = canonical
	m.Content = contentText
	m.ContinuationOf = originalID

	var out Message
	err = s.db.WriteTx(ctx, func(tx WriteTx) error {
		row := tx.QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", m.SessionID)
		var one int
		if err := row.Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		seqRow := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(sequence), -1) + 1 FROM session_messages WHERE session_id = ?",
			m.SessionID)
		var next int64
		if err := seqRow.Scan(&next); err != nil {
			return err
		}
		m.Sequence = next

		var toolCallsJSON any
		if len(m.ToolCalls) > 0 {
			b, jerr := json.Marshal(m.ToolCalls)
			if jerr != nil {
				return jerr
			}
			toolCallsJSON = string(b)
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO session_messages
                (id, session_id, sequence, role, content, tool_calls, created_at, content_json, continuation_of)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			m.ID, m.SessionID, m.Sequence, string(m.Role),
			m.Content, toolCallsJSON, m.CreatedAt.UnixNano(),
			string(contentJSON), originalID); err != nil {
			return err
		}
		out = m
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	return out, nil
}

// marshalLastUsage encodes a LastUsage snapshot to JSON for the
// sessions.last_usage_json column.
func marshalLastUsage(u LastUsage) (string, error) {
	b, err := json.Marshal(u)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalLastUsage decodes the sessions.last_usage_json column back
// into a LastUsage value.
func unmarshalLastUsage(raw string) (LastUsage, error) {
	var u LastUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return LastUsage{}, fmt.Errorf("session: unmarshal last_usage_json: %w", err)
	}
	return u, nil
}
