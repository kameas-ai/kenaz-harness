// Package sqlite is the concrete storage backend for the harness's
// unified DB (DIRECTIVE_001). It implements storage.DB on top of
// modernc.org/sqlite — pure-Go SQLite, no CGO. v1 ships only the
// relational substrate needed by core/session; vector / backup /
// diagnostics surfaces are stubbed and return storage.ErrNotImplemented.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagedb "github.com/kameas-ai/kenaz-harness/core/storage/db"
	"github.com/kameas-ai/kenaz-harness/core/storage/internal/lockfile"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
	"github.com/kameas-ai/kenaz-harness/core/units"

	_ "modernc.org/sqlite"
)

// Open returns a storage.DB rooted at <cfg.DataDir>/data.db. It applies
// the storage-bootstrap and any pre-registered migrations under the
// supplied registry function. v1 refuses encryption-at-rest and
// non-local mounts (unless explicitly overridden in Config).
func Open(cfg storage.Config) (storage.DB, error) {
	cfg = applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	if cfg.EncryptionStatus == storage.EncryptionStatusEnabled {
		// v1 punts on encryption. The unified DB still encodes the
		// operator's choice in Config so the upgrade path is mechanical.
		return nil, fmt.Errorf("%w: encryption-at-rest", storage.ErrNotImplemented)
	}

	// Mount-kind check. WAL on a network or cloud-sync filesystem
	// silently corrupts the journal; refuse unless the operator
	// explicitly opts in.
	mountReport, mountErr := storagedb.CheckMount(cfg.DataDir)
	if mountErr != nil {
		return nil, fmt.Errorf("storage: mount check: %w", mountErr)
	}
	if !mountReport.IsLocal() && !cfg.AllowNonLocalMount {
		return nil, fmt.Errorf("%w: %s", storage.ErrNonLocalMount, &storage.MountKindError{
			Kind:   string(mountReport.Kind),
			Detail: mountReport.Detail,
			Path:   mountReport.Path,
		})
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("storage: mkdir data dir: %w", err)
	}
	dbPath := filepath.Join(cfg.DataDir, "data.db")
	lockPath := dbPath + ".harness-lock"

	lockHandle, err := lockfile.Acquire(lockPath)
	if err != nil {
		var holder *lockfile.HolderError
		if errors.As(err, &holder) {
			return nil, fmt.Errorf("%w: %s", storage.ErrDBLocked, &storage.LockHeldError{
				HolderPID: holder.PID,
				HolderAt:  holder.StartedAt,
			})
		}
		return nil, fmt.Errorf("%w: %v", storage.ErrDBLocked, err)
	}

	dsn := "file:" + url.PathEscape(dbPath) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lockHandle.Release()
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	// modernc.org/sqlite serialises writes per-connection. One open
	// connection plus the BUSY timeout keeps the unified DB safe for
	// concurrent readers/writers across goroutines.
	rawDB.SetMaxOpenConns(1)
	if err := rawDB.PingContext(context.Background()); err != nil {
		_ = rawDB.Close()
		_ = lockHandle.Release()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}

	db := &concreteDB{
		path: dbPath,
		raw:  rawDB,
		lock: lockHandle,
	}

	exec := newExecutor(rawDB)
	registry := migrations.NewRegistry(exec, nil, nil)
	for _, m := range migrations.StorageBootstrap() {
		if err := registry.Register(m); err != nil {
			db.closeOnError()
			return nil, fmt.Errorf("storage: register bootstrap: %w", err)
		}
	}
	// v1 owns session-table migrations directly. When other missions
	// (secrets-keychain, scheduler, …) move into the unified DB they
	// will register here too — separate missions, not this one.
	// event-log moved in below (audit-that-tells-the-truth-01PMZA10).
	if err := session.RegisterMigrations(registry); err != nil {
		db.closeOnError()
		return nil, fmt.Errorf("storage: register session migrations: %w", err)
	}
	if err := slashcmd.RegisterMigrations(registry); err != nil {
		db.closeOnError()
		return nil, fmt.Errorf("storage: register slashcmd migrations: %w", err)
	}
	if err := units.RegisterMigrations(registry); err != nil {
		db.closeOnError()
		return nil, fmt.Errorf("storage: register units migrations: %w", err)
	}
	// event-log: audit-that-tells-the-truth-01PMZA10 UNIT-2. The
	// migrations land inert here — nothing constructs an
	// eventlog.SQLBackend against this registry yet (that is UNIT-3),
	// and nothing writes through it (UNIT-4). Registering them now is
	// still correct: it is what makes them apply on every install from
	// this point forward, INCLUDING upgraded installs whose ledger
	// high-water mark already sits well above the 100-199 block these
	// occupy (spec §1.3) — see core/storage/sqlite/upgrade_path_test.go.
	if err := eventlog.RegisterMigrations(registry); err != nil {
		db.closeOnError()
		return nil, fmt.Errorf("storage: register event-log migrations: %w", err)
	}
	db.registry = registry

	if err := migrations.EnsureLedger(context.Background(), exec); err != nil {
		db.closeOnError()
		return nil, fmt.Errorf("storage: ensure ledger: %w", err)
	}
	if err := registry.Apply(context.Background()); err != nil {
		db.closeOnError()
		return nil, err
	}

	// Post-apply invariant: Apply reported success, so EVERY registered
	// migration must now have an applied ledger row.
	//
	// It exists because the v0.63.0 P0 had no such tripwire. Pending()
	// selected on a GLOBAL max(applied) across a version space shared by every
	// mission, so on any database carrying units/1100..1103 the later
	// sessions/0332..0335 were silently declared already-done. Apply() then
	// returned nil having applied nothing, Open() succeeded, and the failure
	// surfaced much later and far away as "no such column: move_history_mode"
	// the first time the user clicked Start session. The boot-time drift
	// detector in core/rpc/api.go did notice, in a goroutine, after the UI had
	// already rendered — it logged a WARN and let the broken app run.
	//
	// THE CHECK IS COMPUTED FROM All() + Applied(), NOT FROM Pending().
	// That is the whole point and it is easy to get wrong: re-asking Pending()
	// after Apply cannot catch a selection bug, because a selection that wrongly
	// calls a migration done says so just as confidently the second time. This
	// check re-derives the answer from the registered set and the raw ledger,
	// so it is an independent witness. (Verified by mutation: reinstate the
	// max-based Pending() and this is what fires.)
	//
	// A schema the code did not compile against is not a state to continue
	// from. Refusing to open puts the failure at its cause, with the versions
	// named, instead of leaving it to whichever query hits the missing column
	// first.
	if err := verifyFullyApplied(registry); err != nil {
		db.closeOnError()
		return nil, err
	}

	logging.L().Info("storage.opened", "path", dbPath, "wal", true, "fk", true)
	return db, nil
}

// verifyFullyApplied reports an error unless every registered migration has
// an effective applied row in the ledger. See the call site in Open for why
// it re-derives the applied set instead of calling Pending().
//
// "Effective" matches the ledger's own append-only rule: rows arrive in
// insertion order and the most recent row for a version wins, so a version
// that was applied and later rolled back does not count as applied.
func verifyFullyApplied(registry *migrations.Registry) error {
	rows, err := registry.Applied()
	if err != nil {
		return fmt.Errorf("storage: post-apply ledger read: %w", err)
	}
	latest := make(map[int]migrations.LedgerEntry, len(rows))
	for _, e := range rows {
		latest[e.Version] = e
	}
	var missing []int
	for _, m := range registry.All() {
		if e, ok := latest[m.Version]; !ok || e.Action != migrations.LedgerActionApplied {
			missing = append(missing, m.Version)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: apply reported success but %d migration(s) have no applied ledger row: %v",
			storage.ErrMigrationFailed, len(missing), missing)
	}
	return nil
}

// defaultConfig returns a storage.Config with conservative defaults
// suitable for running with disk-encryption-only protection. Useful
// from helpers that need a Config without forcing the caller to know
// every encryption / vector / event knob.
func defaultConfig(dataDir string) storage.Config {
	return storage.Config{
		DataDir:          dataDir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
}

// applyDefaults re-uses the unexported Config helpers via a small
// shim — Config.applyDefaults / Config.validate live next to the type
// in core/storage/storage.go but are unexported, so we mirror their
// logic here.
func applyDefaults(cfg storage.Config) storage.Config {
	if cfg.VectorBackend == "" {
		cfg.VectorBackend = storage.VectorBackendSQLiteVec
	}
	if cfg.WAL == nil {
		t := true
		cfg.WAL = &t
	}
	if cfg.ForeignKeys == nil {
		t := true
		cfg.ForeignKeys = &t
	}
	if cfg.EventBufferSize <= 0 {
		cfg.EventBufferSize = 1024
	}
	if cfg.EncryptionStatus == "" {
		if cfg.EncryptionKey.Keychain != "" || len(cfg.EncryptionKey.Inline) > 0 {
			cfg.EncryptionStatus = storage.EncryptionStatusEnabled
		}
	}
	return cfg
}

func validate(cfg storage.Config) error {
	if cfg.DataDir == "" {
		return errors.New("storage: Config.DataDir required")
	}
	switch cfg.EncryptionStatus {
	case storage.EncryptionStatusEnabled,
		storage.EncryptionStatusDisabled,
		storage.EncryptionStatusDisabledWithDiskEncryption:
		// OK
	case "":
		return errors.New("storage: EncryptionStatus must be set explicitly (enabled | disabled | disabled_with_disk_encryption)")
	default:
		return errors.New("storage: EncryptionStatus has unknown value")
	}
	if cfg.EncryptionStatus == storage.EncryptionStatusEnabled {
		if cfg.EncryptionKey.Keychain == "" && len(cfg.EncryptionKey.Inline) == 0 {
			return storage.ErrEncryptionKeyMissing
		}
	}
	switch cfg.VectorBackend {
	case storage.VectorBackendSQLiteVec,
		storage.VectorBackendLanceDB,
		storage.VectorBackendChromem:
		// OK
	default:
		return errors.New("storage: VectorBackend has unknown value")
	}
	return nil
}

// concreteDB satisfies storage.DB. v1 only wires Reader / WriteTx /
// Migrations / SetEventSink / Close; the remaining interfaces return
// storage.ErrNotImplemented.
type concreteDB struct {
	path string
	raw  *sql.DB
	lock *lockfile.Handle

	registry *migrations.Registry

	sinkMu sync.Mutex
	sink   storage.EventSink
}

// closeOnError releases acquired resources after a failed Open.
func (d *concreteDB) closeOnError() {
	if d.raw != nil {
		_ = d.raw.Close()
	}
	if d.lock != nil {
		_ = d.lock.Release()
	}
}

// Reader returns the read-side surface.
func (d *concreteDB) Reader() storage.Reader {
	return &reader{db: d.raw}
}

// SQL returns the underlying *sql.DB. Exposed for narrow stdlib-
// shaped consumers (agentgraph.SQLEventLog, the memory hook journal
// writer) that wrap one INSERT or one SELECT per call. Callers MUST
// NOT open long-lived transactions through this handle — the storage
// layer's WAL contention invariants depend on the WriteTx writer-
// thread discipline (plan §4.1). The handle is matched at the wiring
// site via a structural interface assertion so storage.DB itself does
// not grow a public method.
func (d *concreteDB) SQL() *sql.DB { return d.raw }

// WriteTx runs fn inside a serialisable transaction.
func (d *concreteDB) WriteTx(ctx context.Context, fn func(tx storage.WriteTx) error) error {
	if d.raw == nil {
		return errors.New("storage: closed")
	}
	tx, err := d.raw.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&writeTx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *concreteDB) Migrations() storage.MigrationRegistry {
	return &registryAdapter{reg: d.registry}
}

// VectorStore is stubbed for v1.
func (d *concreteDB) VectorStore() storage.VectorStore { return notImplementedVector{} }

// Backup is stubbed for v1.
func (d *concreteDB) Backup() storage.BackupTaker { return notImplementedBackup{} }

// Diagnostics is stubbed for v1.
func (d *concreteDB) Diagnostics() storage.Diagnostics { return notImplementedDiagnostics{} }

// SetEventSink swaps the active sink under a mutex. v1 has no
// bootstrap buffer wired (no events emitted yet), so the swap is a
// straight assignment.
func (d *concreteDB) SetEventSink(_ context.Context, sink storage.EventSink) error {
	d.sinkMu.Lock()
	defer d.sinkMu.Unlock()
	d.sink = sink
	return nil
}

// Close closes the connection and releases the data-dir lock. Idempotent.
func (d *concreteDB) Close(_ context.Context) error {
	var firstErr error
	if d.raw != nil {
		if err := d.raw.Close(); err != nil {
			firstErr = err
		}
		d.raw = nil
	}
	if d.lock != nil {
		if err := d.lock.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
		d.lock = nil
	}
	return firstErr
}
