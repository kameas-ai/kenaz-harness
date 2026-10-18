package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDBlockedPermissionRequests identifies migration 0338 — the
// durable, surfaceable, re-runnable record of a denied permission request
// (model-scheduled-jobs-01PMSJ01 WP06, FR-004, owner decision 2).
//
// WHY THIS TABLE (spec.md §5.5). Four candidate homes were considered and
// rejected before this one:
//
//   - scheduled_chat_run_history: outcome-shaped (status/output_snippet/
//     error), and its FK is ON DELETE CASCADE — deleting the job would
//     destroy the pending-grant record with it (AC-007 forbids this).
//   - The Cedar decision store: each gate builds its own engine with a
//     private, unread MemoryDecisionStore (docs/unwired-ledger.md) —
//     writing there is writing to /dev/null with extra steps.
//   - core/context/audit alone: necessary (the denial IS audited) but
//     not sufficient — an audit event is an append-only fact, not a work
//     item with a pending -> granted -> dismissed lifecycle a surfacing
//     UI can query cheaply.
//
// DELIBERATELY NO FK to scheduled_chat_runs. Deleting a job must not
// delete the record of what it was blocked from doing — this is the
// sessions/0327-... lesson (docs/unwired-ledger.md): an FK with
// ON DELETE CASCADE on a "this outlives the row that caused it" table is
// a silent-data-loss trap. origin_id is a plain TEXT column, not a
// foreign key.
//
// origin is a column, not a filter: the interactive fs-write path
// produces these rows too (C-2), recorded from day one — if only
// scheduled runs wrote rows, the WP07 surfacing UI would read empty for
// every user who never scheduled a job, and a real defect there would go
// unnoticed.
//
// Numbering: 0338, the lower of the two numbers
// (sessions/0338-0340) this mission's docs reserved and WP09's
// migration0340 explicitly left unregistered ("0338/0339 are reserved
// for this mission's WP06/WP08 and were not registered in this tree at
// authoring time"). See migrations_scheduled_chat_runs_trigger.go (0339)
// for the full argument that registering a number below an
// already-applied 0340 is safe on this tree's migration runner — the
// same argument applies here; core/storage/migrations.Registry.Pending()
// selects pending migrations by set membership, not by a high-water
// mark, and VerifyLedger's contiguity check is scoped per owning
// mission, not global.
//
// Proven, not merely argued: see
// TestMigration0338_AppliesOverPreviousReleaseSnapshotAlreadyAt0340 in
// core/storage/sqlite/blocked_permission_requests_upgrade_test.go.
const migrationIDBlockedPermissionRequests = "sessions/0338-blocked-permission-requests"

const sqlBlockedPermissionRequestsSchema = `
        CREATE TABLE IF NOT EXISTS blocked_permission_requests (
            id           TEXT PRIMARY KEY,
            origin       TEXT NOT NULL,
            origin_id    TEXT NOT NULL DEFAULT '',
            session_id   TEXT NOT NULL DEFAULT '',
            family       TEXT NOT NULL,
            action       TEXT NOT NULL,
            resource     TEXT NOT NULL,
            reason       TEXT NOT NULL DEFAULT '',
            status       TEXT NOT NULL DEFAULT 'pending',
            created_at   INTEGER NOT NULL,
            resolved_at  INTEGER
        );

        CREATE INDEX IF NOT EXISTS idx_blocked_permission_requests_status
            ON blocked_permission_requests (status, created_at DESC);

        CREATE INDEX IF NOT EXISTS idx_blocked_permission_requests_origin
            ON blocked_permission_requests (origin, origin_id);
    `

// migration0338 returns the migration that lands the
// blocked_permission_requests table.
func migration0338() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDBlockedPermissionRequests,
		Version:       338,
		OwningMission: OwningMission,
		UpSource:      sqlBlockedPermissionRequestsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlBlockedPermissionRequestsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_blocked_permission_requests_origin",
				"DROP INDEX IF EXISTS idx_blocked_permission_requests_status",
				"DROP TABLE IF EXISTS blocked_permission_requests",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
