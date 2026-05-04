// Storage layer for workflow schedules.
//
// At chassis boot, LoadAll reads every enabled schedule from
// workflow_schedules and hands them to the caller for registration.
// Register / Unregister call Upsert / Delete to keep the DB in sync.
package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// ErrScheduleNotFound is returned when a requested schedule does not
// exist in the store.
var ErrScheduleNotFound = errors.New("scheduler: schedule not found")

// StoredSchedule is the DB-level projection of workflow_schedules.
type StoredSchedule struct {
	WorkflowID  string
	Cron        string
	Timezone    string
	Enabled     bool
	LastFiredAt *time.Time
	LastRunID   string
}

// Storage persists workflow_schedules rows.
type Storage interface {
	// LoadAll returns every row from workflow_schedules (regardless of
	// enabled flag).
	LoadAll(ctx context.Context) ([]StoredSchedule, error)
	// Upsert inserts or replaces the schedule row for workflowID.
	Upsert(ctx context.Context, s StoredSchedule) error
	// Delete removes the schedule row for workflowID.
	Delete(ctx context.Context, workflowID string) error
	// SetLastFired updates last_fired_at and last_run_id for the given
	// workflowID. Used after a successful dispatch.
	SetLastFired(ctx context.Context, workflowID, runID string, firedAt time.Time) error
}

// SQLiteStorage is the production Storage backed by the chassis DB.
type SQLiteStorage struct {
	db storage.DB
}

// NewSQLiteStorage returns a Storage backed by the given DB.
func NewSQLiteStorage(db storage.DB) *SQLiteStorage {
	return &SQLiteStorage{db: db}
}

// LoadAll implements Storage.
func (s *SQLiteStorage) LoadAll(ctx context.Context) ([]StoredSchedule, error) {
	const q = `
		SELECT workflow_id, cron, timezone, enabled, last_fired_at, last_run_id
		FROM workflow_schedules
		ORDER BY workflow_id
	`
	rows, err := s.db.Reader().Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StoredSchedule
	for rows.Next() {
		var ss StoredSchedule
		var enabledInt int
		var lastFiredRaw *int64
		var lastRunID *string
		if err := rows.Scan(&ss.WorkflowID, &ss.Cron, &ss.Timezone, &enabledInt, &lastFiredRaw, &lastRunID); err != nil {
			return nil, err
		}
		ss.Enabled = enabledInt != 0
		if lastFiredRaw != nil {
			t := time.Unix(*lastFiredRaw, 0).UTC()
			ss.LastFiredAt = &t
		}
		if lastRunID != nil {
			ss.LastRunID = *lastRunID
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// Upsert implements Storage.
func (s *SQLiteStorage) Upsert(ctx context.Context, ss StoredSchedule) error {
	enabledInt := 0
	if ss.Enabled {
		enabledInt = 1
	}
	const q = `
		INSERT INTO workflow_schedules
			(workflow_id, cron, timezone, enabled, last_fired_at, last_run_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workflow_id) DO UPDATE SET
			cron         = excluded.cron,
			timezone     = excluded.timezone,
			enabled      = excluded.enabled,
			last_fired_at = excluded.last_fired_at,
			last_run_id  = excluded.last_run_id
	`
	var lastFiredRaw *int64
	if ss.LastFiredAt != nil {
		v := ss.LastFiredAt.Unix()
		lastFiredRaw = &v
	}
	var lastRunID *string
	if ss.LastRunID != "" {
		lastRunID = &ss.LastRunID
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q,
			ss.WorkflowID, ss.Cron, ss.Timezone, enabledInt, lastFiredRaw, lastRunID,
		)
		return err
	})
}

// Delete implements Storage.
func (s *SQLiteStorage) Delete(ctx context.Context, workflowID string) error {
	const q = `DELETE FROM workflow_schedules WHERE workflow_id = ?`
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q, workflowID)
		return err
	})
}

// SetLastFired implements Storage.
func (s *SQLiteStorage) SetLastFired(ctx context.Context, workflowID, runID string, firedAt time.Time) error {
	const q = `
		UPDATE workflow_schedules
		SET last_fired_at = ?, last_run_id = ?
		WHERE workflow_id = ?
	`
	ts := firedAt.Unix()
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q, ts, runID, workflowID)
		return err
	})
}

var _ Storage = (*SQLiteStorage)(nil)
