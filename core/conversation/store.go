package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrBranchNotFound is returned when a branch id has no matching row.
	ErrBranchNotFound = errors.New("conversation: branch not found")
	// ErrBranchExists is returned when Create receives an id that is
	// already present.
	ErrBranchExists = errors.New("conversation: branch already exists")
	// ErrInvalidArg signals trivially invalid inputs.
	ErrInvalidArg = errors.New("conversation: invalid argument")
)

// Store is the persistence contract for the branches subsystem. Two
// implementations ship: NewMemoryStore (tests + boot fallback) and
// NewSQLStore (real persistence path). All methods are safe for
// concurrent use.
type Store interface {
	Create(ctx context.Context, b Branch) error
	Get(ctx context.Context, id string) (Branch, error)
	ListByParent(ctx context.Context, parentSessionID string) ([]Branch, error)
	ListByChild(ctx context.Context, childSessionID string) ([]Branch, error)
	UpdateStatus(ctx context.Context, id string, status BranchStatus, now time.Time) error
	MarkMerged(ctx context.Context, id string, now time.Time) error
	MarkAbandoned(ctx context.Context, id string, now time.Time) error

	AppendMessageRef(ctx context.Context, ref MessageRef) error
	ListMessageRefs(ctx context.Context, branchID string) ([]MessageRef, error)
	UpdateChildMsgID(ctx context.Context, branchID string, seq int64, childMsgID string) error
}

// ── In-memory store ──────────────────────────────────────────────────

type memStore struct {
	mu       sync.RWMutex
	branches map[string]Branch
	refs     map[string][]MessageRef // branchID → ordered refs
}

// NewMemoryStore returns an in-memory Store suitable for tests.
func NewMemoryStore() Store {
	return &memStore{
		branches: map[string]Branch{},
		refs:     map[string][]MessageRef{},
	}
}

func (s *memStore) Create(_ context.Context, b Branch) error {
	if b.ID == "" || b.ParentSessionID == "" || b.ChildSessionID == "" {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.branches[b.ID]; ok {
		return ErrBranchExists
	}
	if b.Status == "" {
		b.Status = BranchStatusActive
	}
	if b.Kind == "" {
		b.Kind = BranchKindFork
	}
	s.branches[b.ID] = b
	return nil
}

func (s *memStore) Get(_ context.Context, id string) (Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.branches[id]
	if !ok {
		return Branch{}, ErrBranchNotFound
	}
	return b, nil
}

func (s *memStore) ListByParent(_ context.Context, parentSessionID string) ([]Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Branch, 0)
	for _, b := range s.branches {
		if b.ParentSessionID == parentSessionID {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) ListByChild(_ context.Context, childSessionID string) ([]Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Branch, 0)
	for _, b := range s.branches {
		if b.ChildSessionID == childSessionID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *memStore) UpdateStatus(_ context.Context, id string, status BranchStatus, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok {
		return ErrBranchNotFound
	}
	b.Status = status
	b.UpdatedAt = now
	s.branches[id] = b
	return nil
}

func (s *memStore) MarkMerged(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok {
		return ErrBranchNotFound
	}
	b.Status = BranchStatusMerged
	b.UpdatedAt = now
	t := now
	b.MergedAt = &t
	s.branches[id] = b
	return nil
}

func (s *memStore) MarkAbandoned(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok {
		return ErrBranchNotFound
	}
	b.Status = BranchStatusAbandoned
	b.UpdatedAt = now
	t := now
	b.AbandonedAt = &t
	s.branches[id] = b
	return nil
}

func (s *memStore) AppendMessageRef(_ context.Context, ref MessageRef) error {
	if ref.BranchID == "" {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[ref.BranchID] = append(s.refs[ref.BranchID], ref)
	return nil
}

func (s *memStore) ListMessageRefs(_ context.Context, branchID string) ([]MessageRef, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MessageRef, len(s.refs[branchID]))
	copy(out, s.refs[branchID])
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (s *memStore) UpdateChildMsgID(_ context.Context, branchID string, seq int64, childMsgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.refs[branchID]
	for i := range rows {
		if rows[i].Seq == seq {
			rows[i].ChildMsgID = childMsgID
			s.refs[branchID] = rows
			return nil
		}
	}
	return ErrBranchNotFound
}

// ── SQL-backed store ─────────────────────────────────────────────────

// Result mirrors the minimal write-result surface the SQL store needs.
type Result interface {
	RowsAffected() (int64, error)
}

// Row mirrors the scanner surface from database/sql.
type Row interface {
	Scan(dest ...any) error
}

// Rows mirrors the iterator surface from database/sql.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// WriteTx is the write-transaction surface. Lines up with both
// database/sql.Tx and storage.WriteTx via narrow adapters.
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

// DB is the minimal connection contract the SQL store consumes. Same
// shape as session.DB; supplied by core/storage at runtime, fakes in
// tests.
type DB interface {
	Reader() Reader
	WriteTx(ctx context.Context, fn func(tx WriteTx) error) error
}

type sqlStore struct {
	db DB
}

// NewSQLStore constructs a SQL-backed Store.
func NewSQLStore(db DB) Store {
	return &sqlStore{db: db}
}

func (s *sqlStore) Create(ctx context.Context, b Branch) error {
	if b.ID == "" || b.ParentSessionID == "" || b.ChildSessionID == "" {
		return ErrInvalidArg
	}
	if b.Status == "" {
		b.Status = BranchStatusActive
	}
	if b.Kind == "" {
		b.Kind = BranchKindFork
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		var merged any
		if b.MergedAt != nil {
			merged = b.MergedAt.UnixNano()
		}
		var abandoned any
		if b.AbandonedAt != nil {
			abandoned = b.AbandonedAt.UnixNano()
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO branches
                (id, parent_session_id, child_session_id, kind, status,
                 model_id, provider_id, title, task_hint,
                 created_at, updated_at, merged_at, abandoned_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			b.ID, b.ParentSessionID, b.ChildSessionID,
			string(b.Kind), string(b.Status),
			b.ModelID, b.ProviderID, b.Title, b.TaskHint,
			b.CreatedAt.UnixNano(), b.UpdatedAt.UnixNano(),
			merged, abandoned,
		)
		return err
	})
}

const sqlSelectBranch = `
    SELECT id, parent_session_id, child_session_id, kind, status,
           model_id, provider_id, title, task_hint,
           created_at, updated_at, merged_at, abandoned_at
    FROM branches
`

func (s *sqlStore) Get(ctx context.Context, id string) (Branch, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectBranch+" WHERE id = ?", id)
	b, err := scanBranch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Branch{}, ErrBranchNotFound
		}
		return Branch{}, err
	}
	return b, nil
}

func (s *sqlStore) ListByParent(ctx context.Context, parentSessionID string) ([]Branch, error) {
	rows, err := s.db.Reader().Query(ctx,
		sqlSelectBranch+" WHERE parent_session_id = ? ORDER BY created_at ASC",
		parentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Branch
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *sqlStore) ListByChild(ctx context.Context, childSessionID string) ([]Branch, error) {
	rows, err := s.db.Reader().Query(ctx,
		sqlSelectBranch+" WHERE child_session_id = ?",
		childSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Branch
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *sqlStore) UpdateStatus(ctx context.Context, id string, status BranchStatus, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE branches SET status = ?, updated_at = ? WHERE id = ?",
			string(status), now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) MarkMerged(ctx context.Context, id string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE branches SET status = ?, updated_at = ?, merged_at = ? WHERE id = ?",
			string(BranchStatusMerged), now.UnixNano(), now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) MarkAbandoned(ctx context.Context, id string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE branches SET status = ?, updated_at = ?, abandoned_at = ? WHERE id = ?",
			string(BranchStatusAbandoned), now.UnixNano(), now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) AppendMessageRef(ctx context.Context, ref MessageRef) error {
	if ref.BranchID == "" {
		return ErrInvalidArg
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		// Compute next seq under the tx so concurrent appends serialize.
		var next int64
		row := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(seq), -1) + 1 FROM branch_message_refs WHERE branch_id = ?",
			ref.BranchID)
		if err := row.Scan(&next); err != nil {
			return err
		}
		ref.Seq = next
		_, err := tx.Exec(ctx, `
            INSERT INTO branch_message_refs (branch_id, seq, parent_msg_id, child_msg_id)
            VALUES (?, ?, ?, ?)
        `, ref.BranchID, ref.Seq, ref.ParentMsgID, ref.ChildMsgID)
		return err
	})
}

func (s *sqlStore) ListMessageRefs(ctx context.Context, branchID string) ([]MessageRef, error) {
	rows, err := s.db.Reader().Query(ctx, `
        SELECT branch_id, seq, parent_msg_id, child_msg_id
        FROM branch_message_refs
        WHERE branch_id = ?
        ORDER BY seq ASC
    `, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRef
	for rows.Next() {
		var r MessageRef
		if err := rows.Scan(&r.BranchID, &r.Seq, &r.ParentMsgID, &r.ChildMsgID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqlStore) UpdateChildMsgID(ctx context.Context, branchID string, seq int64, childMsgID string) error {
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE branch_message_refs SET child_msg_id = ? WHERE branch_id = ? AND seq = ?",
			childMsgID, branchID, seq)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

// scanBranch scans a Branch from a one-row scanner.
func scanBranch(sc interface{ Scan(dest ...any) error }) (Branch, error) {
	var (
		b           Branch
		kindStr     string
		statusStr   string
		createdAt   int64
		updatedAt   int64
		merged      sql.NullInt64
		abandoned   sql.NullInt64
	)
	if err := sc.Scan(
		&b.ID, &b.ParentSessionID, &b.ChildSessionID,
		&kindStr, &statusStr,
		&b.ModelID, &b.ProviderID, &b.Title, &b.TaskHint,
		&createdAt, &updatedAt, &merged, &abandoned,
	); err != nil {
		return Branch{}, err
	}
	b.Kind = BranchKind(kindStr)
	b.Status = BranchStatus(statusStr)
	b.CreatedAt = time.Unix(0, createdAt).UTC()
	b.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if merged.Valid {
		t := time.Unix(0, merged.Int64).UTC()
		b.MergedAt = &t
	}
	if abandoned.Valid {
		t := time.Unix(0, abandoned.Int64).UTC()
		b.AbandonedAt = &t
	}
	return b, nil
}

func rowsAffectedOrNotFound(res Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBranchNotFound
	}
	return nil
}

// Helper that callers can use in tests to format an internal error
// without taking a hard dep on conversion sentinels.
func wrapErr(op string, err error) error {
	return fmt.Errorf("conversation: %s: %w", op, err)
}
