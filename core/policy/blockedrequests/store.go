// Package blockedrequests is the SQLite-backed persistence layer for
// blocked_permission_requests (model-scheduled-jobs-01PMSJ01 WP06,
// FR-004, migration sessions/0338 —
// core/session/migrations_blocked_permission_requests.go).
//
// This is a durable, surfaceable, RE-RUNNABLE record of a denied
// permission request — deliberately its own table rather than a reuse
// of scheduled_chat_run_history (outcome-shaped, ON DELETE CASCADE'd —
// would destroy the pending grant if the job is deleted, AC-007) or the
// Cedar decision store (each gate builds its own engine with a private,
// unread MemoryDecisionStore — writing there is writing to /dev/null
// with extra steps, per docs/unwired-ledger.md). See the migration file
// for the full "why this table" argument.
//
// Package placement: core/policy (not core/rpc) because the consumer
// list spans both the fs gate wiring (core/rpc/builtins_wiring.go, via
// core/tools/fs.BlockedRequestSink) and, in WP07, a surfacing RPC view —
// neither should import the other, and this table is policy-adjacent
// state, not chassis wiring.
package blockedrequests

import (
	"context"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// Status values for Record.Status (spec.md §5.5's lifecycle).
const (
	StatusPending   = "pending"
	StatusGranted   = "granted"
	StatusDismissed = "dismissed"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("blockedrequests: not found")

// Record is the DB-level projection of blocked_permission_requests.
type Record struct {
	ID         string
	Origin     string // "scheduled_chat_run" | "interactive"
	OriginID   string // scheduled_chat_runs.id, or ""
	SessionID  string
	Family     string // cedar.Family: bash|fs|cred|tool
	Action     string // e.g. "write_filesystem"
	Resource   string // canonical path / command / locator
	Reason     string
	Status     string // "pending" | "granted" | "dismissed"
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// Store is the storage interface for blocked permission requests. All
// methods are safe for concurrent use. The production implementation is
// SQLiteStore; tests may inject a stub.
type Store interface {
	// Create inserts a new pending record. ID must be non-empty and unique.
	Create(ctx context.Context, r Record) error
	// Get returns the record for id, or ErrNotFound.
	Get(ctx context.Context, id string) (Record, error)
	// ListByStatus returns records with the given status, newest first.
	// An empty status returns every record regardless of status.
	ListByStatus(ctx context.Context, status string) ([]Record, error)
	// SetStatus transitions id to status, stamping ResolvedAt when status
	// is StatusGranted or StatusDismissed (leaving it nil for
	// StatusPending, which no caller should ever transition BACK to, but
	// nothing here forbids it). Returns ErrNotFound when no row with id
	// exists.
	SetStatus(ctx context.Context, id string, status string, resolvedAt time.Time) error
}

// SQLiteStore is the production Store backed by the harness storage.DB.
type SQLiteStore struct {
	db storage.DB
}

// NewSQLiteStore returns a Store backed by db.
func NewSQLiteStore(db storage.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Create implements Store.
func (s *SQLiteStore) Create(ctx context.Context, r Record) error {
	const q = `
		INSERT INTO blocked_permission_requests
			(id, origin, origin_id, session_id, family, action, resource, reason, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	status := r.Status
	if status == "" {
		status = StatusPending
	}
	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q,
			r.ID, r.Origin, r.OriginID, r.SessionID, r.Family, r.Action, r.Resource, r.Reason,
			status, createdAt.Unix(),
		)
		return err
	})
}

// Get implements Store.
func (s *SQLiteStore) Get(ctx context.Context, id string) (Record, error) {
	const q = `
		SELECT id, origin, origin_id, session_id, family, action, resource, reason, status, created_at, resolved_at
		FROM blocked_permission_requests
		WHERE id = ?
	`
	row := s.db.Reader().QueryRow(ctx, q, id)
	return scanRecord(row)
}

// ListByStatus implements Store.
func (s *SQLiteStore) ListByStatus(ctx context.Context, status string) ([]Record, error) {
	var rows interface {
		Next() bool
		Scan(dest ...any) error
		Err() error
		Close() error
	}
	var err error
	if status == "" {
		rows, err = s.db.Reader().Query(ctx, `
			SELECT id, origin, origin_id, session_id, family, action, resource, reason, status, created_at, resolved_at
			FROM blocked_permission_requests
			ORDER BY created_at DESC
		`)
	} else {
		rows, err = s.db.Reader().Query(ctx, `
			SELECT id, origin, origin_id, session_id, family, action, resource, reason, status, created_at, resolved_at
			FROM blocked_permission_requests
			WHERE status = ?
			ORDER BY created_at DESC
		`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		r, serr := scanRecord(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetStatus implements Store.
func (s *SQLiteStore) SetStatus(ctx context.Context, id string, status string, resolvedAt time.Time) error {
	const q = `UPDATE blocked_permission_requests SET status = ?, resolved_at = ? WHERE id = ?`
	var resolvedAtVal *int64
	if status != StatusPending {
		v := resolvedAt.Unix()
		resolvedAtVal = &v
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, q, status, resolvedAtVal, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (Record, error) {
	var r Record
	var createdAtRaw int64
	var resolvedAtRaw *int64
	if err := row.Scan(
		&r.ID, &r.Origin, &r.OriginID, &r.SessionID, &r.Family, &r.Action, &r.Resource, &r.Reason,
		&r.Status, &createdAtRaw, &resolvedAtRaw,
	); err != nil {
		return Record{}, ErrNotFound
	}
	r.CreatedAt = time.Unix(createdAtRaw, 0).UTC()
	if resolvedAtRaw != nil {
		t := time.Unix(*resolvedAtRaw, 0).UTC()
		r.ResolvedAt = &t
	}
	return r, nil
}

// Compile-time interface check.
var _ Store = (*SQLiteStore)(nil)
