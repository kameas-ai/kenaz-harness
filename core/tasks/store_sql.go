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
// harness migration framework runs the tasks/1200-tasks-init migration at
// boot — see migrations.go — as long as RegisterMigrations was called
// before storage.Open).
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
