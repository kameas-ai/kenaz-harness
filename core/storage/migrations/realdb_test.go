package migrations

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// realDB is a migrations.Executor backed by an actual on-disk SQLite
// database (modernc.org/sqlite, the same driver core/storage/sqlite uses
// in production).
//
// WHY THIS EXISTS ALONGSIDE fakeDB.
//
// fakeDB is not a SQL engine — it pattern-matches a handful of statement
// prefixes and keeps the ledger in a []LedgerEntry. That makes it fast and
// useless for anything that depends on how SQL actually behaves: rowid
// ordering of the ledger, NULL scanning of rolled_back_from_version into an
// `any`, real transactional rollback, duplicate rows for one version. The
// max-based Pending() bug shipped past a suite that ran entirely on fakeDB.
//
// Every behavioural test in this package's Pending/VerifyLedger suite runs
// against BOTH executors (see executorFactories) so the fake can never again
// be the only witness.
type realDB struct{ db *sql.DB }

func newRealDB(t *testing.T) *realDB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &realDB{db: db}
}

func (e *realDB) WriteTx(ctx context.Context, fn func(tx WriteTx) error) error {
	if e.db == nil {
		return errors.New("realDB: closed")
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&realTx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (e *realDB) QueryRow(ctx context.Context, query string, args ...any) Row {
	return realRow{r: e.db.QueryRowContext(ctx, query, args...)}
}

func (e *realDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rs, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &realRows{rs: rs}, nil
}

type realTx struct{ tx *sql.Tx }

func (w *realTx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := w.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return realResult{res: res}, nil
}

func (w *realTx) QueryRow(ctx context.Context, query string, args ...any) Row {
	return realRow{r: w.tx.QueryRowContext(ctx, query, args...)}
}

func (w *realTx) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rs, err := w.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &realRows{rs: rs}, nil
}

type realRow struct{ r *sql.Row }

func (r realRow) Scan(dest ...any) error { return r.r.Scan(dest...) }

type realRows struct{ rs *sql.Rows }

func (r *realRows) Next() bool             { return r.rs.Next() }
func (r *realRows) Scan(dest ...any) error { return r.rs.Scan(dest...) }
func (r *realRows) Close() error           { return r.rs.Close() }
func (r *realRows) Err() error             { return r.rs.Err() }

type realResult struct{ res sql.Result }

func (r realResult) RowsAffected() (int64, error) { return r.res.RowsAffected() }
func (r realResult) LastInsertID() (int64, error) { return r.res.LastInsertId() }
