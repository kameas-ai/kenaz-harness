// Package sqlitedb is a focused SQLite adapter that satisfies the
// session package's DB / Reader / WriteTx contracts. It exists so the
// SessionManager can persist to disk before the broader storage
// subsystem (vector backend, encryption, integrity audits) is wired.
//
// Backing driver: modernc.org/sqlite (pure-Go, no CGO). Connection
// pooled by database/sql. Foreign keys + WAL enabled.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB and exposes the Reader / WriteTx surface the
// session.Store consumes.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path. Foreign keys and
// WAL are enabled by default.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("sqlitedb: mkdir: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: open: %w", err)
	}
	// modernc.org/sqlite is safe for concurrent reads but writes
	// serialise behind a single connection — keep MaxOpenConns at 1
	// for writes and rely on the BUSY timeout for any other consumer.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitedb: ping: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the underlying *sql.DB.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Reader returns the read surface session.Store consumes.
func (d *DB) Reader() session.Reader {
	return &reader{db: d.db}
}

// WriteTx runs fn inside a transaction and commits on success.
// On error the transaction is rolled back and the error returned.
func (d *DB) WriteTx(ctx context.Context, fn func(tx session.WriteTx) error) error {
	if d == nil || d.db == nil {
		return errors.New("sqlitedb: closed")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&writeTx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// EnsureSessionsSchema applies the session-package's schema migrations
// as a single idempotent transaction. We bypass the broader migration
// framework here so the session feature can run without the storage
// subsystem online.
func (d *DB) EnsureSessionsSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id              TEXT PRIMARY KEY,
			name            TEXT NOT NULL,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			last_active_at  INTEGER NOT NULL,
			position        INTEGER NOT NULL,
			draft           TEXT NOT NULL DEFAULT '',
			scroll_position INTEGER NOT NULL DEFAULT 0,
			archived_at     INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS session_messages (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			sequence    INTEGER NOT NULL,
			role        TEXT NOT NULL,
			content     TEXT NOT NULL,
			tool_calls  TEXT,
			created_at  INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_session_seq
			ON session_messages (session_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_position
			ON sessions (position)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at
			ON sessions (last_active_at)`,
	}
	return d.WriteTx(ctx, func(tx session.WriteTx) error {
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s); err != nil {
				return fmt.Errorf("sqlitedb: schema %q: %w", firstLine(s), err)
			}
		}
		return nil
	})
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// ── reader ────────────────────────────────────────────────────────────

type reader struct{ db *sql.DB }

func (r *reader) QueryRow(ctx context.Context, query string, args ...any) session.Row {
	return rowAdapter{r: r.db.QueryRowContext(ctx, query, args...)}
}

func (r *reader) Query(ctx context.Context, query string, args ...any) (session.Rows, error) {
	rs, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// ── writeTx ───────────────────────────────────────────────────────────

type writeTx struct{ tx *sql.Tx }

func (w *writeTx) Exec(ctx context.Context, query string, args ...any) (session.Result, error) {
	res, err := w.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return resultAdapter{res: res}, nil
}

func (w *writeTx) QueryRow(ctx context.Context, query string, args ...any) session.Row {
	return rowAdapter{r: w.tx.QueryRowContext(ctx, query, args...)}
}

func (w *writeTx) Query(ctx context.Context, query string, args ...any) (session.Rows, error) {
	rs, err := w.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// ── adapters around database/sql primitives ───────────────────────────

type rowAdapter struct{ r *sql.Row }

func (ra rowAdapter) Scan(dest ...any) error { return ra.r.Scan(dest...) }

type rowsAdapter struct{ rs *sql.Rows }

func (rs *rowsAdapter) Next() bool                 { return rs.rs.Next() }
func (rs *rowsAdapter) Scan(dest ...any) error      { return rs.rs.Scan(dest...) }
func (rs *rowsAdapter) Close() error                { return rs.rs.Close() }
func (rs *rowsAdapter) Err() error                  { return rs.rs.Err() }

type resultAdapter struct{ res sql.Result }

func (r resultAdapter) RowsAffected() (int64, error) { return r.res.RowsAffected() }
