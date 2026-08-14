package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDMoves identifies migration 0333 — the transcript-move
// columns the model-moves-transcript-01PMCH01 mission needs so one
// human turn can persist as an ordered list of model moves instead of a
// single flattened assistant message (spec FR-001).
//
// Three nullable columns on session_messages:
//
//   - kind          TEXT    — "assistant_move" | "tool_call" |
//     "tool_result" | "final". NULL on every row written before this
//     migration and on every classic (non-move) row written after it.
//     NULL is the discriminator: a NULL kind means "render exactly as
//     before".
//   - move_index    INTEGER — 0-based, dense position of the entry
//     within its human turn. NULL when kind is NULL.
//   - turn_span_id  TEXT    — id of the user message that opened the
//     turn this entry belongs to. Every move in one turn shares it;
//     that shared id IS the turn's span. NULL when kind is NULL.
//
// Deliberately no CHECK constraint on kind: SQLite cannot ALTER TABLE a
// CHECK into place (see migration 0327 for the rename/copy/drop recipe
// that would be required), and the vocabulary is enforced ahead of the
// write by Manager.AppendTranscriptEntry — the only writer that can
// reach these columns (moves.go).
//
// Nullable + no default means existing rows are untouched and old
// readers that never SELECT these columns keep working verbatim.
//
// Numbering: 0333, the next free version after 0332
// (unified-context-artifacts-01NCTXU01 — see
// migrations_artifacts_global_scope.go).
const migrationIDMoves = "sessions/0333-transcript-moves"

const sqlMovesSchema = `
        ALTER TABLE session_messages ADD COLUMN kind TEXT;
        ALTER TABLE session_messages ADD COLUMN move_index INTEGER;
        ALTER TABLE session_messages ADD COLUMN turn_span_id TEXT;
    `

// migration0333 returns the transcript-move-columns migration.
//
// Idempotent: each ALTER is guarded by a pragma_table_info probe so a
// re-run (or a database that already picked the columns up from a
// future schema snapshot) is a no-op rather than a "duplicate column
// name" hard failure.
//
// Down is a no-op, matching the column-add convention used by 0302 /
// 0310 / 0314 / 0318: SQLite's DROP COLUMN needs >= 3.35.0 and the
// columns carry safe NULL defaults, so leaving them behind is harmless.
func migration0333() migrations.Migration {
	type column struct {
		name string
		ddl  string
	}
	cols := []column{
		{"kind", "ALTER TABLE session_messages ADD COLUMN kind TEXT"},
		{"move_index", "ALTER TABLE session_messages ADD COLUMN move_index INTEGER"},
		{"turn_span_id", "ALTER TABLE session_messages ADD COLUMN turn_span_id TEXT"},
	}
	return migrations.Migration{
		ID:            migrationIDMoves,
		Version:       333,
		OwningMission: OwningMission,
		UpSource:      sqlMovesSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				row := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name=?", c.name)
				var n int
				if err := row.Scan(&n); err != nil {
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
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			return nil
		},
	}
}
