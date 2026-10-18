package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDKnobs lands the per-session and per-message knob columns
// the provider-implementation-uniformity-01KQ8V4F WP09 needs:
//
//   - sessions.knobs_default — TEXT (nullable). A JSON-encoded RequestKnobs
//     struct carrying the per-session default reasoning effort, seed, etc.
//     NULL means "no session-level override; use global defaults".
//
//   - session_messages.knobs_override — TEXT (nullable). A JSON-encoded
//     RequestKnobs struct for the one message turn that overrides the
//     session default and/or global default. NULL means "inherit from
//     session/global".
//
//     STILL UNWIRED as of model-settings-reach-the-model-01PMZ101 UNIT-6 /
//     WP10 — that WP gave sessions.knobs_default (above) its first
//     production writer AND reader (Sessions_{Get,Set}KnobsDefault,
//     chat.LLMProviderAdapter.Generate's send-path merge), but deliberately
//     did NOT extend the same treatment to this column. Blocker: no
//     surface today asks for a PER-MESSAGE override distinct from the
//     per-session default — /effort sets a knob that "takes effect on the
//     next message" (cmd_effort.go) but has no notion of reverting after
//     that one message, and SessionTunePanel only ever edits the session
//     default. Wiring this column needs a product decision about what a
//     one-message override even means UX-wise (a slash-command flag? a
//     per-send toggle?) before it needs a Go reader. Owner: whichever
//     mission specs a per-message knob override; re-check at the release
//     after model-settings-reach-the-model-01PMZ101 UNIT-6 merges. See
//     docs/unwired-ledger.md for the dated entry.
//
// Numbering: 0330, the next free version after 0329 (provider_capabilities).
//
// Idempotent: the Up function checks pragma_table_info before issuing each
// ALTER TABLE. Re-running on a schema that already has the columns is safe.
//
// Down: follows the column-add convention — no DROP COLUMN; operators
// wanting a rollback restore from a pre-0330 backup.
const migrationIDKnobs = "sessions/0330-knobs"

// migration0330 returns the migration that adds knobs_default to sessions
// and knobs_override to session_messages.
func migration0330() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDKnobs,
		Version:       330,
		OwningMission: OwningMission,
		UpSource: `ALTER TABLE sessions ADD COLUMN knobs_default TEXT;
ALTER TABLE session_messages ADD COLUMN knobs_override TEXT;`,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			// sessions.knobs_default
			row := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='knobs_default'")
			var n int
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE sessions ADD COLUMN knobs_default TEXT"); err != nil {
					return err
				}
			}

			// session_messages.knobs_override
			row = tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name='knobs_override'")
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if _, err := tx.Exec(ctx,
					"ALTER TABLE session_messages ADD COLUMN knobs_override TEXT"); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			// Column-add; no rollback per convention.
			return nil
		},
	}
}
