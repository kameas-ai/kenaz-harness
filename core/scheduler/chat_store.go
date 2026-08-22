// Package scheduler — SQLite-backed store for scheduled_chat_runs.
//
// ScheduledChatStore is the persistence layer for the
// scheduled-chat-runs-01KX5R8B mission (WP01). It is intentionally
// separate from the Job-based Store so the chat-run surface can evolve
// independently without touching the workflow-scheduler storage path.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// ErrChatRunNotFound is returned when a requested scheduled chat run
// does not exist in the store.
var ErrChatRunNotFound = errors.New("scheduler: scheduled chat run not found")

// ScheduledRunCreatedByUser / ScheduledRunCreatedByModel are the two
// legal values of ChatRunRecord.CreatedBy (model-scheduled-jobs-01PMSJ01
// WP09, FR-005). Mirrors the shape of core/agentgraph.SpecProvenance —
// a string provenance marker on the record, stamped server-side by the
// creating code path (never taken from caller input) and branched on
// downstream (here, by the Cedar context attribute both
// core/policy/cedar.GateScheduledChatCreate and
// .GateScheduledChatExecute inject).
const (
	ScheduledRunCreatedByUser  = "user"
	ScheduledRunCreatedByModel = "model"
)

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
	// CreatedBy is one of ScheduledRunCreatedByUser / ...Model. Stamped
	// server-side at the creating call site
	// (core/rpc/views/scheduledchat.API.Create / .CreateAsModel) — never
	// settable through CreateInput/UpdateInput, and Update's SQL (below)
	// deliberately omits this column so an edit cannot change it either.
	CreatedBy string
	// ToolAllowlist is the tool-name allowlist declared at creation time.
	// nil/empty means "no allowlist declared". Owner ruling B-3
	// (2026-08-19): a CreatedBy == ScheduledRunCreatedByModel row with an
	// empty ToolAllowlist must never execute — see
	// core/policy/cedar/hooks.go GateScheduledChatExecute and
	// core/policy/cedar/policies/default_scheduled_run_policy.cedar.
	ToolAllowlist []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// encodeToolAllowlist JSON-encodes a tool allowlist for storage. A nil
// or empty slice encodes to "" (not "[]" or "null") so the empty state
// is a single unambiguous string F1's Cedar context check
// (has_tool_allowlist) and the create-time guard in
// core/rpc/views/scheduledchat can test with a plain != "" comparison.
func encodeToolAllowlist(list []string) string {
	if len(list) == 0 {
		return ""
	}
	b, err := json.Marshal(list)
	if err != nil {
		// list is a []string; json.Marshal on a []string cannot fail.
		return ""
	}
	return string(b)
}

// decodeToolAllowlist reverses encodeToolAllowlist. A malformed value
// (should not occur outside hand-edited DBs) decodes to nil, which is
// the fail-safe direction: an unparseable allowlist reads as "no
// allowlist declared," not as "unrestricted."
func decodeToolAllowlist(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
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
			(id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at, created_by, tool_allowlist)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	enabledInt := 0
	if r.Enabled {
		enabledInt = 1
	}
	createdBy := r.CreatedBy
	if createdBy == "" {
		createdBy = ScheduledRunCreatedByUser
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, q,
			r.ID, r.Name, r.PromptTemplate, r.Cron, r.Timezone,
			r.Model, r.OutputSink, enabledInt,
			r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
			createdBy, encodeToolAllowlist(r.ToolAllowlist),
		)
		return err
	})
}

// Update implements ScheduledChatStore.
//
// created_by is deliberately absent from both the column list and the
// bound args below — Update cannot change who created a row. This is
// the enforcement half of "stamped server-side, never settable by the
// caller" (FR-005): even if a future caller smuggled a CreatedBy value
// into the ChatRunRecord passed here, this statement has no column to
// write it into.
func (s *SQLiteChatStore) Update(ctx context.Context, r ChatRunRecord) error {
	const q = `
		UPDATE scheduled_chat_runs
		SET name = ?, prompt_template = ?, cron = ?, timezone = ?,
		    model = ?, output_sink = ?, enabled = ?, updated_at = ?, tool_allowlist = ?
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
			encodeToolAllowlist(r.ToolAllowlist),
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
		SELECT id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at, created_by, tool_allowlist
		FROM scheduled_chat_runs
		WHERE id = ?
	`
	row := s.db.Reader().QueryRow(ctx, q, id)
	return scanChatRunRecord(row)
}

// List implements ScheduledChatStore.
func (s *SQLiteChatStore) List(ctx context.Context) ([]ChatRunRecord, error) {
	const q = `
		SELECT id, name, prompt_template, cron, timezone, model, output_sink, enabled, created_at, updated_at, created_by, tool_allowlist
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
	var toolAllowlistRaw string
	if err := row.Scan(
		&r.ID, &r.Name, &r.PromptTemplate, &r.Cron, &r.Timezone,
		&r.Model, &r.OutputSink, &enabledInt,
		&createdAtRaw, &updatedAtRaw,
		&r.CreatedBy, &toolAllowlistRaw,
	); err != nil {
		return ChatRunRecord{}, ErrChatRunNotFound
	}
	r.Enabled = enabledInt != 0
	r.CreatedAt = time.Unix(createdAtRaw, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAtRaw, 0).UTC()
	r.ToolAllowlist = decodeToolAllowlist(toolAllowlistRaw)
	return r, nil
}

// Compile-time interface check.
var _ ScheduledChatStore = (*SQLiteChatStore)(nil)
