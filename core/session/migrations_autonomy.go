package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDAutonomy is the identifier for migration 0316.
//
// This migration adds two nullable columns to each of the projects and
// sessions tables for the autonomy-dial mission (autonomy-dial-01KR3M2A
// WP02). Both columns are nullable on every table; null means "no value
// stored at this layer — inherit from the upstream layer (session →
// project → global → tier-default)":
//
//   - autonomy_level INTEGER     — encoded autonomy.Tier (0..4) or NULL
//   - autonomy_overrides TEXT    — JSON map of knob → value, or NULL
//
// The settings layer is JSON-file backed (core/rpc/views/settings) and
// not represented as a SQL table; the global autonomy profile rides the
// existing settings.json round-trip via two new fields rather than a
// separate schema migration. As a result migration 0316 only touches
// projects + sessions.
//
// Numbering: 0316 — next free version in the sessions block (300-399)
// after 0314 (token-cost-telemetry session_messages columns). 0315 is
// reserved/skipped to preserve the per-mission contiguity that prior
// missions established; the migration framework's contiguity check
// only requires registered migrations are contiguous in the applied
// ledger, not in the version-number space, so the gap is harmless.
const migrationIDAutonomy = "sessions/0316-autonomy-columns"

const sqlAutonomySchema = `
        ALTER TABLE projects ADD COLUMN autonomy_level INTEGER;
        ALTER TABLE projects ADD COLUMN autonomy_overrides TEXT;
        ALTER TABLE sessions ADD COLUMN autonomy_level INTEGER;
        ALTER TABLE sessions ADD COLUMN autonomy_overrides TEXT;
    `

// migration0316 returns the autonomy-columns migration. Down is a no-op:
// SQLite ALTER TABLE DROP COLUMN requires ≥3.35.0 and the columns have
// safe NULL defaults so leaving them behind is safe. The framework's
// ledger guarantees the migration runs at most once per database, so
// re-applying this migration to an already-migrated DB is a no-op (the
// pending list is empty).
func migration0316() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDAutonomy,
		Version:       316,
		OwningMission: OwningMission,
		UpSource:      sqlAutonomySchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"ALTER TABLE projects ADD COLUMN autonomy_level INTEGER",
				"ALTER TABLE projects ADD COLUMN autonomy_overrides TEXT",
				"ALTER TABLE sessions ADD COLUMN autonomy_level INTEGER",
				"ALTER TABLE sessions ADD COLUMN autonomy_overrides TEXT",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// SQLite ALTER TABLE DROP COLUMN not supported on older macOS.
			// Accept no-op; extra NULL columns cause no regressions.
			return nil
		},
	}
}
