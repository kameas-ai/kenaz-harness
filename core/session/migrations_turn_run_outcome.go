package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDTurnRunOutcome identifies migration 0344 — the terminal
// outcome of each chat run, on the run's existing session_turn_runs row
// (undelivered-message-retry, dogfood 2026-10-07).
//
// THE BUG. An OpenRouter account ran out of credits; the provider
// answered 402 before a single token; the chat showed a transient "Send
// failed" banner and nothing on the message itself. The user's row was
// already persisted (the single-writer append precedes the run), so after
// adding credits the user re-typed and re-sent — a duplicate user row —
// because nothing said the first one had never reached the model, and
// nothing offered to re-run it.
//
// WHY HERE AND NOT ON session_messages. "Did this message reach the
// model" is a property of the RUN that tried to deliver it, not of the
// message: a message is delivered when some run carrying it got a
// response, and a retry is a second run of the same turn. session_turn_
// runs already holds one row per run keyed by run_id and carrying the
// turn_span_id (= the user message id), written synchronously at
// StartStream — so the outcome is a terminal UPDATE of a row that
// already exists, no new writer to the hot transcript table (the
// session_message writers gate is untouched), and the read side is the
// Sessions_TurnRuns list the transcript already loads for its run links.
// The delivery state of a message is then derived at read time: its
// newest run failed undelivered, and no LATER run in the session was
// delivered (a later delivered run carried it in history).
//
// Columns (all additive, all defaulted, so every pre-0344 row — and any
// run still in flight — reads as outcome '' = "unknown", which the
// surface renders as nothing at all, never as NOT DELIVERED):
//
//   - outcome          '' | 'completed' | 'failed' | 'stopped'
//   - delivered        NULL (unknown) | 0 | 1 — whether the model
//     accepted the request (any streamed output, or a completion)
//   - failure_class    llm.FailureClass ('user_actionable' | 'transient' |
//     'unknown'); '' unless outcome='failed'
//   - failure_code     llm.FailureCode* (payment_required, rate_limited…)
//   - failure_status   provider HTTP status, 0 when none
//   - failure_provider adapter kind ("openrouter")
//   - failure_summary  one-line copy ("Out of credits with OpenRouter")
//   - failure_message  provider message, sanitized by
//     llm.SanitizeProviderMessage (credential shapes redacted, capped)
//   - finished_at      unix nanos of the terminal write; NULL in flight
//
// Additive only: no existing column, row or constraint is touched, so
// check-destructive-migration-coverage.sh has nothing to cover. The
// upgrade-path proof is core/storage/sqlite/turn_run_outcome_upgrade_
// test.go, which starts from the committed v0.92.0 snapshot.
const migrationIDTurnRunOutcome = "sessions/0344-turn-run-outcome"

const sqlTurnRunOutcomeSchema = `
        ALTER TABLE session_turn_runs ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN delivered INTEGER;
        ALTER TABLE session_turn_runs ADD COLUMN failure_class TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN failure_status INTEGER NOT NULL DEFAULT 0;
        ALTER TABLE session_turn_runs ADD COLUMN failure_provider TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN failure_summary TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN failure_message TEXT NOT NULL DEFAULT '';
        ALTER TABLE session_turn_runs ADD COLUMN finished_at INTEGER;
    `

// migration0344 returns the turn-run-outcome migration.
//
// Idempotent, like migration0333: each ALTER is guarded by a
// pragma_table_info probe, so a re-run over a database that already has
// a column (the ledger-rewind repair path — see
// core/storage/sqlite/repair_upgrade_test.go) is a no-op instead of a
// "duplicate column name" failure that would block Open.
//
// Down is the package's convention for additive columns: a no-op (SQLite
// DROP COLUMN needs >= 3.35 and every column is defaulted, so leaving
// them is safe — see migration0317).
func migration0344() migrations.Migration {
	cols := []struct{ name, ddl string }{
		{"outcome", "ALTER TABLE session_turn_runs ADD COLUMN outcome TEXT NOT NULL DEFAULT ''"},
		{"delivered", "ALTER TABLE session_turn_runs ADD COLUMN delivered INTEGER"},
		{"failure_class", "ALTER TABLE session_turn_runs ADD COLUMN failure_class TEXT NOT NULL DEFAULT ''"},
		{"failure_code", "ALTER TABLE session_turn_runs ADD COLUMN failure_code TEXT NOT NULL DEFAULT ''"},
		{"failure_status", "ALTER TABLE session_turn_runs ADD COLUMN failure_status INTEGER NOT NULL DEFAULT 0"},
		{"failure_provider", "ALTER TABLE session_turn_runs ADD COLUMN failure_provider TEXT NOT NULL DEFAULT ''"},
		{"failure_summary", "ALTER TABLE session_turn_runs ADD COLUMN failure_summary TEXT NOT NULL DEFAULT ''"},
		{"failure_message", "ALTER TABLE session_turn_runs ADD COLUMN failure_message TEXT NOT NULL DEFAULT ''"},
		{"finished_at", "ALTER TABLE session_turn_runs ADD COLUMN finished_at INTEGER"},
	}
	return migrations.Migration{
		ID:            migrationIDTurnRunOutcome,
		Version:       344,
		OwningMission: OwningMission,
		UpSource:      sqlTurnRunOutcomeSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				var n int
				if err := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info('session_turn_runs') WHERE name=?", c.name).Scan(&n); err != nil {
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
