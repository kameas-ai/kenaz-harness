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

	AppendMessage(ctx context.Context, m Message) (Message, error)
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
}

// memStore is the in-memory Store implementation. Backed by maps
// guarded by a single RWMutex; appropriate for test scale and as the
// boot fallback before the SQL store is wired.
type memStore struct {
	mu        sync.RWMutex
	records   map[string]Record
	messages  map[string][]Message // session_id -> ordered messages
	seqByID   map[string]int64     // session_id -> next sequence
}

// NewMemoryStore returns an in-memory Store. Useful for tests and as
// the manager's default before storage-foundations wires a real DB.
func NewMemoryStore() Store {
	return &memStore{
		records:  map[string]Record{},
		messages: map[string][]Message{},
		seqByID:  map[string]int64{},
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
		_, err := tx.Exec(ctx, `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		)
		return err
	})
}

const sqlSelectSession = `
    SELECT id, name, created_at, updated_at, last_active_at,
           position, draft, scroll_position, archived_at,
           system_prompt, context_kind, project_id
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

func (s *sqlStore) Rename(ctx context.Context, id, name string, now time.Time) error {
	if name == "" {
		return ErrInvalidName
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE sessions SET name = ?, updated_at = ? WHERE id = ?",
			name, now.UnixNano(), id)
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
	var out Message
	err := s.db.WriteTx(ctx, func(tx WriteTx) error {
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
                (id, session_id, sequence, role, content, tool_calls, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)
        `,
			m.ID, m.SessionID, m.Sequence, string(m.Role),
			m.Content, toolCallsJSON, m.CreatedAt.UnixNano()); err != nil {
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
	// Verify session exists for symmetry with the in-memory implementation.
	row := s.db.Reader().QueryRow(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID)
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	rows, err := s.db.Reader().Query(ctx, `
        SELECT id, session_id, sequence, role, content, tool_calls, created_at
        FROM session_messages
        WHERE session_id = ?
        ORDER BY sequence ASC
    `, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var (
			m         Message
			roleStr   string
			toolCalls sql.NullString
			createdAt int64
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Sequence, &roleStr,
			&m.Content, &toolCalls, &createdAt); err != nil {
			return nil, err
		}
		m.Role = Role(roleStr)
		m.CreatedAt = time.Unix(0, createdAt).UTC()
		if toolCalls.Valid && toolCalls.String != "" {
			if err := json.Unmarshal([]byte(toolCalls.String), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanRecord scans a Record from a one-row scanner. Works for both Row
// (single row) and Rows (current row) since both expose Scan(dest...).
func scanRecord(sc interface{ Scan(dest ...any) error }) (Record, error) {
	var (
		r          Record
		createdAt  int64
		updatedAt  int64
		lastActive int64
		archived   sql.NullInt64
		projectID  sql.NullString
	)
	if err := sc.Scan(
		&r.ID, &r.Name,
		&createdAt, &updatedAt, &lastActive,
		&r.Position, &r.Draft, &r.ScrollPosition,
		&archived,
		&r.SystemPrompt, &r.ContextKind,
		&projectID,
	); err != nil {
		return Record{}, err
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
