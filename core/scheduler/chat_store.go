// Package scheduler — SQLite-backed store for scheduled_chat_runs.
//
// ScheduledChatStore is the persistence layer for the
// scheduled-chat-runs-01KX5R8B mission (WP01). It is intentionally
// separate from the Job-based Store so the chat-run surface can evolve
// independently without touching the workflow-scheduler storage path.
package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// ErrChatRunNotFound is returned when a requested scheduled chat run
// does not exist in the store.
var ErrChatRunNotFound = errors.New("scheduler: scheduled chat run not found")

// ChatRunRecord is the DB-level projection of scheduled_chat_runs.
type ChatRunRecord struct {
	ID             string
	Name           string
	PromptTemplate string
	Cron           string
	Timezone       string
	Model          string
	OutputSink     string
	Enabled        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChatRunHistoryRecord is one row in scheduled_chat_run_history.
type ChatRunHistoryRecord struct {
	ID            string
	ChatRunID     string
	SessionID     string
	Status        string // completed | failed | running
	StartedAt     time.Time
	EndedAt       *time.Time
	OutputSnippet string
	Error         string
}

// ScheduledChatStore is the storage interface for scheduled chat runs.
//
// All methods are safe for concurrent use. The production implementation
// is SQLiteChatStore; tests may inject a stub.
type ScheduledChatStore interface {
	// Create inserts a new scheduled_chat_runs row. ID must be non-empty and unique.
	Create(ctx context.Context, r ChatRunRecord) error
	// Update replaces all mutable fields of an existing row.
	// Returns ErrChatRunNotFound when no row with r.ID exists.
	Update(ctx context.Context, r ChatRunRecord) error
	// Delete removes the row for id. ON DELETE CASCADE sweeps history rows.
	Delete(ctx context.Context, id string) error
	// Get returns the row for id or ErrChatRunNotFound.
	Get(ctx context.Context, id string) (ChatRunRecord, error)
	// List returns all rows ordered by created_at ascending.
	List(ctx context.Context) ([]ChatRunRecord, error)
	// SetEnabled flips the enabled flag for id.
	// Returns ErrChatRunNotFound when no row with id exists.
	SetEnabled(ctx context.Context, id string, enabled bool) error

	// AppendHistory records one execution outcome.
	AppendHistory(ctx context.Context, h ChatRunHistoryRecord) error
	// History returns up to limit rows for chatRunID in reverse-chronological order.
	History(ctx context.Context, chatRunID string, limit int) ([]ChatRunHistoryRecord, error)
}

// SQLiteChatStore is the production ScheduledChatStore backed by the
// harness storage.DB.
type SQLiteChatStore struct {
	db storage.DB
}

// NewSQLiteChatStore returns a ScheduledChatStore backed by db.
func NewSQLiteChatStore(db storage.DB) *SQLiteChatStore {
	return &SQLiteChatStore{db: db}
}

// Create implements ScheduledChatStore.
func (s *SQLiteChatStore) Create(ctx context.Context, r ChatRunRecord) error {
	const q = `
		INSERT INTO scheduled_chat_runs
			(id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	enabledInt := 0
	if r.Enabled {
		enabledInt = 1
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q,
			r.ID, r.Name, r.PromptTemplate, r.Cron, r.Timezone,
			r.Model, r.OutputSink, enabledInt,
			r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
		)
		return err
	})
}

// Update implements ScheduledChatStore.
func (s *SQLiteChatStore) Update(ctx context.Context, r ChatRunRecord) error {
	const q = `
		UPDATE scheduled_chat_runs
		SET name = ?, prompt_template = ?, cron = ?, timezone = ?,
		    model = ?, output_sink = ?, enabled = ?, updated_at = ?
		WHERE id = ?
	`
	enabledInt := 0
	if r.Enabled {
		enabledInt = 1
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, q,
			r.Name, r.PromptTemplate, r.Cron, r.Timezone,
			r.Model, r.OutputSink, enabledInt, r.UpdatedAt.Unix(),
			r.ID,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrChatRunNotFound
		}
		return nil
	})
}

// Delete implements ScheduledChatStore.
func (s *SQLiteChatStore) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM scheduled_chat_runs WHERE id = ?`
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q, id)
		return err
	})
}

// Get implements ScheduledChatStore.
func (s *SQLiteChatStore) Get(ctx context.Context, id string) (ChatRunRecord, error) {
	const q = `
		SELECT id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at
		FROM scheduled_chat_runs
		WHERE id = ?
	`
	row := s.db.Reader().QueryRow(ctx, q, id)
	return scanChatRunRecord(row)
}

// List implements ScheduledChatStore.
func (s *SQLiteChatStore) List(ctx context.Context) ([]ChatRunRecord, error) {
	const q = `
		SELECT id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at
		FROM scheduled_chat_runs
		ORDER BY created_at ASC
	`
	rows, err := s.db.Reader().Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatRunRecord
	for rows.Next() {
		r, err := scanChatRunRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEnabled implements ScheduledChatStore.
func (s *SQLiteChatStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	const q = `
		UPDATE scheduled_chat_runs SET enabled = ?, updated_at = ?
		WHERE id = ?
	`
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, q, enabledInt, time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrChatRunNotFound
		}
		return nil
	})
}

// AppendHistory implements ScheduledChatStore.
func (s *SQLiteChatStore) AppendHistory(ctx context.Context, h ChatRunHistoryRecord) error {
	const q = `
		INSERT INTO scheduled_chat_run_history
			(id, chat_run_id, session_id, status, started_at, ended_at, output_snippet, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	var endedAt *int64
	if h.EndedAt != nil {
		v := h.EndedAt.Unix()
		endedAt = &v
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q,
			h.ID, h.ChatRunID, h.SessionID, h.Status,
			h.StartedAt.Unix(), endedAt,
			h.OutputSnippet, h.Error,
		)
		return err
	})
}

// History implements ScheduledChatStore.
func (s *SQLiteChatStore) History(ctx context.Context, chatRunID string, limit int) ([]ChatRunHistoryRecord, error) {
	const q = `
		SELECT id, chat_run_id, session_id, status, started_at, ended_at, output_snippet, error
		FROM scheduled_chat_run_history
		WHERE chat_run_id = ?
		ORDER BY started_at DESC
		LIMIT ?
	`
	rows, err := s.db.Reader().Query(ctx, q, chatRunID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatRunHistoryRecord
	for rows.Next() {
		var h ChatRunHistoryRecord
		var endedAtRaw *int64
		startedAtRaw := int64(0)
		if err := rows.Scan(
			&h.ID, &h.ChatRunID, &h.SessionID, &h.Status,
			&startedAtRaw, &endedAtRaw,
			&h.OutputSnippet, &h.Error,
		); err != nil {
			return nil, err
		}
		h.StartedAt = time.Unix(startedAtRaw, 0).UTC()
		if endedAtRaw != nil {
			t := time.Unix(*endedAtRaw, 0).UTC()
			h.EndedAt = &t
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// scanChatRunRecord scans one row into a ChatRunRecord. Works for both
// *storage.Row (QueryRow) and rows.Scan (Query iteration).
type scanner interface {
	Scan(dest ...any) error
}

func scanChatRunRecord(row scanner) (ChatRunRecord, error) {
	var r ChatRunRecord
	var enabledInt int
	var createdAtRaw, updatedAtRaw int64
	if err := row.Scan(
		&r.ID, &r.Name, &r.PromptTemplate, &r.Cron, &r.Timezone,
		&r.Model, &r.OutputSink, &enabledInt,
		&createdAtRaw, &updatedAtRaw,
	); err != nil {
		return ChatRunRecord{}, ErrChatRunNotFound
	}
	r.Enabled = enabledInt != 0
	r.CreatedAt = time.Unix(createdAtRaw, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAtRaw, 0).UTC()
	return r, nil
}

// Compile-time interface check.
var _ ScheduledChatStore = (*SQLiteChatStore)(nil)
