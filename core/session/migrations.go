// Package session owns the harness's session-persistence subsystem.
// Migrations registered here populate the "sessions" reservation block
// (300-399) in core/storage/migrations.
//
// DIRECTIVE_001: this package is the single owner of the sessions and
// session_messages tables; all read/write access by other packages must
// go through the public Manager API.
package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

const (
	OwningMission = "sessions"

	migrationIDInit = "sessions/0300-init"
)

const sqlInitSchema = `
        CREATE TABLE IF NOT EXISTS projects (
            id          TEXT PRIMARY KEY,
            name        TEXT NOT NULL,
            description TEXT NOT NULL DEFAULT '',
            created_at  INTEGER NOT NULL,
            updated_at  INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_projects_name ON projects (name);

        CREATE TABLE IF NOT EXISTS sessions (
            id              TEXT PRIMARY KEY,
            name            TEXT NOT NULL,
            created_at      INTEGER NOT NULL,
            updated_at      INTEGER NOT NULL,
            last_active_at  INTEGER NOT NULL,
            position        INTEGER NOT NULL,
            draft           TEXT NOT NULL DEFAULT '',
            scroll_position INTEGER NOT NULL DEFAULT 0,
            archived_at     INTEGER,
            system_prompt   TEXT NOT NULL DEFAULT '',
            context_kind    TEXT NOT NULL DEFAULT 'system',
            project_id      TEXT NULL REFERENCES projects(id) ON DELETE SET NULL
        );

        CREATE INDEX IF NOT EXISTS idx_sessions_position
            ON sessions (position);
        CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at
            ON sessions (last_active_at);

        CREATE TABLE IF NOT EXISTS session_messages (
            id          TEXT PRIMARY KEY,
            session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            sequence    INTEGER NOT NULL,
            role        TEXT NOT NULL,
            content     TEXT NOT NULL,
            tool_calls  TEXT,
            created_at  INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_session_messages_session_seq
            ON session_messages (session_id, sequence);
    `

// Migrations returns the migration set that owns the sessions schema.
// Pre-1.0 collapse: schema lands in a single 0300 init since no install
// today carries useful state. Future schema changes get their own
// version in the 300 block — context-library WP03 lands as 0301
// (see migrations_attachments.go for the renumbering note); multimodal-io
// WP02 lands as 0302 (see migrations_content_json.go); artifacts-storage
// WP01 lands as 0303 (see migrations_artifacts.go); artifacts-storage
// WP02 lands as 0304 (see migrations_artifacts_promote.go);
// telemetry-otel WP01 lands as 0305 (see migrations_telemetry.go);
// agent-kernel-graph WP07 lands 0306 (branches — see
// migrations_branches.go); WP10 lands as 0307 (corpora — see
// migrations_corpora.go); WP04 lands 0308 (memory hook journal — see
// migrations_memory_hook_journal.go) and 0309 (agent_graph_events —
// see migrations_agent_graph_events.go); compaction-strategy-ui WP01
// lands as 0310 (compaction bookkeeping columns + indexes — see
// migrations_compaction.go); session-auto-titling WP01 lands as 0311
// (auto_titled column — see migrations_auto_titled.go);
// cross-session-search WP01 lands as 0312 (FTS5 virtual table +
// triggers — see migrations_search_fts.go);
// branch-as-subagent-recommendation WP04 lands as 0313 (subagent
// metadata columns — see migrations_subagent.go);
// token-cost-telemetry-01KQ8TD7 WP02 lands as 0314 (session_messages
// usage columns — see migrations_session_usage.go);
// autonomy-dial-01KR3M2A WP02 lands as 0316 (autonomy_level +
// autonomy_overrides columns on projects + sessions — see
// migrations_autonomy.go); long-turn-resilience-01KR3PRS WP03 lands
// as 0317 (streaming_failed_at + streaming_failure_kind +
// streaming_recoverable + continuation_of columns on session_messages —
// see migrations_resume.go). 0315 lands the cost_threshold_fired
// idempotency table for the threshold-notification scheduler
// (token-cost-telemetry-01KQ8TD7 WP06 — see migrations_cost_threshold.go).
// 0319 lands the workflows + workflow_versions tables that back the
// user-defined workflows store (workflows-01KQ8TDG WP06 — see
// migrations_workflows.go). 0320 lands the workflow_runs_cache table
// that backs the rerun_policy resolver (workflows-01KQ8TDG WP08 — see
// migrations_workflows_cache.go). 0321 lands the workflow_schedules
// table that backs the cron-scheduler persistence layer
// (workflows-agentic-01KW2D3X WP02 — see migrations_workflow_schedules.go).
// 0322 lands the sessions.last_usage_json column for the per-session usage
// snapshot (backend-context-window-length-01KQ8TD3 WP02 — see
// migrations_last_usage.go).
// 0323 lands the additive display-meta columns on branches
// (parent_message_id, branch_title, creation_path, parent_session_title)
// needed by the branching-ux-polish-01KQ8TD7 WP01 breadcrumb + sidebar
// (spec §3 / plan §1).
// 0324 lands the artifact_versions append-only history table that backs
// the kaneaz__update_artifact builtin tool
// (update-artifact-tool-01KQ8TD4 — see migrations_artifact_versions.go).
// 0326 lands the agent_graph_node_provenance table that records
// {kind, manifest_version_at_author, fingerprint_at_author} per graph node,
// enabling the manifest-drift detector on graph load
// (manifest-versioning-01NDFSEX02 WP03 — see migrations_manifest_provenance.go).
// 0327 extends the artifacts.source CHECK constraint to include
// 'model_output' for model-generated images captured by the WP02
// auto-capture pipeline (multimodal-io-extended-01KQ8TD2 WP02 —
// see migrations_source_model_output.go).
// 0328 adds image_width / image_height / page_count columns to the
// media_artifacts table so the metadata that Put() already extracts
// in-memory survives a Get/List round-trip (multimodal-io-01KQ8TDF
// FR-017 — see migrations_media_artifact_meta.go).
// 0329–0330 are reserved for the provider-implementation-uniformity
// foundation (parallel agent — not yet merged).
// 0331 adds the custom_endpoint_capabilities table that persists probed
// capability matrices for custom OpenAI-compatible endpoints
// (custom-openai-compatible-endpoint-01KQ8VN0 WP03 — see
// migrations_custom_endpoint.go).
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDInit,
			Version:       300,
			OwningMission: OwningMission,
			UpSource:      sqlInitSchema,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlInitSchema) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_session_messages_session_seq",
					"DROP TABLE IF EXISTS session_messages",
					"DROP INDEX IF EXISTS idx_sessions_last_active_at",
					"DROP INDEX IF EXISTS idx_sessions_position",
					"DROP TABLE IF EXISTS sessions",
					"DROP INDEX IF EXISTS idx_projects_name",
					"DROP TABLE IF EXISTS projects",
				} {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
		},
		migration0301(),
		migration0302(),
		migration0303(),
		migration0304(),
		migration0305(),
		migration0306(),
		migration0307(),
		migration0308(),
		migration0309(),
		migration0310(),
		migration0311(),
		migration0312(),
		migration0313(),
		migration0314(),
		migration0315(),
		migration0316(),
		migration0317(),
		migration0318(),
		migration0319(),
		migration0320(),
		migration0321(),
		migration0322(),
		migration0323(),
		migration0324(),
		migration0325(),
		migration0326(),
		migration0327(),
		migration0328(),
		migration0331(),
	}
}

// RegisterMigrations registers every migration returned by Migrations()
// and aborts on the first error. Callers MUST register before
// storage.Open so the framework picks them up before applying pending
// migrations.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitSQL is a tiny semicolon splitter. The DDL above contains no
// quoted semicolons, so a literal split is sufficient.
func splitSQL(src string) []string {
	out := make([]string, 0, 8)
	cur := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ';' {
			s := trimASCIISpace(string(cur))
			if s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		s := trimASCIISpace(string(cur))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimASCIISpace(s string) string {
	start := 0
	for start < len(s) {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}
