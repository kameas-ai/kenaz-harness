package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	corestorage "github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// ErrFixNotAutomatable mirrors migrations.ErrFixNotAutomatable at the view
// layer so callers outside the migrations package can identify the sentinel.
var ErrFixNotAutomatable = migrations.ErrFixNotAutomatable

// ErrStorageUnavailable is returned when the API was constructed without a
// live storage.DB (nil path — test harness or incomplete boot).
var ErrStorageUnavailable = errors.New("storage view: storage is not available")

// API is the concrete StorageAPI implementation.
//
// It is constructed in core/rpc/api.go when a real Core (storage.DB) is
// available. When constructed with nil values, every method returns
// ErrStorageUnavailable so the settings panel can display a graceful message.
type API struct {
	db      corestorage.DB
	dataDir string // path to the data directory; data.db lives at <dataDir>/data.db
}

// NewAPI constructs a StorageAPI backed by the given storage.DB. If db or
// dataDir are zero, every call returns ErrStorageUnavailable.
func NewAPI(db corestorage.DB, dataDir string) *API {
	return &API{db: db, dataDir: dataDir}
}

// GetMigrationDriftReport reads the ledger and registered migrations, compares
// them, and returns every discrepancy.
//
// This delegates classification to migrations.DetectDrift instead of
// carrying a second copy of the Kind/Severity/Suggestion switch. Before
// upgrade-path-coverage-01PMUG01 WP04 this method WAS that second copy —
// the live path, reached via Storage_GetMigrationDriftReport, while
// migrations.DetectDrift (core/storage/migrations/doctor.go) had zero
// non-test callers but carried the only test coverage (doctor_test.go).
// Now there is exactly one implementation: a change to its severity table
// changes what both the RPC surface and doctor_test.go see.
//
// driftSourceAdapter bridges the type gap: a.db.Migrations() returns
// storage.MigrationRegistry (storage.Migration / storage.LedgerEntry),
// while DetectDrift wants migrations.Migration / migrations.LedgerEntry —
// a parallel type family that core/storage/sqlite/adapters.go translates
// the other direction for the sqlite package. There is no exported path
// from storage.MigrationRegistry back to the concrete *migrations.Registry
// (the sqlite adapter's underlying registry field is unexported), so this
// method re-does that translation at the RPC-view boundary instead.
func (a *API) GetMigrationDriftReport(_ context.Context) (DriftReport, error) {
	if a.db == nil {
		return DriftReport{}, ErrStorageUnavailable
	}
	report, err := migrations.DetectDrift(driftSourceAdapter{reg: a.db.Migrations()})
	if err != nil {
		return DriftReport{}, fmt.Errorf("storage: drift: %w", err)
	}
	return fromMigrationsDriftReport(report), nil
}

// driftSourceAdapter satisfies migrations.DriftSource by translating
// storage.MigrationRegistry's wire-family types into the migrations
// package's parallel types.
type driftSourceAdapter struct {
	reg corestorage.MigrationRegistry
}

func (d driftSourceAdapter) Applied() ([]migrations.LedgerEntry, error) {
	rows, err := d.reg.Applied()
	if err != nil {
		return nil, err
	}
	out := make([]migrations.LedgerEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, migrations.LedgerEntry{
			Version:               e.Version,
			ID:                    e.ID,
			AppliedAt:             e.AppliedAt,
			ContentHash:           e.ContentHash,
			OwningMission:         e.OwningMission,
			Action:                migrations.LedgerAction(e.Action),
			RolledBackFromVersion: e.RolledBackFromVersion,
		})
	}
	return out, nil
}

func (d driftSourceAdapter) All() []migrations.Migration {
	migs := d.reg.All()
	out := make([]migrations.Migration, 0, len(migs))
	for _, m := range migs {
		out = append(out, migrations.Migration{
			ID:            m.ID,
			Version:       m.Version,
			OwningMission: m.OwningMission,
			UpSource:      m.UpSource,
			ContentHash:   m.ContentHash,
		})
	}
	return out
}

// fromMigrationsDriftReport translates migrations.DriftReport into the
// RPC view's own JSON-tagged wire type (api.go's DriftReport/DriftEntry).
// The wire types are unchanged by this WP — frontend/wailsjs consumes
// them — only the classification that populates them moved.
func fromMigrationsDriftReport(r migrations.DriftReport) DriftReport {
	out := DriftReport{Drifts: make([]DriftEntry, 0, len(r.Drifts))}
	for _, d := range r.Drifts {
		out.Drifts = append(out.Drifts, DriftEntry{
			Version:    d.Version,
			LedgerID:   d.LedgerID,
			ExpectedID: d.ExpectedID,
			Kind:       d.Kind,
			Severity:   d.Severity,
			Suggestion: d.Suggestion,
		})
	}
	return out
}

// ApplyDriftFix repairs an id_mismatch drift entry for the given version.
// It backs up data.db, then UPDATEs the ledger row's id to the expected ID.
//
// Returns ErrFixNotAutomatable for ledger_only and code_only entries.
// Returns ErrStorageUnavailable when the API was constructed without storage.
func (a *API) ApplyDriftFix(ctx context.Context, version int) error {
	if a.db == nil {
		return ErrStorageUnavailable
	}

	// Fetch the current drift report to find this entry.
	report, err := a.GetMigrationDriftReport(ctx)
	if err != nil {
		return fmt.Errorf("storage: drift fix: fetch report: %w", err)
	}

	var entry *DriftEntry
	for i := range report.Drifts {
		if report.Drifts[i].Version == version {
			entry = &report.Drifts[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("storage: drift fix: no drift entry found for version %d", version)
	}
	if entry.Kind != "id_mismatch" {
		return fmt.Errorf("%w: version %d has kind=%q; only id_mismatch can be fixed automatically",
			ErrFixNotAutomatable, version, entry.Kind)
	}

	// Step 1: backup data.db before any writes.
	if err := a.backupDB(); err != nil {
		return fmt.Errorf("storage: drift fix: backup: %w", err)
	}

	// Step 2: UPDATE the ledger row.
	// We UPDATE only the most-recent applied row for this version. Using
	// a deterministic WHERE clause (version + id + action=applied) ensures
	// we don't accidentally rename a rolled-back row.
	err = a.db.WriteTx(ctx, func(tx corestorage.WriteTx) error {
		res, execErr := tx.Exec(ctx,
			`UPDATE harness_migrations
             SET id = ?
             WHERE version = ? AND id = ? AND action = 'applied'`,
			entry.ExpectedID, version, entry.LedgerID,
		)
		if execErr != nil {
			return execErr
		}
		n, rowErr := res.RowsAffected()
		if rowErr != nil {
			return rowErr
		}
		if n == 0 {
			return fmt.Errorf("no rows updated for version=%d id=%q", version, entry.LedgerID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("storage: drift fix: update ledger: %w", err)
	}
	return nil
}

// backupDB copies <dataDir>/data.db to
// <dataDir>/data.db.backup-pre-doctor-<unix-ts>.
func (a *API) backupDB() error {
	if a.dataDir == "" {
		return errors.New("storage: drift fix: dataDir not set")
	}
	src := filepath.Join(a.dataDir, "data.db")
	dst := filepath.Join(a.dataDir,
		"data.db.backup-pre-doctor-"+strconv.FormatInt(time.Now().Unix(), 10),
	)

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return out.Sync()
}
