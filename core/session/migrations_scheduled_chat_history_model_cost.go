package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatHistoryModelCost identifies migration 0345 —
// the model and cost of each scheduled chat run, on its existing
// scheduled_chat_run_history row (dogfood 2026-10-08 round 2).
//
// THE BUG. A scheduled chat failed four times on the profile's default
// model and then succeeded on another; Workflows → Runs could say only
// "completed" or "failed". Which model a run actually used (the row's
// override, or whatever "active default" resolved to at fire time) and
// what it cost were not recorded anywhere a run list could read: the run
// session is one row among hundreds, and the model resolution happens at
// dispatch, so reading the schedule row afterwards answers the wrong
// question once the default changes.
//
// Columns (additive, defaulted — every pre-0345 row reads as "model
// unknown, cost 0", which the surface renders as nothing):
//
//   - model     the model id the run was dispatched with
//   - cost_usd  the run session's cumulative cost at the terminal event
//
// Additive only, so check-destructive-migration-coverage.sh has nothing
// to cover. Idempotent like migration0344: each ALTER is guarded by a
// pragma_table_info probe so the ledger-rewind repair path re-runs it as
// a no-op.
const migrationIDScheduledChatHistoryModelCost = "sessions/0345-scheduled-chat-history-model-cost"

const sqlScheduledChatHistoryModelCostSchema = `
        ALTER TABLE scheduled_chat_run_history ADD COLUMN model TEXT NOT NULL DEFAULT '';
        ALTER TABLE scheduled_chat_run_history ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
    `

// migration0345 returns the scheduled-chat-history model/cost migration.
// Down is a no-op, the package convention for additive defaulted columns
// (see migration0344).
func migration0345() migrations.Migration {
	cols := []struct{ name, ddl string }{
		{"model", "ALTER TABLE scheduled_chat_run_history ADD COLUMN model TEXT NOT NULL DEFAULT ''"},
		{"cost_usd", "ALTER TABLE scheduled_chat_run_history ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0"},
	}
	return migrations.Migration{
		ID:            migrationIDScheduledChatHistoryModelCost,
		Version:       345,
		OwningMission: OwningMission,
		UpSource:      sqlScheduledChatHistoryModelCostSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				var n int
				if err := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_run_history') WHERE name=?", c.name).Scan(&n); err != nil {
					return err
				}
				if n > 0 {
					continue
				}
				if _, err := tx.Exec(ctx, c.ddl); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(context.Context, migrations.WriteTx) error { return nil },
	}
}
