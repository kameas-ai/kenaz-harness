package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatRunsSession identifies migration 0348 — the
// conversation a schedule's runs continue.
//
// Before this column every fire of a scheduled chat created a brand-new
// "Scheduled: <name>" session, so a */30 schedule left hundreds of chats
// behind. A schedule now owns ONE conversation: the first fire creates it
// and stores its id here, and every later fire appends its rendered prompt
// as a new user turn in that same session
// (core/rpc/chat_run_dispatcher.go, the only reader and writer).
//
// Column (additive, nullable):
//
//   - session_id TEXT  the schedule's conversation; NULL until the first
//     fire creates one, and again after a first fire whose empty session
//     was discarded. A dangling id (the user deleted the session) is
//     repaired at the next fire by creating a new session and storing it.
//
// No foreign key: deleting the conversation must not delete the schedule,
// and deleting the schedule leaves the conversation as an ordinary chat.
//
// Additive only, so check-destructive-migration-coverage.sh has nothing to
// cover. Idempotent like migration0345: the ALTER is guarded by a
// pragma_table_info probe. Down is a no-op, the package convention for
// additive columns.
const migrationIDScheduledChatRunsSession = "sessions/0348-scheduled-chat-runs-session-id"

const sqlScheduledChatRunsSessionSchema = `
        ALTER TABLE scheduled_chat_runs ADD COLUMN session_id TEXT;
    `

// migration0348 returns the scheduled_chat_runs.session_id migration.
func migration0348() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDScheduledChatRunsSession,
		Version:       348,
		OwningMission: OwningMission,
		UpSource:      sqlScheduledChatRunsSessionSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			var n int
			if err := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('scheduled_chat_runs') WHERE name='session_id'").Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			_, err := tx.Exec(ctx, "ALTER TABLE scheduled_chat_runs ADD COLUMN session_id TEXT")
			return err
		},
		Down: func(context.Context, migrations.WriteTx) error { return nil },
	}
}
