package migrations

import (
	"context"
	"errors"
)

// LedgerAction enumerates the kinds of rows in the harness_migrations
// ledger. Mirrored from storage.LedgerAction so this package can compile
// without importing the parent.
type LedgerAction string

const (
	LedgerActionApplied    LedgerAction = "applied"
	LedgerActionRolledBack LedgerAction = "rolled_back"
)

// Migration is a versioned, ordered, idempotent unit of schema change.
// Mirrors storage.Migration — adapter functions in the storage package
// translate between them.
type Migration struct {
	ID            string
	Version       int
	OwningMission string
	Up            func(ctx context.Context, tx WriteTx) error
	Down          func(ctx context.Context, tx WriteTx) error
	UpSource      string
	ContentHash   string
}

// LedgerEntry mirrors storage.LedgerEntry.
type LedgerEntry struct {
	Version               int
	ID                    string
	AppliedAt             string
	ContentHash           string
	OwningMission         string
	Action                LedgerAction
	RolledBackFromVersion *int
}

// WriteTx is the minimal write-transaction surface migrations need. It
// mirrors storage.WriteTx; the storage package provides an adapter from
// storage.WriteTx to migrations.WriteTx.
type WriteTx interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Row, Rows, Result mirror the storage equivalents.
type Row interface {
	Scan(dest ...any) error
}

type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

type Result interface {
	RowsAffected() (int64, error)
	LastInsertID() (int64, error)
}

// Executor is what the migration runner uses to acquire write
// transactions. The storage package wires up an Executor backed by its
// libSQL connection; tests wire up an in-memory fake.
type Executor interface {
	WriteTx(ctx context.Context, fn func(tx WriteTx) error) error
	// Reader-style point query for ledger reads. Storage wires this
	// from its read pool.
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Now is the clock used to stamp ledger entries. Tests override.
type Now func() string

// EmitFunc is the per-event emission callback. Storage wires this to its
// internal emit helper which routes to the active EventSink. The
// migrations package never imports the EventSink directly.
type EmitFunc func(ctx context.Context, kind string, payload map[string]any)

// Sentinel errors. Mirrored from storage.* — same identity preserved
// when wrapped at the boundary.
var (
	ErrMigrationFailed       = errors.New("storage: migration failed")
	ErrLedgerHashMismatch    = errors.New("storage: migration content hash differs from ledger")
	ErrSchemaGap             = errors.New("storage: migration ledger has gaps or unknown entries")
	ErrVersionOutOfBlock     = errors.New("storage: migration version outside owning mission's reserved block")
	ErrVersionCollision      = errors.New("storage: two migrations registered at the same version")
	ErrMigrationIDCollision  = errors.New("storage: two migrations registered with the same ID")
	ErrUnknownOwningMission  = errors.New("storage: migration owning mission has no reserved version block")
	ErrInvalidMigration      = errors.New("storage: migration is invalid")
)
