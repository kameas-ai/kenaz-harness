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
// migrations_compaction.go); long-turn-resilience-01KR3PRS WP03 lands
// as 0317 (streaming_failed_at + streaming_failure_kind +
// streaming_recoverable + continuation_of columns on session_messages —
// see migrations_resume.go).
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
		migration0317(),
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
