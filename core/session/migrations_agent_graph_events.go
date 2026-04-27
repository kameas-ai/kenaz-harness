package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDAgentGraphEvents lands the agent_graph_events table — the
// canonical EventLog for the agent-kernel-graph mission. Every node
// fire produces N events (node_start, kind-specific side effects,
// node_complete) and the kernel persists them as a single transactional
// batch via core/agentgraph.SQLEventLog.
//
// Numbering: 0309 per the mission spec §4.11 / FR-057. (0306 = branches;
// 0307 = corpora; 0308 = memory hook journal.)
const migrationIDAgentGraphEvents = "sessions/0309-agent-graph-events"

const sqlAgentGraphEventsSchema = `
        CREATE TABLE IF NOT EXISTS agent_graph_events (
            run_id        TEXT NOT NULL,
            session_id    TEXT NOT NULL DEFAULT '',
            node_id       TEXT NOT NULL DEFAULT '',
            seq           INTEGER NOT NULL,
            kind          TEXT NOT NULL,
            payload_json  TEXT NOT NULL DEFAULT 'null',
            ts_ns         INTEGER NOT NULL,
            PRIMARY KEY (run_id, seq)
        );

        CREATE INDEX IF NOT EXISTS idx_agent_graph_events_session
            ON agent_graph_events (session_id, ts_ns);

        CREATE INDEX IF NOT EXISTS idx_agent_graph_events_kind
            ON agent_graph_events (kind, ts_ns);

        CREATE INDEX IF NOT EXISTS idx_agent_graph_events_node
            ON agent_graph_events (run_id, node_id);
    `

// migration0309 returns the agent_graph_events migration.
func migration0309() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDAgentGraphEvents,
		Version:       309,
		OwningMission: OwningMission,
		UpSource:      sqlAgentGraphEventsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlAgentGraphEventsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_agent_graph_events_node",
				"DROP INDEX IF EXISTS idx_agent_graph_events_kind",
				"DROP INDEX IF EXISTS idx_agent_graph_events_session",
				"DROP TABLE IF EXISTS agent_graph_events",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
