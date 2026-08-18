// Package migrations — doctor.go
//
// DetectDrift compares the live harness_migrations ledger against the
// registered migration set and surfaces every discrepancy as a DriftEntry.
// It does NOT modify the ledger; the storage view layer exposes ApplyDriftFix
// for the single automatable repair (id_mismatch).
//
// May 2026 incident (PR #71): a migration was renumbered between dev builds
// (version 322 changed ID from "branch-display-meta" to "last_usage_json").
// Users who had already applied the old build received no signal — the DB
// silently continued carrying the stale ledger row, causing subtle failures
// only visible when the runner's hash-check later caught it. This detector
// surfaces the mismatch at boot so the operator can act before the engine
// refuses to start.
package migrations

import (
	"errors"
	"fmt"
)

// ErrFixNotAutomatable is returned by callers (e.g. the storage RPC view)
// when the user attempts to fix a drift kind that requires human judgment
// (ledger_only, code_only). The caller surfaces this to the user as a
// suggestion rather than an error path.
var ErrFixNotAutomatable = errors.New("migrations: fix cannot be applied automatically — manual intervention required")

// DriftReport is the top-level result of DetectDrift.
type DriftReport struct {
	// Drifts is the ordered list of detected discrepancies, sorted by
	// Version ascending so the UI can display them in a predictable order.
	Drifts []DriftEntry
}

// DriftEntry describes a single discrepancy between the live ledger and the
// registered migration set.
type DriftEntry struct {
	// Version is the migration version number this entry concerns.
	Version int

	// LedgerID is the id currently stored in the harness_migrations ledger
	// row for this version. Empty for code_only entries (no ledger row).
	LedgerID string

	// ExpectedID is the ID the registered migration declares. Empty for
	// ledger_only entries (no registered migration).
	ExpectedID string

	// Kind classifies the discrepancy:
	//   "id_mismatch"  — both ledger and registry have this version, but the
	//                    IDs differ. Automatable fix: UPDATE ledger row.
	//   "ledger_only"  — the ledger has an applied row for this version, but
	//                    the registry has no registered migration. Typically a
	//                    rolled-back migration whose code was removed.
	//   "code_only"    — the registry has a migration at this version, but
	//                    the ledger has no applied row. The normal pending
	//                    state ahead of the next boot. NOT "the runner will
	//                    apply it on next boot": since v0.63.1 Open runs
	//                    verifyFullyApplied and REFUSES TO START if a
	//                    registered migration still has no applied row after
	//                    Apply. The Suggestion field below says so; this
	//                    comment used to contradict it (fixed 2026-08-18).
	Kind string

	// Severity:
	//   "error"   — id_mismatch (the ledger row's ID does not match the
	//               registered migration; the DB state may be inconsistent).
	//   "warning"  — ledger_only (applied row with no registered migration).
	//   "info"    — code_only (pending migration; expected pre-apply state).
	Severity string

	// Suggestion is a human-readable one-liner describing the recommended
	// action. Displayed in the Settings → Health panel.
	Suggestion string
}

// DriftSource is the minimal registry surface DetectDrift needs: the
// applied-ledger read and the registered-migration set. *Registry
// satisfies this directly.
//
// It exists so a second caller can feed DetectDrift a registry it doesn't
// own without going through *Registry's boot-time construction path. The
// RPC storage view (core/rpc/views/storage) is that caller: it only has
// access to storage.MigrationRegistry (a parallel interface with parallel
// types, translated at the sqlite adapter boundary — see
// core/storage/sqlite/adapters.go), so it adapts that into a DriftSource
// wrapping storage.Migration/storage.LedgerEntry values converted to
// migrations.Migration/migrations.LedgerEntry, rather than constructing a
// *Registry. This keeps drift classification in exactly one place
// (upgrade-path-coverage-01PMUG01 WP04, FR-3f): before this change,
// core/rpc/views/storage/impl.go carried an independent, untested
// reimplementation of the switch below, and a severity/suggestion change
// here changed nothing a user saw.
type DriftSource interface {
	Applied() ([]LedgerEntry, error)
	All() []Migration
}

// DetectDrift reads the current ledger via registry.Applied() and the
// registered migrations via registry.All(), then returns every discrepancy.
// It never modifies the database.
//
// Rolled-back versions whose most recent ledger row is action=rolled_back are
// excluded from the id_mismatch check (they rolled back cleanly). A
// rolled-back version with no corresponding registered migration is also
// excluded from ledger_only to avoid noise.
func DetectDrift(registry DriftSource) (DriftReport, error) {
	// Read all ledger entries (returns full history; insertion-order).
	entries, err := registry.Applied()
	if err != nil {
		return DriftReport{}, fmt.Errorf("doctor: read ledger: %w", err)
	}

	// Build the "effective applied" set: per version, the most recent action
	// wins (insertion-order, last write wins). Same semantics as VerifyLedger.
	type ledgerSummary struct {
		id     string
		action LedgerAction
	}
	ledger := map[int]ledgerSummary{}
	for _, e := range entries {
		ledger[e.Version] = ledgerSummary{id: e.ID, action: e.Action}
	}

	// Read registered migrations.
	registered := map[int]Migration{}
	for _, m := range registry.All() {
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
			// Only flag applied rows; a rolled-back row with a matching
			// registry entry is clean.
			if led.action != LedgerActionApplied {
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
			// Rolled-back rows with no matching code are common and safe —
			// the mission deleted the migration after rolling back. Skip them.
			if led.action == LedgerActionRolledBack {
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
			// code_only is the normal pre-apply state; info severity so the
			// panel can suppress it by default.
			drifts = append(drifts, DriftEntry{
				Version:    v,
				LedgerID:   "",
				ExpectedID: reg.ID,
				Kind:       "code_only",
				Severity:   "info",
				Suggestion: fmt.Sprintf(
					"Migration v%d (id=%q) is registered but not yet applied. "+
						"This is the normal pending state ahead of the next boot: since "+
						"v0.63.1, Open refuses to start rather than run against a schema "+
						"a registered migration was never applied to.",
					v, reg.ID,
				),
			})
		}
	}

	// Sort ascending by version for stable UI output.
	sortDrifts(drifts)

	return DriftReport{Drifts: drifts}, nil
}

// sortDrifts sorts the slice in-place by Version ascending (insertion sort;
// N is small — number of known migration versions).
func sortDrifts(d []DriftEntry) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Version < d[j-1].Version; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
