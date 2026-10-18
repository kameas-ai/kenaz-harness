package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatRunsTrigger identifies migration 0339 — one-shot
// schedules for scheduled_chat_runs (model-scheduled-jobs-01PMSJ01 WP08,
// FR-006).
//
// Two columns are added to the existing scheduled_chat_runs table
// (created by migration 0325, migrations_scheduled_chat_runs.go):
//
//   - trigger_kind TEXT NOT NULL DEFAULT 'cron' — 'cron' (the only kind
//     that existed before this migration) or 'once'. Existing rows
//     backfill to 'cron' by the column default, which is correct: every
//     row that exists before this migration was scheduled the only way
//     that existed, a recurring cron expression.
//   - run_at INTEGER (nullable, unix seconds) — the fire time for a
//     trigger_kind='once' row. NULL for every 'cron' row.
//
// Why this is needed (spec.md §5.7): the schema was cron-only
// (`cron TEXT NOT NULL`), and "run this once at T" — the overwhelmingly
// common case for "schedule a job to run later" — had to be encoded as a
// recurring expression the user then had to remember to disable. A
// trigger_kind='once' row is armed with a one-shot timer
// (core/scheduler/chat_cron_engine.go), fires exactly once, writes
// history, and sets enabled=0 so it does not re-fire on the next boot
// reload.
//
// Numbering: 0339. docs allocate sessions/0338-0340 to
// model-scheduled-jobs-01PMSJ01 (0338 to WP06's blocked_permission_requests
// table — see migrations_blocked_permission_requests.go — 0339 to this
// WP08 migration, 0340 already landed as WP09's provenance columns —
// see migrations_scheduled_chat_runs_provenance.go). Re-verified against
// the live registry before writing this: 0340 is registered
// (core/session/migrations.go), 0338/0339 were reserved but NOT yet
// registered — this migration claims 0339, the lower of the two
// remaining reserved numbers, so it lands BELOW the mission's own
// already-shipped 0340 on any install that has already applied it.
//
// THIS IS SAFE. core/storage/migrations.Registry.Pending() selects by
// SET MEMBERSHIP, not by a high-water mark (registry.go's own doc: "a
// migration is pending iff the ledger has no applied row for its
// version, whatever else is in the ledger") — the exact bug class that
// shipped as v0.63.0 (a max-based selection rule that skipped every
// migration numbered below an already-applied one from a
// later-numbered, differently-ordered mission) was fixed before this
// migration was written, not worked around by it. VerifyLedger's
// per-mission contiguity check (runner.go) also only orders a migration
// against its OWN mission's other migrations, so 0339 landing after 0340
// was already ledgered does not trip "registered migration below its
// mission's high-water mark but absent from the ledger" — 0339 becomes
// pending (unapplied, versionless-of-0340) and Apply() runs it in the
// same ascending-version pass as every other pending migration.
//
// Proven, not merely argued: see
// TestMigration0339_AppliesOverPreviousReleaseSnapshotAlreadyAt0340 in
// core/storage/sqlite/scheduled_chat_trigger_upgrade_test.go, which boots
// a v0.78.1 snapshot (a database that already carries 0340 in its
// ledger) through the production Open path and asserts 0339 applies
// cleanly alongside it.
//
// Idempotent: the Up function checks pragma_table_info before issuing
// each ALTER TABLE, matching the sessions/0330-knobs / sessions/0340
// convention.
//
// Down: follows the column-add convention (sessions/0330-knobs) — no
// DROP COLUMN; operators wanting a rollback restore from a pre-0339
// backup.
const migrationIDScheduledChatRunsTrigger = "sessions/0339-scheduled-chat-runs-trigger-kind"

// migration0339 returns the migration that adds trigger_kind and run_at
// to scheduled_chat_runs.
func migration0339() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDScheduledChatRunsTrigger,
		Version:       339,
		OwningMission: OwningMission,
		UpSource: `ALTER TABLE scheduled_chat_runs ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'cron';
ALTER TABLE scheduled_chat_runs ADD COLUMN run_at INTEGER;`,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			// trigger_kind
			row := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_runs') WHERE name='trigger_kind'")
			var n int
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE scheduled_chat_runs ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'cron'"); err != nil {
					return err
				}
			}

			// run_at
			row = tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_runs') WHERE name='run_at'")
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE scheduled_chat_runs ADD COLUMN run_at INTEGER"); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			// Column-add; no rollback per convention (sessions/0330-knobs).
			return nil
		},
	}
}
