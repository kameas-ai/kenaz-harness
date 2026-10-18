package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDSessionTurnRuns identifies migration 0342 — the persisted
// chat turn -> agent-graph run mapping
// (agentgraph-settings-linkage-01DOGF0D WP03, spec FR-2).
//
// Every chat turn executes as one agent-graph kernel run whose events
// land in agent_graph_events under the run id. Before this table nothing
// durable said WHICH run a transcript turn was: the run id lived only in
// the frontend's streamSubscriptionId for the live stream, run_start
// carried no session or turn, and session_messages has no run column. So
// a past turn could not be linked to the graph of what the agent did.
//
// One row per chat run, written synchronously at StartStream:
//
//   - run_id: the kernel run id ("chat-<ULID>" since WP02);
//   - turn_span_id: the user message id every move of the turn carries
//     (session_messages.turn_span_id), the key the transcript groups a
//     turn by;
//   - graph_id: the graph the run executed;
//   - spec_digest: agentgraph.SpecDigest of the RESOLVED spec, i.e.
//     which version ran, so a library-fallback materialization can
//     verify itself instead of always warning.
//
// A side table rather than a run_id column on session_messages: that is
// the hot table, and chat-single-writer-01DOGF0G is concurrently
// changing which row anchors a turn's span. Rows for turns before this
// migration simply do not exist — the transcript renders those turns'
// run affordance disabled with a reason, never a link that could 404 or
// open the wrong run (pre-WP02 "chat-<n>" ids were reused across
// restarts).
//
// ON DELETE CASCADE from sessions, mirroring session_messages and
// stream_checkpoints: sqlStore.Delete relies on FK cascade.
//
// Numbering: 0342. Cross-mission order for the dogfood-2026-10-04 round
// is G -> D -> C: chat-single-writer-01DOGF0G claims 0341 (its
// dedupe-user-turns migration), this mission 0342, and
// artifacts-as-units-01DOGF0C 0343. Additive only — no existing table
// is touched, so check-destructive-migration-coverage.sh has nothing to
// cover.
const migrationIDSessionTurnRuns = "sessions/0342-session-turn-runs"

const sqlSessionTurnRunsSchema = `
        CREATE TABLE IF NOT EXISTS session_turn_runs (
            run_id        TEXT PRIMARY KEY,
            session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            turn_span_id  TEXT NOT NULL DEFAULT '',
            graph_id      TEXT NOT NULL DEFAULT '',
            spec_digest   TEXT NOT NULL DEFAULT '',
            created_at    INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_session_turn_runs_session
            ON session_turn_runs (session_id, created_at);
    `

// migration0342 returns the session_turn_runs migration. Down drops the
// table: turns lose their run-graph links (the affordance degrades to
// disabled-with-reason), and nothing else references the table.
func migration0342() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSessionTurnRuns,
		Version:       342,
		OwningMission: OwningMission,
		UpSource:      sqlSessionTurnRunsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlSessionTurnRunsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_session_turn_runs_session",
				"DROP TABLE IF EXISTS session_turn_runs",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
