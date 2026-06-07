package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDResume is the identifier for migration 0317.
//
// This migration extends session_messages with the four columns the
// long-turn-resilience-01KR3PRS WP03 resume flow needs to persist a
// partial assistant turn after a stream drop:
//
//   - streaming_failed_at INTEGER       — unix-nanos timestamp captured
//                                         at the moment the chat runner
//                                         decided the stream was lost.
//                                         NULL on every healthy row.
//   - streaming_failure_kind TEXT       — one of "transient" | "auth" |
//                                         "unknown"; lets the UI tailor
//                                         the failure copy without
//                                         re-classifying. NULL on
//                                         healthy rows.
//   - streaming_recoverable INTEGER     — 0/1 bool. 1 when no tool_use
//                                         block executed before the
//                                         drop, so a continuation prompt
//                                         is safe. 0 when tool calls
//                                         already ran and the user
//                                         needs to re-issue the request
//                                         instead. NULL on healthy rows.
//   - continuation_of TEXT              — id of the partial assistant
//                                         row this row continues. NULL
//                                         on every original assistant
//                                         row; populated only on the
//                                         continuation written by the
//                                         resume RPC.
//
// All four are nullable so existing rows are not affected — a healthy
// assistant row has every column NULL, the same shape it had before
// the resume mission landed.
//
// One index rides along: idx_session_messages_continuation_of, used
// by the partial-message lookup the frontend issues to decide whether
// to render a "(continued below)" caption on the original bubble.
//
// Numbering: 0317 — next free version in the sessions block (300-399)
// after 0316 (autonomy_columns). The 0315 gap remains reserved per the
// note in migrations_autonomy.go.
const migrationIDResume = "sessions/0317-streaming-resume-columns"

const sqlResumeSchema = `
        ALTER TABLE session_messages ADD COLUMN streaming_failed_at INTEGER;
        ALTER TABLE session_messages ADD COLUMN streaming_failure_kind TEXT;
        ALTER TABLE session_messages ADD COLUMN streaming_recoverable INTEGER;
        ALTER TABLE session_messages ADD COLUMN continuation_of TEXT;
        CREATE INDEX IF NOT EXISTS idx_session_messages_continuation_of
            ON session_messages (continuation_of) WHERE continuation_of IS NOT NULL;
    `

// migration0317 returns the streaming-resume-columns migration.
//
// Down is a best-effort no-op for the columns (matching the package
// convention for additive column migrations — see migrations_autonomy.go,
// migrations_session_usage.go); the index is dropped on Down because
// dropping an index is cheap and always safe on SQLite.
func migration0317() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDResume,
		Version:       317,
		OwningMission: OwningMission,
		UpSource:      sqlResumeSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"ALTER TABLE session_messages ADD COLUMN streaming_failed_at INTEGER",
				"ALTER TABLE session_messages ADD COLUMN streaming_failure_kind TEXT",
				"ALTER TABLE session_messages ADD COLUMN streaming_recoverable INTEGER",
				"ALTER TABLE session_messages ADD COLUMN continuation_of TEXT",
				"CREATE INDEX IF NOT EXISTS idx_session_messages_continuation_of " +
					"ON session_messages (continuation_of) WHERE continuation_of IS NOT NULL",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// Drop the index (cheap, always safe). Columns are left in
			// place per the package convention for additive column
			// migrations: SQLite ALTER TABLE DROP COLUMN requires ≥3.35.0
			// and the columns have safe NULL defaults.
			_, err := tx.Exec(ctx, "DROP INDEX IF EXISTS idx_session_messages_continuation_of")
			return err
		},
	}
}
