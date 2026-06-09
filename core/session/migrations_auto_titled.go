package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDAutoTitled lands the auto_titled column the session-auto-titling
// mission (session-auto-titling-01KQ8TDS WP01) needs to track whether the
// engine has already generated a title for a session:
//
//   - sessions.auto_titled — INTEGER NOT NULL DEFAULT 0 (boolean flag).
//     0 means "not yet auto-titled"; 1 means the engine has written a title
//     (or a user has manually renamed, locking out further auto-titling).
//     Default 0 ensures existing sessions are treated as not yet titled
//     without any row updates.
//
// Numbering: 0311, the next free version in the sessions block (300-399)
// after 0310 (compaction bookkeeping — see migrations_compaction.go).
//
// Idempotent: the Up function checks pragma_table_info before issuing the
// ALTER TABLE, so re-running on a DB that already has the column is a no-op.
// This guards the rare case where the migration framework's ledger and the
// actual schema drift (e.g. backup restore mid-migration sequence).
//
// Down: follows the column-add migration convention established by 0302 and
// 0310 — we do not DROP COLUMN; operators wanting a clean rollback restore
// from a pre-0311 backup.
const migrationIDAutoTitled = "sessions/0311-auto-titled"

// sqlAutoTitledColumn is the canonical DDL body for migration 0311.
const sqlAutoTitledColumn = `ALTER TABLE sessions ADD COLUMN auto_titled INTEGER NOT NULL DEFAULT 0`

// migration0311 returns the migration that adds the auto_titled column to the
// sessions table. See the doc comment above for the full rationale.
func migration0311() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDAutoTitled,
		Version:       311,
		OwningMission: OwningMission,
		UpSource:      sqlAutoTitledColumn,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			// Check whether the column already exists before issuing the
			// ALTER TABLE, so the migration is safe to re-run (idempotent).
			row := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='auto_titled'")
			var n int
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				// Column already present; nothing to do.
				return nil
			}
			_, err := tx.Exec(ctx, sqlAutoTitledColumn)
			return err
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			// Column-add migrations are left in place on down-migration per
			// the convention established by 0302 and 0310. Operators wanting
			// a clean rollback restore from a pre-0311 backup.
			return nil
		},
	}
}
