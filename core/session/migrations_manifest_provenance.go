package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDManifestProvenance identifies migration 0326 — the schema
// landing for the manifest-versioning mission (manifest-versioning-01NDFSEX02,
// WP03). Adds the agent_graph_node_provenance table which records the
// manifest contract at the time a graph node was authored.
//
// Columns:
//   - graph_id: identifies the parent graph (YAML path or UUID).
//   - node_id:  the node's ID within the graph.
//   - kind:     the NodeKind string (e.g. "model", "decision").
//   - manifest_version_at_author: semver from the manifest at author time.
//   - fingerprint_at_author: sha256 hex of the behavior-affecting subset.
//
// Existing nodes that pre-date this migration have no row (empty provenance
// is treated as "unknown provenance, no drift warning" per spec WP03).
//
// Numbering: 0326, the next free version after 0325
// (scheduled_chat_runs — see migrations_scheduled_chat_runs.go).
const migrationIDManifestProvenance = "sessions/0326-manifest-provenance"

const sqlManifestProvenanceSchema = `
        CREATE TABLE IF NOT EXISTS agent_graph_node_provenance (
            graph_id                    TEXT NOT NULL,
            node_id                     TEXT NOT NULL,
            kind                        TEXT NOT NULL DEFAULT '',
            manifest_version_at_author  TEXT NOT NULL DEFAULT '',
            fingerprint_at_author       TEXT NOT NULL DEFAULT '',
            PRIMARY KEY (graph_id, node_id)
        );

        CREATE INDEX IF NOT EXISTS idx_agraph_node_prov_kind
            ON agent_graph_node_provenance (kind);
    `

// migration0326 returns the agent_graph_node_provenance migration.
func migration0326() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDManifestProvenance,
		Version:       326,
		OwningMission: OwningMission,
		UpSource:      sqlManifestProvenanceSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlManifestProvenanceSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_agraph_node_prov_kind",
				"DROP TABLE IF EXISTS agent_graph_node_provenance",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
