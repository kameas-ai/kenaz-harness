package storage

import (
	"context"
	"errors"
)

// CredentialReference is a stub of the secrets-keychain mission's credential
// reference shape. The real type lives in core/secrets and is consumed via
// the SecretsBackend interface below.
//
// TODO(secrets-keychain mission, FR-001): replace this struct with the
// canonical core/secrets.CredentialReference once that mission lands.
type CredentialReference struct {
	// Keychain is the OS-keychain entry name holding the encryption key.
	Keychain string
	// Inline is reserved for tests only; production must always use Keychain.
	Inline []byte
}

// SecretsBackend is the minimal interface storage consumes from the
// secrets-keychain mission. Storage uses it once at Open time (and again
// during encryption-key rotation) and immediately zeroes the returned bytes.
//
// TODO(secrets-keychain mission): switch to core/secrets.Backend.
type SecretsBackend interface {
	Resolve(ctx context.Context, ref CredentialReference) ([]byte, error)
}

// VectorBackend selects the active vector store backend.
type VectorBackend string

const (
	// VectorBackendSQLiteVec is the default. Embeds vectors in the same
	// libSQL DB file as relational state.
	VectorBackendSQLiteVec VectorBackend = "sqlite-vec"
	// VectorBackendLanceDB is opt-in for >500k vector workloads.
	VectorBackendLanceDB VectorBackend = "lancedb"
	// VectorBackendChromem is the pure-Go fallback for CGo-free environments.
	VectorBackendChromem VectorBackend = "chromem"
)

// EncryptionStatus records the explicit operator choice about
// encryption-at-rest. The "disabled_with_disk_encryption" value is the
// only acceptable disabled option for production installs.
type EncryptionStatus string

const (
	EncryptionStatusEnabled                    EncryptionStatus = "enabled"
	EncryptionStatusDisabled                   EncryptionStatus = "disabled"
	EncryptionStatusDisabledWithDiskEncryption EncryptionStatus = "disabled_with_disk_encryption"
)

// Config carries the parameters required to Open a storage handle.
//
// Defaults (applied by Open if zero-valued):
//   - WAL          = true
//   - ForeignKeys  = true
//   - VectorBackend = VectorBackendSQLiteVec
//   - EventBufferSize = 1024 (BootstrapEventSink ring buffer)
type Config struct {
	// DataDir is the harness data directory. The DB file lives at
	// <DataDir>/data.db with sidecar files alongside.
	DataDir string

	// EncryptionKey references the database encryption key in the OS
	// keychain via the secrets-keychain mission. Zero value means
	// encryption is disabled (operator must also set EncryptionStatus
	// explicitly to record the choice).
	EncryptionKey CredentialReference

	// EncryptionStatus pins the operator's explicit choice. If
	// EncryptionKey is set, this defaults to "enabled". If unset, the
	// operator must record either "disabled" or
	// "disabled_with_disk_encryption" — the absence of an explicit choice
	// causes Open to refuse rather than silently leave data in plaintext.
	EncryptionStatus EncryptionStatus

	// AllowNonLocalMount permits Open to proceed on a non-local mount
	// (NFS, SMB, cloud-sync). Default false. When true, an
	// non_local_mount_overridden event is emitted for the audit trail.
	AllowNonLocalMount bool

	// VectorBackend selects the default vector backend. Defaults to
	// VectorBackendSQLiteVec.
	VectorBackend VectorBackend

	// WAL toggles WAL mode. Defaults to true. Only set false in tests
	// that explicitly need rollback-journal mode.
	WAL *bool

	// ForeignKeys toggles foreign-key enforcement. Defaults to true.
	ForeignKeys *bool

	// EventBufferSize is the BootstrapEventSink ring-buffer capacity.
	// Defaults to 1024 if zero.
	EventBufferSize int

	// SecretsBackend resolves the encryption key. May be nil when
	// encryption is disabled. TODO(secrets-keychain): swap to the real
	// backend once the secrets mission merges.
	SecretsBackend SecretsBackend
}

// applyDefaults returns cfg with zero-valued fields filled in. It does not
// mutate the receiver.
func (cfg Config) applyDefaults() Config {
	out := cfg
	if out.VectorBackend == "" {
		out.VectorBackend = VectorBackendSQLiteVec
	}
	if out.WAL == nil {
		t := true
		out.WAL = &t
	}
	if out.ForeignKeys == nil {
		t := true
		out.ForeignKeys = &t
	}
	if out.EventBufferSize <= 0 {
		out.EventBufferSize = 1024
	}
	if out.EncryptionStatus == "" {
		if out.EncryptionKey.Keychain != "" || len(out.EncryptionKey.Inline) > 0 {
			out.EncryptionStatus = EncryptionStatusEnabled
		}
	}
	return out
}

// validate returns the first violation in cfg.
func (cfg Config) validate() error {
	if cfg.DataDir == "" {
		return errors.New("storage: Config.DataDir required")
	}
	switch cfg.EncryptionStatus {
	case EncryptionStatusEnabled,
		EncryptionStatusDisabled,
		EncryptionStatusDisabledWithDiskEncryption:
		// OK
	case "":
		return errors.New("storage: EncryptionStatus must be set explicitly (enabled | disabled | disabled_with_disk_encryption)")
	default:
		return errors.New("storage: EncryptionStatus has unknown value")
	}
	if cfg.EncryptionStatus == EncryptionStatusEnabled {
		if cfg.EncryptionKey.Keychain == "" && len(cfg.EncryptionKey.Inline) == 0 {
			return ErrEncryptionKeyMissing
		}
	}
	switch cfg.VectorBackend {
	case VectorBackendSQLiteVec,
		VectorBackendLanceDB,
		VectorBackendChromem:
		// OK
	default:
		return errors.New("storage: VectorBackend has unknown value")
	}
	return nil
}

// Reader is the read-only handle. Implemented by storage's read pool.
type Reader interface {
	// QueryRow runs an indexed point query. Sub-millisecond reads are the
	// expected case (NFR-002).
	QueryRow(ctx context.Context, query string, args ...any) Row
	// Query streams rows.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Row is the one-row result returned by Reader.QueryRow.
type Row interface {
	Scan(dest ...any) error
}

// Rows is a streaming row iterator.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// WriteTx is the write transaction surface. All writes funnel through a
// single goroutine + queue (plan §4.1) so consumers do not contend for
// locks. Within fn the transaction is open; returning a non-nil error
// rolls it back.
type WriteTx interface {
	// Exec runs a write statement.
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	// QueryRow inside the transaction.
	QueryRow(ctx context.Context, query string, args ...any) Row
	// Query inside the transaction.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Result is the bare result from Exec.
type Result interface {
	RowsAffected() (int64, error)
	LastInsertID() (int64, error)
}

// MigrationRegistry collects migrations from every consuming mission.
// Missions register at process boot, before Open completes the schema
// check. The concrete type lives in core/storage/migrations.
type MigrationRegistry interface {
	Register(m Migration) error
	// All returns every registered migration, sorted by Version ascending.
	// Used by the drift detector to compare the registry against the ledger.
	All() []Migration
	Applied() ([]LedgerEntry, error)
	Pending() ([]Migration, error)
	Apply(ctx context.Context) error
	Rollback(ctx context.Context, toVersion int) error
}

// Migration is a versioned, ordered, idempotent unit of schema change.
//
// ContentHash is the SHA-256 (hex) of the migration's canonicalized Up
// source — comments stripped, whitespace collapsed, line endings
// normalized. The runner verifies this hash at every startup; mismatch
// triggers ErrLedgerHashMismatch.
type Migration struct {
	ID            string
	Version       int
	OwningMission string
	Up            func(ctx context.Context, tx WriteTx) error
	Down          func(ctx context.Context, tx WriteTx) error
	// UpSource is the canonical SQL-or-Go-string source the runner hashes.
	// Required when ContentHash is empty.
	UpSource string
	// ContentHash is the hex SHA-256 of the canonicalized UpSource. If
	// empty at registration time the registry computes it from UpSource.
	ContentHash string
}

// LedgerEntry is one row in the harness_migrations ledger table.
type LedgerEntry struct {
	Version               int
	ID                    string
	AppliedAt             string // RFC3339 UTC
	ContentHash           string
	OwningMission         string
	Action                LedgerAction
	RolledBackFromVersion *int
}

// LedgerAction enumerates the kinds of events recorded in the ledger.
// The ledger is append-only; rollbacks add new "rolled_back" rows rather
// than deleting the original "applied" row.
type LedgerAction string

const (
	LedgerActionApplied    LedgerAction = "applied"
	LedgerActionRolledBack LedgerAction = "rolled_back"
)

// VectorStore is the harness's logical vector-store handle. Backends live
// under core/storage/vector/<backend>/ and are selected via Config.
type VectorStore interface {
	OpenCollection(ctx context.Context, spec CollectionSpec) (Collection, error)
	Backends() []VectorBackend
}

// Collection is a logical bag of embeddings keyed by string ID.
type Collection interface {
	Insert(ctx context.Context, items []Embedding) error
	Search(ctx context.Context, q []float32, k int, filter Filter) ([]Match, error)
	Delete(ctx context.Context, ids []string) error
	Reindex(ctx context.Context) error
	Stats(ctx context.Context) (CollectionStats, error)
	Close() error
}

// CollectionSpec describes a collection's shape.
type CollectionSpec struct {
	Name         string
	Dimension    int
	Metric       VectorMetric
	Quantization VectorQuantization
	Backend      VectorBackend // optional override; default = configured default
}

// VectorMetric enumerates supported similarity metrics.
type VectorMetric string

const (
	VectorMetricCosine VectorMetric = "cosine"
	VectorMetricL2     VectorMetric = "l2"
	VectorMetricDot    VectorMetric = "dot"
)

// VectorQuantization enumerates supported quantization modes.
type VectorQuantization string

const (
	VectorQuantizationNone       VectorQuantization = "none"
	VectorQuantizationBinaryInt8 VectorQuantization = "binary_int8"
)

// Embedding is one stored vector with metadata.
type Embedding struct {
	ID       string
	Vector   []float32
	Metadata map[string]any
}

// Filter restricts search by metadata. Empty matches all.
type Filter map[string]any

// Match is one nearest neighbor.
type Match struct {
	ID       string
	Score    float32
	Metadata map[string]any
}

// CollectionStats describes a collection's runtime state.
type CollectionStats struct {
	Count   int64
	Backend VectorBackend
}

// BackupTaker takes a consistent online backup while writes proceed.
type BackupTaker interface {
	Take(ctx context.Context, dst string) (BackupArtifact, error)
}

// RestoreApplier applies a backup file to a fresh data dir.
type RestoreApplier interface {
	Apply(ctx context.Context, src string) error
}

// BackupArtifact is the audit row recorded when a backup completes.
type BackupArtifact struct {
	Path                string
	TakenAt             string // RFC3339 UTC
	SourceSchemaVersion int
	ContentHash         string
	EncryptionStatus    EncryptionStatus
}

// Diagnostics exposes integrity-check and status reports.
type Diagnostics interface {
	Verify(ctx context.Context) (VerifyReport, error)
	Status(ctx context.Context) (StatusReport, error)
}

// VerifyReport is the result of PRAGMA integrity_check.
type VerifyReport struct {
	OK       bool
	Messages []string
}

// StatusReport is what `harness db status` returns.
type StatusReport struct {
	InstallID         string
	LibSQLVersion     string
	SQLiteVersion     string
	SchemaVersion     int
	EncryptionStatus  EncryptionStatus
	WALMode           bool
	ForeignKeysOn     bool
	FileSizeBytes     int64
	PageCount         int64
	AppliedMigrations []LedgerEntry
}

// EventSink is the outbound interface storage owns to avoid an import
// cycle with core/event. During boot, storage emits to a buffered
// implementation; once event-log's migrations apply, the runtime calls
// DB.SetEventSink to swap in the real sink.
type EventSink interface {
	Emit(ctx context.Context, kind string, payload map[string]any) error
}

// DB is the harness's app-database handle. One per process.
type DB interface {
	Reader() Reader
	WriteTx(ctx context.Context, fn func(tx WriteTx) error) error
	Migrations() MigrationRegistry
	VectorStore() VectorStore
	Backup() BackupTaker
	Diagnostics() Diagnostics
	// SetEventSink swaps the active EventSink under a mutex and drains
	// the bootstrap buffer through the new sink in insertion order.
	SetEventSink(ctx context.Context, sink EventSink) error
	Close(ctx context.Context) error
}

// Sentinel errors. Stable typed errors per plan §3 error taxonomy.
//
// Wrap with %w to preserve identity through fmt.Errorf.
var (
	// ErrDBLocked is returned when another harness process holds the
	// data-dir lock. The lock holder's PID is wrapped as
	// LockHeldError when available.
	ErrDBLocked = errors.New("storage: database is already opened by another harness process")

	// ErrNonLocalMount is returned when the data directory is on a
	// network filesystem or cloud-sync root and the operator did not
	// set Config.AllowNonLocalMount.
	ErrNonLocalMount = errors.New("storage: refusing to open database on non-local mount")

	// ErrEncryptionKeyMissing is returned when EncryptionStatus is
	// "enabled" but no key reference was supplied.
	ErrEncryptionKeyMissing = errors.New("storage: encryption enabled but no key reference supplied")

	// ErrEncryptionKeyMismatch is returned when the supplied key fails
	// to decrypt the database.
	ErrEncryptionKeyMismatch = errors.New("storage: encryption key does not match database")

	// ErrSchemaGap is returned when the migration ledger has missing
	// versions in [min, max] or references a migration that no longer
	// exists in the registry.
	ErrSchemaGap = errors.New("storage: migration ledger has gaps or unknown entries")

	// ErrMigrationFailed wraps a failure during apply or rollback. The
	// underlying error is preserved with errors.Is/As.
	ErrMigrationFailed = errors.New("storage: migration failed")

	// ErrLedgerHashMismatch is returned when a registered migration's
	// canonical content hash does not match the ledger row.
	ErrLedgerHashMismatch = errors.New("storage: migration content hash differs from ledger")

	// ErrIntegrityCheckFailed is returned when PRAGMA integrity_check
	// returns anything other than "ok".
	ErrIntegrityCheckFailed = errors.New("storage: integrity check failed")

	// ErrBackupInProgress is returned when a second concurrent backup
	// attempt is made.
	ErrBackupInProgress = errors.New("storage: backup already in progress")

	// ErrRestoreLockHeld is returned when Apply is called against a
	// data dir whose harness lock is currently held.
	ErrRestoreLockHeld = errors.New("storage: cannot restore while harness is running on destination")

	// ErrSQLiteVersionTooOld is returned when the embedded SQLite is
	// below the 3.51.0 floor (research D5).
	ErrSQLiteVersionTooOld = errors.New("storage: SQLite version below 3.51.0 floor")

	// ErrVersionOutOfBlock is returned when a Migration's version falls
	// outside its owning mission's reserved version block.
	ErrVersionOutOfBlock = errors.New("storage: migration version outside owning mission's reserved block")

	// ErrVersionCollision is returned when two migrations share a Version.
	ErrVersionCollision = errors.New("storage: two migrations registered at the same version")

	// ErrMigrationIDCollision is returned when two migrations share an ID.
	ErrMigrationIDCollision = errors.New("storage: two migrations registered with the same ID")

	// ErrUnknownOwningMission is returned when a migration's
	// OwningMission has no entry in the version-block table.
	ErrUnknownOwningMission = errors.New("storage: migration owning mission has no reserved version block")

	// ErrNotImplemented is returned by stubbed surfaces during early WPs.
	ErrNotImplemented = errors.New("storage: not implemented in this build")
)

// LockHeldError carries the holder's PID and start-time when storage can
// determine them. Wrapped by ErrDBLocked.
type LockHeldError struct {
	HolderPID int
	HolderAt  string // RFC3339 UTC; empty when unknown
}

func (e *LockHeldError) Error() string {
	if e.HolderAt != "" {
		return "storage: data dir locked by pid " + itoa(e.HolderPID) + " since " + e.HolderAt
	}
	return "storage: data dir locked by pid " + itoa(e.HolderPID)
}

// MountKindError carries the matched filesystem or cloud-sync provider
// detected on the data dir. Wrapped by ErrNonLocalMount.
type MountKindError struct {
	Kind   string // "nfs", "smb", "cifs", "cloud:icloud", "cloud:dropbox", ...
	Detail string
	Path   string
}

func (e *MountKindError) Error() string {
	return "storage: non-local mount detected (" + e.Kind + "): " + e.Path + " — " + e.Detail
}

// SQLiteVersionError carries the detected SQLite version when it is below
// the 3.51 floor. Wrapped by ErrSQLiteVersionTooOld.
type SQLiteVersionError struct {
	Detected string
	Required string
}

func (e *SQLiteVersionError) Error() string {
	return "storage: SQLite version " + e.Detected + " below floor " + e.Required
}

// itoa is a tiny zero-alloc int-to-string for sentinel error messages so
// this file does not need to import strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
