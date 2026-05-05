package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// reader implements storage.Reader against *sql.DB.
type reader struct{ db *sql.DB }

func (r *reader) QueryRow(ctx context.Context, query string, args ...any) storage.Row {
	return rowAdapter{r: r.db.QueryRowContext(ctx, query, args...)}
}

func (r *reader) Query(ctx context.Context, query string, args ...any) (storage.Rows, error) {
	rs, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// writeTx implements storage.WriteTx against *sql.Tx.
type writeTx struct{ tx *sql.Tx }

func (w *writeTx) Exec(ctx context.Context, query string, args ...any) (storage.Result, error) {
	res, err := w.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return resultAdapter{res: res}, nil
}

func (w *writeTx) QueryRow(ctx context.Context, query string, args ...any) storage.Row {
	return rowAdapter{r: w.tx.QueryRowContext(ctx, query, args...)}
}

func (w *writeTx) Query(ctx context.Context, query string, args ...any) (storage.Rows, error) {
	rs, err := w.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// rowAdapter / rowsAdapter / resultAdapter wrap database/sql primitives
// to satisfy both storage.* and migrations.* interfaces (they share the
// same shape).

type rowAdapter struct{ r *sql.Row }

func (ra rowAdapter) Scan(dest ...any) error { return ra.r.Scan(dest...) }

type rowsAdapter struct{ rs *sql.Rows }

func (rs *rowsAdapter) Next() bool            { return rs.rs.Next() }
func (rs *rowsAdapter) Scan(dest ...any) error { return rs.rs.Scan(dest...) }
func (rs *rowsAdapter) Close() error           { return rs.rs.Close() }
func (rs *rowsAdapter) Err() error             { return rs.rs.Err() }

type resultAdapter struct{ res sql.Result }

func (r resultAdapter) RowsAffected() (int64, error) { return r.res.RowsAffected() }
func (r resultAdapter) LastInsertID() (int64, error) { return r.res.LastInsertId() }

// executor wraps *sql.DB as a migrations.Executor. Reads run against the
// pool; writes go through a tx.
type executor struct{ db *sql.DB }

func newExecutor(db *sql.DB) *executor { return &executor{db: db} }

func (e *executor) WriteTx(ctx context.Context, fn func(tx migrations.WriteTx) error) error {
	if e.db == nil {
		return errors.New("storage: executor closed")
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&migWriteTx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (e *executor) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	return rowAdapter{r: e.db.QueryRowContext(ctx, query, args...)}
}

func (e *executor) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	rs, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// migWriteTx wraps *sql.Tx for the migration framework.
type migWriteTx struct{ tx *sql.Tx }

func (w *migWriteTx) Exec(ctx context.Context, query string, args ...any) (migrations.Result, error) {
	res, err := w.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return resultAdapter{res: res}, nil
}

func (w *migWriteTx) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	return rowAdapter{r: w.tx.QueryRowContext(ctx, query, args...)}
}

func (w *migWriteTx) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	rs, err := w.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &rowsAdapter{rs: rs}, nil
}

// registryAdapter exposes migrations.Registry through the public
// storage.MigrationRegistry surface. The two interfaces live in
// different packages with parallel shapes; we translate at the boundary.
type registryAdapter struct{ reg *migrations.Registry }

func (a *registryAdapter) Register(m storage.Migration) error {
	return a.reg.Register(toMigrationsMig(m))
}

func (a *registryAdapter) Applied() ([]storage.LedgerEntry, error) {
	rows, err := a.reg.Applied()
	if err != nil {
		return nil, err
	}
	out := make([]storage.LedgerEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromMigrationsLedger(r))
	}
	return out, nil
}

func (a *registryAdapter) Pending() ([]storage.Migration, error) {
	rows, err := a.reg.Pending()
	if err != nil {
		return nil, err
	}
	out := make([]storage.Migration, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromMigrationsMig(r))
	}
	return out, nil
}

func (a *registryAdapter) All() []storage.Migration {
	migs := a.reg.All()
	out := make([]storage.Migration, 0, len(migs))
	for _, m := range migs {
		out = append(out, fromMigrationsMig(m))
	}
	return out
}

func (a *registryAdapter) Apply(ctx context.Context) error {
	return a.reg.Apply(ctx)
}

func (a *registryAdapter) Rollback(ctx context.Context, toVersion int) error {
	return a.reg.Rollback(ctx, toVersion)
}

func toMigrationsMig(m storage.Migration) migrations.Migration {
	return migrations.Migration{
		ID:            m.ID,
		Version:       m.Version,
		OwningMission: m.OwningMission,
		UpSource:      m.UpSource,
		ContentHash:   m.ContentHash,
		Up:            wrapTxFunc(m.Up),
		Down:          wrapTxFunc(m.Down),
	}
}

func fromMigrationsMig(m migrations.Migration) storage.Migration {
	return storage.Migration{
		ID:            m.ID,
		Version:       m.Version,
		OwningMission: m.OwningMission,
		UpSource:      m.UpSource,
		ContentHash:   m.ContentHash,
		// Up / Down funcs live in their owning package; the public
		// MigrationRegistry surface only exposes metadata here.
	}
}

func fromMigrationsLedger(e migrations.LedgerEntry) storage.LedgerEntry {
	return storage.LedgerEntry{
		Version:               e.Version,
		ID:                    e.ID,
		AppliedAt:             e.AppliedAt,
		ContentHash:           e.ContentHash,
		OwningMission:         e.OwningMission,
		Action:                storage.LedgerAction(e.Action),
		RolledBackFromVersion: e.RolledBackFromVersion,
	}
}

func wrapTxFunc(fn func(ctx context.Context, tx storage.WriteTx) error) func(ctx context.Context, tx migrations.WriteTx) error {
	if fn == nil {
		return nil
	}
	return func(ctx context.Context, tx migrations.WriteTx) error {
		return fn(ctx, &migrationWriteTxAdapter{tx: tx})
	}
}

// migrationWriteTxAdapter exposes a migrations.WriteTx as a
// storage.WriteTx so callers that registered Up/Down with storage.WriteTx
// signatures keep working.
type migrationWriteTxAdapter struct{ tx migrations.WriteTx }

func (a *migrationWriteTxAdapter) Exec(ctx context.Context, query string, args ...any) (storage.Result, error) {
	res, err := a.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return migrationsResultAdapter{r: res}, nil
}

func (a *migrationWriteTxAdapter) QueryRow(ctx context.Context, query string, args ...any) storage.Row {
	return migrationsRowAdapter{r: a.tx.QueryRow(ctx, query, args...)}
}

func (a *migrationWriteTxAdapter) Query(ctx context.Context, query string, args ...any) (storage.Rows, error) {
	rs, err := a.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return migrationsRowsAdapter{r: rs}, nil
}

type migrationsRowAdapter struct{ r migrations.Row }

func (a migrationsRowAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }

type migrationsRowsAdapter struct{ r migrations.Rows }

func (a migrationsRowsAdapter) Next() bool             { return a.r.Next() }
func (a migrationsRowsAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }
func (a migrationsRowsAdapter) Close() error           { return a.r.Close() }
func (a migrationsRowsAdapter) Err() error             { return a.r.Err() }

type migrationsResultAdapter struct{ r migrations.Result }

func (a migrationsResultAdapter) RowsAffected() (int64, error) { return a.r.RowsAffected() }
func (a migrationsResultAdapter) LastInsertID() (int64, error) { return a.r.LastInsertID() }
