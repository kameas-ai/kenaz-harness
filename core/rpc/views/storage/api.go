// Package storage exposes the storage-health view-scoped RPC surface.
//
// The primary feature in v0.5.1 is the migration drift detector: it
// compares the live harness_migrations ledger against the registered
// migration set and surfaces every discrepancy so the user can inspect
// and optionally repair them from Settings → Health.
package storage

import "context"

// DriftReport is the top-level drift-detection result returned to the
// frontend. It mirrors migrations.DriftReport but lives in the RPC view
// package so Wails can reflect it into TypeScript without importing the
// internal migrations package.
type DriftReport struct {
	// Drifts is the ordered list of discrepancies, sorted by Version
	// ascending. Empty when no drift is detected.
	Drifts []DriftEntry `json:"drifts"`
}

// DriftEntry describes one discrepancy between the ledger and the
// registered migration set.
type DriftEntry struct {
	// Version is the migration version this entry concerns.
	Version int `json:"version"`

	// LedgerID is the ID currently stored in the ledger for this version.
	// Empty for code_only entries.
	LedgerID string `json:"ledgerId"`

	// ExpectedID is the ID the registered migration declares.
	// Empty for ledger_only entries.
	ExpectedID string `json:"expectedId"`

	// Kind classifies the discrepancy:
	//   "id_mismatch"  — both ledger and registry present; IDs differ.
	//   "ledger_only"  — applied ledger row with no registered migration.
	//   "code_only"    — registered migration not yet applied.
	Kind string `json:"kind"`

	// Severity is one of "error", "warning", or "info".
	Severity string `json:"severity"`

	// Suggestion is a human-readable recommended action.
	Suggestion string `json:"suggestion"`
}

// StorageAPI is the view-scoped accessor for storage-health operations.
type StorageAPI interface {
	// GetMigrationDriftReport reads the ledger and registry, compares
	// them, and returns every discrepancy. Never modifies the database.
	GetMigrationDriftReport(ctx context.Context) (DriftReport, error)

	// ApplyDriftFix attempts to repair the drift at the given version.
	// For "id_mismatch" entries it backs up data.db, then UPDATEs the
	// ledger row's id to match the registered migration's ID.
	//
	// For "ledger_only" and "code_only" entries it returns
	// ErrFixNotAutomatable — these require user judgment.
	ApplyDriftFix(ctx context.Context, version int) error
}
