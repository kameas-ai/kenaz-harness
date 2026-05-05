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

	corestorage "github.com/sigil-tech/kaneaz-harness/core/storage"
	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
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
func (a *API) GetMigrationDriftReport(_ context.Context) (DriftReport, error) {
	if a.db == nil {
		return DriftReport{}, ErrStorageUnavailable
	}
	reg := a.db.Migrations()

	// Read all ledger entries.
	entries, err := reg.Applied()
	if err != nil {
		return DriftReport{}, fmt.Errorf("storage: drift: read ledger: %w", err)
	}

	// Build the effective applied set: last write per version wins.
	type ledgerSummary struct {
		id     string
		action corestorage.LedgerAction
	}
	ledger := map[int]ledgerSummary{}
	for _, e := range entries {
		ledger[e.Version] = ledgerSummary{id: e.ID, action: e.Action}
	}

	// Read registered migrations.
	registered := map[int]corestorage.Migration{}
	for _, m := range reg.All() {
		registered[m.Version] = m
	}

	// Build the union of all known versions.
	seen := map[int]struct{}{}
	for v := range ledger {
		seen[v] = struct{}{}
	}
	for v := range registered {
		seen[v] = struct{}{}
	}

	var drifts []DriftEntry

	for v := range seen {
		led, inLedger := ledger[v]
		reg, inRegistry := registered[v]

		switch {
		case inLedger && inRegistry:
			if led.action != corestorage.LedgerActionApplied {
				continue
			}
			if led.id != reg.ID {
				drifts = append(drifts, DriftEntry{
					Version:    v,
					LedgerID:   led.id,
					ExpectedID: reg.ID,
					Kind:       "id_mismatch",
					Severity:   "error",
					Suggestion: fmt.Sprintf(
						"Ledger row v%d has id=%q but the registered migration expects %q. "+
							"Use 'Apply automatic fix' to rename the ledger row (a backup is created first).",
						v, led.id, reg.ID,
					),
				})
			}

		case inLedger && !inRegistry:
			// Rolled-back rows with no matching code are safe to skip.
			if led.action == corestorage.LedgerActionRolledBack {
				continue
			}
			drifts = append(drifts, DriftEntry{
				Version:    v,
				LedgerID:   led.id,
				ExpectedID: "",
				Kind:       "ledger_only",
				Severity:   "warning",
				Suggestion: fmt.Sprintf(
					"Ledger has an applied entry for v%d (id=%q) but no matching migration is registered. "+
						"This usually means a rolled-back migration whose code was removed. "+
						"Inspect the ledger and remove the row manually if this is expected.",
					v, led.id,
				),
			})

		case !inLedger && inRegistry:
			drifts = append(drifts, DriftEntry{
				Version:    v,
				LedgerID:   "",
				ExpectedID: reg.ID,
				Kind:       "code_only",
				Severity:   "info",
				Suggestion: fmt.Sprintf(
					"Migration v%d (id=%q) is registered but not yet applied. "+
						"It will be applied automatically on the next chassis boot.",
					v, reg.ID,
				),
			})
		}
	}

	// Sort ascending by version.
	sortDrifts(drifts)

	return DriftReport{Drifts: drifts}, nil
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

// sortDrifts sorts the slice in-place by Version ascending.
func sortDrifts(d []DriftEntry) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Version < d[j-1].Version; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
