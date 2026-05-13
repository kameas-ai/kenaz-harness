package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// sqliteStore implements SQLStore against a *sql.DB opened with the
// modernc.org/sqlite driver (no CGo).
type sqliteStore struct {
	db *sql.DB
}

// NewSQLiteStore wraps an open *sql.DB.
//
// The caller is responsible for ensuring the tasks table exists (the
// harness migration framework runs 0001_tasks.sql at boot via
// Migrations()).
func NewSQLiteStore(db *sql.DB) SQLStore {
	return &sqliteStore{db: db}
}

func (s *sqliteStore) Insert(ctx context.Context, t Task) error {
	startedAtUnix := t.StartedAt.UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks
		    (id, kind, owner_session_id, cmd, description, status, exit_code, started_at, ended_at)
		VALUES
		    (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO NOTHING`,
		t.ID, t.Kind, t.OwnerSessionID, t.Cmd, t.Description,
		t.Status, t.ExitCode, startedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("tasks store insert: %w", err)
	}
	return nil
}

func (s *sqliteStore) UpdateStatus(ctx context.Context, id, status string, exitCode int, endedAt time.Time) error {
	endedAtUnix := endedAt.UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status=?, exit_code=?, ended_at=? WHERE id=?`,
		status, exitCode, endedAtUnix, id,
	)
	if err != nil {
		return fmt.Errorf("tasks store update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkCrashed updates all running rows to status=crashed. It does not
// check live PIDs (that's handled by recovery.go which calls
// os.FindProcess; the SQL-only path just marks everything that was
// "running" at boot).
func (s *sqliteStore) MarkCrashed(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET status='crashed', ended_at=?
		WHERE status='running'`,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("tasks store mark_crashed: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Migration shape — mirrors core/event/log/register.go so the harness
// bootstrap can call Migrations() and forward them.
type Migration struct {
	ID  string
	SQL string
}

// Migrations returns the tasks migration in registration order.
func Migrations() []Migration {
	return []Migration{
		{ID: "0001_tasks", SQL: migration0001Tasks},
	}
}

// MigrationRegistry is the minimal interface the harness migration framework
// exposes. Tasks registers itself via RegisterMigrations.
type MigrationRegistry interface {
	Register(owner string, migrations []Migration) error
}

// RegisterMigrations registers the tasks migrations with the supplied registry.
func RegisterMigrations(reg MigrationRegistry) error {
	return reg.Register("core/tasks", Migrations())
}

// migration0001Tasks is the SQL for the tasks table and indexes.
const migration0001Tasks = `-- Migration 0001: tasks table + indexes.
-- Owner: core/tasks (background-task-monitor-01KZNP3C).

CREATE TABLE IF NOT EXISTS tasks (
  id               TEXT PRIMARY KEY,
  kind             TEXT NOT NULL,
  owner_session_id TEXT,
  cmd              TEXT,
  description      TEXT,
  status           TEXT NOT NULL,
  exit_code        INTEGER,
  started_at       INTEGER NOT NULL,
  ended_at         INTEGER
);

CREATE INDEX IF NOT EXISTS idx_tasks_owner  ON tasks(owner_session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
`

// GetTask is a convenience reader for recovery.go.
func (s *sqliteStore) GetRunningIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks WHERE status='running'`)
	if err != nil {
		return nil, fmt.Errorf("tasks store get_running: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetByID returns a Task row or ErrNotFound.
func (s *sqliteStore) GetByID(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, kind, owner_session_id, cmd, description, status, exit_code, started_at, ended_at
		FROM tasks WHERE id=?`, id)
	return scanTaskRow(row)
}

func scanTaskRow(row *sql.Row) (Task, error) {
	var t Task
	var ownerSessionID, cmd, description sql.NullString
	var exitCode sql.NullInt64
	var startedAtMs int64
	var endedAtMs sql.NullInt64

	err := row.Scan(
		&t.ID, &t.Kind, &ownerSessionID, &cmd, &description,
		&t.Status, &exitCode, &startedAtMs, &endedAtMs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("tasks store scan: %w", err)
	}
	t.OwnerSessionID = ownerSessionID.String
	t.Cmd = cmd.String
	t.Description = description.String
	t.ExitCode = int(exitCode.Int64)
	t.StartedAt = time.UnixMilli(startedAtMs)
	if endedAtMs.Valid {
		endedAt := time.UnixMilli(endedAtMs.Int64)
		t.EndedAt = &endedAt
	}
	return t, nil
}
