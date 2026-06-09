package corpus

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// NewStorageDB wraps a storage.DB so the corpus manager can consume it
// through the narrower corpus.DB contract. The two surfaces share method
// shapes; this adapter only handles the type translation across packages
// so corpus does not import core/storage transitively.
func NewStorageDB(db storage.DB) DB {
	if db == nil {
		return nil
	}
	return &storageAdapter{db: db}
}

type storageAdapter struct{ db storage.DB }

func (a *storageAdapter) Reader() Reader {
	return storageReaderAdapter{r: a.db.Reader()}
}

func (a *storageAdapter) WriteTx(ctx context.Context, fn func(tx WriteTx) error) error {
	return a.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		return fn(storageWriteTxAdapter{tx: tx})
	})
}

type storageReaderAdapter struct{ r storage.Reader }

func (a storageReaderAdapter) QueryRow(ctx context.Context, query string, args ...any) Row {
	return storageRowAdapter{r: a.r.QueryRow(ctx, query, args...)}
}

func (a storageReaderAdapter) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rs, err := a.r.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return storageRowsAdapter{r: rs}, nil
}

type storageWriteTxAdapter struct{ tx storage.WriteTx }

func (a storageWriteTxAdapter) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	res, err := a.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return storageResultAdapter{r: res}, nil
}

func (a storageWriteTxAdapter) QueryRow(ctx context.Context, query string, args ...any) Row {
	return storageRowAdapter{r: a.tx.QueryRow(ctx, query, args...)}
}

func (a storageWriteTxAdapter) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rs, err := a.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return storageRowsAdapter{r: rs}, nil
}

type storageRowAdapter struct{ r storage.Row }

func (a storageRowAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }

type storageRowsAdapter struct{ r storage.Rows }

func (a storageRowsAdapter) Next() bool             { return a.r.Next() }
func (a storageRowsAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }
func (a storageRowsAdapter) Close() error           { return a.r.Close() }
func (a storageRowsAdapter) Err() error             { return a.r.Err() }

type storageResultAdapter struct{ r storage.Result }

func (a storageResultAdapter) RowsAffected() (int64, error) { return a.r.RowsAffected() }
func (a storageResultAdapter) LastInsertID() (int64, error) { return a.r.LastInsertID() }
