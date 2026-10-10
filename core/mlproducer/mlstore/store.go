package mlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Outbox table / op values (kameas.ml.table / kameas.ml.op on the wire).
const (
	TableEvents = "events"
	TableTasks  = "tasks"
	OpInsert    = "insert"
	OpUpsert    = "upsert"
)

// Store is the sqlite-backed outbox + task store. Safe for concurrent use
// to the extent *sql.DB is; every multi-statement write is one transaction.
type Store struct {
	db *sql.DB
}

// New wraps the unified harness database (the *sql.DB storage.Open hands
// out). The ml-producer migrations must already have applied.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Record is one outbox row as the shipper reads it.
type Record struct {
	Seq       int64
	Table     string // TableEvents | TableTasks
	Op        string // OpInsert | OpUpsert
	RowID     string // decimal seq for events, the task id for tasks
	Body      []byte // JSON record body — never log it
	CreatedAt int64  // unix ms
}

// EventDraft is an event whose body depends on its own seq (the body's
// `id` must equal kameas.ml.row_id, which is the seq sqlite assigns).
type EventDraft struct {
	CreatedAt int64
	Body      func(seq int64) ([]byte, error)
}

// TaskRow is the persisted state of one task (spec §3.1).
type TaskRow struct {
	TaskID       string
	SessionHash  string
	RepoRootHash string
	Phase        string
	Files        map[string]int
	StartedAt    int64
	LastActive   int64
	CompletedAt  int64 // 0 = open
	CommitCount  int
	TestRuns     int
	TestFails    int
	LastUpsertAt int64
}

// Write is one atomic recorder step: zero or more events, the task's new
// state, and optionally a task upsert record for the outbox.
type Write struct {
	Events []EventDraft
	Task   *TaskRow
	// TaskUpsert, when non-nil, is enqueued as a tasks/upsert record for
	// Task.TaskID.
	TaskUpsert []byte
}

// Commit applies w in one transaction and returns the seqs assigned to
// w.Events, in order.
func (s *Store) Commit(ctx context.Context, w Write) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("mlstore: nil store")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mlstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	seqs := make([]int64, 0, len(w.Events))
	for _, ev := range w.Events {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO ml_outbox (tbl, op, row_id, body, created_at) VALUES (?, ?, '', '', ?)`,
			TableEvents, OpInsert, ev.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("mlstore: insert event: %w", err)
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("mlstore: event seq: %w", err)
		}
		body, err := ev.Body(seq)
		if err != nil {
			return nil, fmt.Errorf("mlstore: event body: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE ml_outbox SET row_id = ?, body = ? WHERE seq = ?`,
			strconv.FormatInt(seq, 10), string(body), seq); err != nil {
			return nil, fmt.Errorf("mlstore: finish event: %w", err)
		}
		seqs = append(seqs, seq)
	}
	if w.Task != nil {
		if err := saveTask(ctx, tx, *w.Task); err != nil {
			return nil, err
		}
		if w.TaskUpsert != nil {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO ml_outbox (tbl, op, row_id, body, created_at) VALUES (?, ?, ?, ?, ?)`,
				TableTasks, OpUpsert, w.Task.TaskID, string(w.TaskUpsert), w.Task.LastActive); err != nil {
				return nil, fmt.Errorf("mlstore: insert task upsert: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("mlstore: commit: %w", err)
	}
	return seqs, nil
}

func saveTask(ctx context.Context, tx *sql.Tx, t TaskRow) error {
	files := t.Files
	if files == nil {
		files = map[string]int{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("mlstore: task files: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ml_tasks (task_id, session_hash, repo_root_hash, phase, files_json,
		  started_at, last_active, completed_at, commit_count, test_runs, test_fails, last_upsert_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
		  session_hash = excluded.session_hash,
		  repo_root_hash = excluded.repo_root_hash,
		  phase = excluded.phase,
		  files_json = excluded.files_json,
		  started_at = excluded.started_at,
		  last_active = excluded.last_active,
		  completed_at = excluded.completed_at,
		  commit_count = excluded.commit_count,
		  test_runs = excluded.test_runs,
		  test_fails = excluded.test_fails,
		  last_upsert_at = excluded.last_upsert_at`,
		t.TaskID, t.SessionHash, t.RepoRootHash, t.Phase, string(filesJSON),
		t.StartedAt, t.LastActive, t.CompletedAt, t.CommitCount, t.TestRuns, t.TestFails, t.LastUpsertAt)
	if err != nil {
		return fmt.Errorf("mlstore: save task: %w", err)
	}
	return nil
}

const taskColumns = `task_id, session_hash, repo_root_hash, phase, files_json,
	started_at, last_active, completed_at, commit_count, test_runs, test_fails, last_upsert_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(r rowScanner) (TaskRow, error) {
	var t TaskRow
	var filesJSON string
	if err := r.Scan(&t.TaskID, &t.SessionHash, &t.RepoRootHash, &t.Phase, &filesJSON,
		&t.StartedAt, &t.LastActive, &t.CompletedAt, &t.CommitCount, &t.TestRuns, &t.TestFails, &t.LastUpsertAt); err != nil {
		return TaskRow{}, err
	}
	t.Files = map[string]int{}
	if filesJSON != "" {
		if err := json.Unmarshal([]byte(filesJSON), &t.Files); err != nil {
			return TaskRow{}, fmt.Errorf("mlstore: decode task files: %w", err)
		}
	}
	return t, nil
}

// LoadTask returns the persisted task, or ok=false when there is none.
func (s *Store) LoadTask(ctx context.Context, taskID string) (TaskRow, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM ml_tasks WHERE task_id = ?`, taskID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, false, nil
	}
	if err != nil {
		return TaskRow{}, false, fmt.Errorf("mlstore: load task: %w", err)
	}
	return t, true, nil
}

// OpenTasksIdleSince returns open tasks whose last_active is older than
// cutoff (unix ms) — the 7-day idle sweep's candidates.
func (s *Store) OpenTasksIdleSince(ctx context.Context, cutoff int64) ([]TaskRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM ml_tasks WHERE completed_at = 0 AND last_active < ? ORDER BY last_active`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("mlstore: idle tasks: %w", err)
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// StaleTasks returns tasks whose newest state has not been upserted yet
// (last_active > last_upsert_at) and whose last upsert is at or before
// cutoff (unix ms) — the recorder's trailing-upsert candidates.
func (s *Store) StaleTasks(ctx context.Context, cutoff int64) ([]TaskRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM ml_tasks WHERE last_active > last_upsert_at AND last_upsert_at <= ? ORDER BY last_active`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("mlstore: stale tasks: %w", err)
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReadBatch returns up to limit outbox records with seq > afterSeq, in seq
// order. The shipper's cursor is the table head: it reads from 0 and
// deletes what it shipped.
func (s *Store) ReadBatch(ctx context.Context, afterSeq int64, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, tbl, op, row_id, body, created_at FROM ml_outbox WHERE seq > ? ORDER BY seq LIMIT ?`,
		afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("mlstore: read batch: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var body string
		if err := rows.Scan(&r.Seq, &r.Table, &r.Op, &r.RowID, &body, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("mlstore: scan batch: %w", err)
		}
		r.Body = []byte(body)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteThrough deletes every outbox record with seq <= seq (the shipper
// calls it after a 2xx) and returns how many went.
func (s *Store) DeleteThrough(ctx context.Context, seq int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ml_outbox WHERE seq <= ?`, seq)
	if err != nil {
		return 0, fmt.Errorf("mlstore: delete through: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Pending returns the number of unsent outbox records.
func (s *Store) Pending(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ml_outbox`).Scan(&n); err != nil {
		return 0, fmt.Errorf("mlstore: pending: %w", err)
	}
	return n, nil
}

// Purge deletes every outbox record and every task row in one transaction
// (spec §4: nothing recorded under a gate that has since closed may
// resurface). AUTOINCREMENT keeps seq monotonic across it.
func (s *Store) Purge(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mlstore: purge begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{`DELETE FROM ml_outbox`, `DELETE FROM ml_tasks`} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("mlstore: purge: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mlstore: purge commit: %w", err)
	}
	return nil
}
