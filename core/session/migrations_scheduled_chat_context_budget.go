package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatContextBudget identifies migration 0349 — the
// per-schedule context policy of a scheduled chat's ONE conversation
// (sessions/0348), and the two history facts that policy needs.
//
// Since 0348 every fire appends to the same session, so every fire
// replays the whole conversation. The schedule now carries a budget: the
// dispatcher (core/rpc/chat_run_dispatcher.go) keeps the newest runs
// verbatim and folds older ones into one rolling summary before a fire
// whose conversation is over it (scheduled-chat-context-budget).
//
// scheduled_chat_runs (additive, defaulted — every existing schedule gets
// the owner-approved defaults):
//
//   - context_budget_tokens INTEGER  the conversation size, in tokens,
//     above which a fire compacts first. Default 20000.
//   - keep_recent_runs      INTEGER  runs kept verbatim. Default 2.
//   - memory_focus          TEXT     what the summary must remember,
//     passed to the summariser. Default ''.
//
// scheduled_chat_run_history (additive):
//
//   - prompt_message_id     TEXT     the session row this run's prompt
//     was appended as — the run boundary the budget counts runs by.
//     NULL on rows from before 0349 and on fires that never appended.
//   - compaction_cost_usd   REAL     what summarising older runs before
//     this fire cost (also included in cost_usd). NULL when the fire did
//     not compact.
//
// Additive only, so check-destructive-migration-coverage.sh has nothing
// to cover. Idempotent like migration0345: each ALTER is guarded by a
// pragma_table_info probe. Down is a no-op, the package convention for
// additive columns.
const migrationIDScheduledChatContextBudget = "sessions/0349-scheduled-chat-context-budget"

const sqlScheduledChatContextBudgetSchema = `
        ALTER TABLE scheduled_chat_runs ADD COLUMN context_budget_tokens INTEGER NOT NULL DEFAULT 20000;
        ALTER TABLE scheduled_chat_runs ADD COLUMN keep_recent_runs INTEGER NOT NULL DEFAULT 2;
        ALTER TABLE scheduled_chat_runs ADD COLUMN memory_focus TEXT NOT NULL DEFAULT '';
        ALTER TABLE scheduled_chat_run_history ADD COLUMN prompt_message_id TEXT;
        ALTER TABLE scheduled_chat_run_history ADD COLUMN compaction_cost_usd REAL;
    `

// migration0349 returns the scheduled-chat context-budget migration.
func migration0349() migrations.Migration {
	cols := []struct{ table, name, ddl string }{
		{"scheduled_chat_runs", "context_budget_tokens", "ALTER TABLE scheduled_chat_runs ADD COLUMN context_budget_tokens INTEGER NOT NULL DEFAULT 20000"},
		{"scheduled_chat_runs", "keep_recent_runs", "ALTER TABLE scheduled_chat_runs ADD COLUMN keep_recent_runs INTEGER NOT NULL DEFAULT 2"},
		{"scheduled_chat_runs", "memory_focus", "ALTER TABLE scheduled_chat_runs ADD COLUMN memory_focus TEXT NOT NULL DEFAULT ''"},
		{"scheduled_chat_run_history", "prompt_message_id", "ALTER TABLE scheduled_chat_run_history ADD COLUMN prompt_message_id TEXT"},
		{"scheduled_chat_run_history", "compaction_cost_usd", "ALTER TABLE scheduled_chat_run_history ADD COLUMN compaction_cost_usd REAL"},
	}
	return migrations.Migration{
		ID:            migrationIDScheduledChatContextBudget,
		Version:       349,
		OwningMission: OwningMission,
		UpSource:      sqlScheduledChatContextBudgetSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				var n int
				if err := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", c.table, c.name).Scan(&n); err != nil {
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
