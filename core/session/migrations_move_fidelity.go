package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDMoveFidelity identifies migration 0334 — the two columns
// the model-visible half of model-moves-transcript-01PMCH01 needs
// (WP03, spec FR-002 + §4).
//
// WP01/0333 gave a transcript entry its move IDENTITY (kind, position,
// turn span). That is everything the DISPLAY layer needs. It is not
// everything the MODEL layer needs, and the two differ in exactly one
// place — tool arguments — which is why this is a separate migration
// with separately-named columns rather than a widening of 0333.
//
//   - session_messages.model_tool_args  TEXT
//
//     The MODEL LAYER's copy of a tool_call move's arguments: the raw
//     JSON the model emitted, keyed by tool-call id. NULL on every
//     classic row, on every non-tool_call move, and on every row
//     written before this migration.
//
//     WHY A SEPARATE COLUMN AND NOT session_messages.tool_calls.
//     session.ToolCall.Arguments already exists and is already
//     round-tripped through the tool_calls JSON column — and WP02
//     deliberately leaves it nil, with a test pinning that
//     (TestMoveToolCalls_NeverCarriesRawArguments). That decision is
//     correct and this migration does not overturn it: session.ToolCall
//     is exported, durable, and projected toward the display surface,
//     so raw argument VALUES in it are one field-projection away from a
//     transcript row the user can read, copy, export and share. A tool
//     argument is frequently the most sensitive text in a session (a
//     token being written to a config file, a password inside a bash
//     command).
//
//     The model layer's claim on the same bytes is legitimate and
//     opposite: the provider protocol REQUIRES the arguments to
//     reconstruct an assistant tool_use block, the model authored them
//     in the first place, and this is history rather than logs (spec
//     §4). Two claims, two lifetimes, two names. Storing them apart is
//     what makes "do not fix one layer with the other" a property of
//     the schema instead of a comment somebody later disagrees with.
//
//   - sessions.move_history_mode  TEXT
//
//     Which model-visible-history mode this session was CREATED under:
//     "moves" for a session opened while the move-fidelity dial was on,
//     "classic" for one opened while it was off. NULL on every session
//     that predates this migration — which is the whole point.
//
//     Spec §4 requires the gate default ON for new sessions and OFF for
//     existing ones, so an in-flight conversation does not change shape
//     underneath the model mid-thread. NULL is that "existing session"
//     marker, and it is read fail-closed: NULL, unknown, or unreadable
//     all resolve to the classic composition. See
//     MoveHistoryModeFromRecord in moves_fidelity.go — the only reader.
//
// Nullable, no DEFAULT, no UPDATE backfill: existing rows are untouched
// and every reader that never SELECTs these columns keeps working
// verbatim. That matches every column-add in the sessions block (0311,
// 0316, 0318, 0330, 0333) — no migration in this tree runs a backfill
// UPDATE, and this one must not either: stamping "moves" onto sessions
// that were written classic would be precisely the mid-thread shape
// change the gate exists to prevent.
//
// Numbering: 0334, the next free version after 0333 (migrations_moves.go).
const migrationIDMoveFidelity = "sessions/0334-move-fidelity-columns"

const sqlMoveFidelitySchema = `
        ALTER TABLE session_messages ADD COLUMN model_tool_args TEXT;
        ALTER TABLE sessions ADD COLUMN move_history_mode TEXT;
    `

// migration0334 returns the move-fidelity-columns migration.
//
// Idempotent: each ALTER is guarded by a pragma_table_info probe, so a
// re-run (or a database that already picked the columns up from a newer
// schema snapshot) is a no-op rather than a "duplicate column name" hard
// failure. Same recipe as 0333.
//
// Down is a no-op, matching the column-add convention used by 0302 /
// 0310 / 0314 / 0316 / 0318 / 0333: SQLite's DROP COLUMN needs >= 3.35.0
// and both columns carry safe NULL defaults, so leaving them behind is
// harmless.
func migration0334() migrations.Migration {
	type column struct {
		table string
		name  string
		ddl   string
	}
	cols := []column{
		{"session_messages", "model_tool_args",
			"ALTER TABLE session_messages ADD COLUMN model_tool_args TEXT"},
		{"sessions", "move_history_mode",
			"ALTER TABLE sessions ADD COLUMN move_history_mode TEXT"},
	}
	return migrations.Migration{
		ID:            migrationIDMoveFidelity,
		Version:       334,
		OwningMission: OwningMission,
		UpSource:      sqlMoveFidelitySchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				row := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", c.table, c.name)
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
