package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatRunsProvenance identifies migration 0340 —
// provenance and per-schedule tool containment for scheduled_chat_runs
// (model-scheduled-jobs-01PMSJ01 WP09).
//
// Two columns are added to the existing scheduled_chat_runs table
// (created by migration 0325, migrations_scheduled_chat_runs.go):
//
//   - created_by TEXT NOT NULL DEFAULT 'user' — stamped SERVER-SIDE at
//     the create site (core/rpc/views/scheduledchat.API.Create /
//     .CreateAsModel). Never taken from caller input — there is no
//     "created_by" field on CreateInput/UpdateInput precisely so a
//     malicious or careless caller has nothing to set. Existing rows
//     backfill to 'user' by the column default, which is correct: every
//     row that exists before this migration was created through the
//     only entry point that existed, the human-facing Create RPC.
//
//   - tool_allowlist TEXT NOT NULL DEFAULT '' — a JSON array of tool
//     names (e.g. '["kenaz__web_fetch"]'). Empty string means "no
//     allowlist declared". Per owner ruling B-3
//     (docs/escalation-register-2026-08-19.md:1314,
//     "PERMIT ONLY WITHIN A TOOL ALLOWLIST"), a model-created schedule
//     (created_by='model') with an empty/absent allowlist must never
//     execute — see core/policy/cedar/hooks.go's GateScheduledChatExecute
//     and core/policy/cedar/policies/default_scheduled_run_policy.cedar
//     for the fail-closed enforcement this column feeds.
//
// Numbering: 0340. docs/v0.65.0-merge-order.md §4 and
// kitty-specs/model-scheduled-jobs-01PMSJ01/tasks.md both allocate
// sessions/0338-0340 to this mission (0338 to WP06's
// blocked_permission_requests table, 0339 to WP08's trigger_kind/run_at
// columns); WP09 takes the top of that range. 0341 is held for
// approval-node-01PMZC12 UNIT-6. Re-verified against the live registry
// before writing this: the highest registered version at the time this
// migration was authored was 336 (sessions/0336-stream-checkpoints);
// 0337-0339 are reserved by sibling WPs in this mission and by
// chat-turn-integrity-01PMZ606 and were NOT registered in this tree at
// authoring time, so 0340 is taken directly per the allocation table
// rather than by re-counting the high-water mark (re-counting would have
// produced a collision with those reserved-but-unregistered numbers).
//
// Idempotent: the Up function checks pragma_table_info before issuing
// each ALTER TABLE, matching the sessions/0330-knobs convention.
//
// Down: follows the column-add convention (sessions/0330-knobs) — no
// DROP COLUMN; operators wanting a rollback restore from a pre-0340
// backup.
const migrationIDScheduledChatRunsProvenance = "sessions/0340-scheduled-chat-runs-created-by"

// migration0340 returns the migration that adds created_by and
// tool_allowlist to scheduled_chat_runs.
func migration0340() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDScheduledChatRunsProvenance,
		Version:       340,
		OwningMission: OwningMission,
		UpSource: `ALTER TABLE scheduled_chat_runs ADD COLUMN created_by TEXT NOT NULL DEFAULT 'user';
ALTER TABLE scheduled_chat_runs ADD COLUMN tool_allowlist TEXT NOT NULL DEFAULT '';`,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			// created_by
			row := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_runs') WHERE name='created_by'")
			var n int
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE scheduled_chat_runs ADD COLUMN created_by TEXT NOT NULL DEFAULT 'user'"); err != nil {
					return err
				}
			}

			// tool_allowlist
			row = tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_runs') WHERE name='tool_allowlist'")
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE scheduled_chat_runs ADD COLUMN tool_allowlist TEXT NOT NULL DEFAULT ''"); err != nil {
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
