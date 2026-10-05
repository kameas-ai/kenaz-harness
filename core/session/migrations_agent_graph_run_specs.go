package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDAgentGraphRunSpecs identifies migration 0343 — the exact
// resolved graph spec each agent-graph run executed, persisted once per
// run (feat/graph-resolved-spec, the follow-up
// agentgraph-settings-linkage-01DOGF0D §5 named: "Persisting the exact
// resolved spec per run (would remove the tier-3 fallback)").
//
// Before this table the resolved spec — post kind-alias rewrite, post
// routing-gate rewrite, post max-turns dial — lived only in two
// in-memory registries (the Manager's started runs and the 64-entry
// chat-run registry). After a restart, or 64 chat turns later, a run's
// materialized graph had to be RECONSTRUCTED from whatever library file
// its run_start event named: the graph as it is now, not as it ran. An
// edit to the graph after the run changed what the run view showed.
//
// One row per run, written by the kernel at run start
// (core/agentgraph.Kernel.Run via the EventLog's RunSpecStore half):
//
//   - run_id: the kernel run id, the same key agent_graph_events uses;
//   - graph_id: the resolved spec's id (denormalised for inspection);
//   - spec_digest: agentgraph.SpecDigest of the stored spec — checked
//     on every read, so a row that no longer decodes to the spec it was
//     written from is refused rather than served as exact;
//   - spec_json: the resolved spec, agentgraph.DumpJSON; bounded by
//     agentgraph.MaxRunSpecBytes at the writer (an oversized spec is not
//     stored and its run view says it is a reconstruction);
//   - created_at_ns: write time.
//
// Insert-once: a resumed or redriven run re-enters Kernel.Run with the
// same run id and the same spec, and the writer's ON CONFLICT DO NOTHING
// keeps the first row. No foreign key to sessions — agent_graph_events
// has none either (a Graphs-view run need not belong to a session), and
// the two tables share one lifecycle.
//
// Additive only: a new table, no existing table touched, so
// check-destructive-migration-coverage.sh has nothing to cover. CREATE
// ... IF NOT EXISTS so a re-open after a ledger rewind re-applies it
// without touching rows already stored (pinned by
// core/storage/sqlite/migration_0343_test.go).
//
// Numbering: 0343, the next free sessions slot after 0342
// (session_turn_runs). artifacts-as-units-01DOGF0C, which 0342's comment
// had pencilled in for 0343, landed in the units block instead
// (units/1104).
const migrationIDAgentGraphRunSpecs = "sessions/0343-agent-graph-run-specs"

const sqlAgentGraphRunSpecsSchema = `
        CREATE TABLE IF NOT EXISTS agent_graph_run_specs (
            run_id         TEXT PRIMARY KEY,
            graph_id       TEXT NOT NULL DEFAULT '',
            spec_digest    TEXT NOT NULL DEFAULT '',
            spec_json      TEXT NOT NULL,
            created_at_ns  INTEGER NOT NULL
        );
    `

// migration0343 returns the agent_graph_run_specs migration. Down drops
// the table: runs lose their exact spec and their run views fall back to
// the labelled reconstruction, exactly as runs from before the table.
func migration0343() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDAgentGraphRunSpecs,
		Version:       343,
		OwningMission: OwningMission,
		UpSource:      sqlAgentGraphRunSpecsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlAgentGraphRunSpecsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS agent_graph_run_specs")
			return err
		},
	}
}
