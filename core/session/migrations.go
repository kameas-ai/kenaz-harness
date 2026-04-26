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

// Owning-mission name and per-migration ids. Constants live at package
// scope so consumers (eg. integration tests) can refer to them without
// reaching into the migration framework.
const (
	OwningMission = "sessions"

	migrationIDSessionsTable      = "sessions/0300-sessions-table"
	migrationIDMessagesTable      = "sessions/0301-session-messages-table"
	migrationIDIndexes            = "sessions/0302-sessions-indexes"
	migrationIDSystemPromptColumn = "sessions/0303-sessions-system-prompt"
	migrationIDContextKindColumn  = "sessions/0304-sessions-context-kind"
	migrationIDProjectIDColumn    = "sessions/0306-sessions-project-id"
	migrationIDProjectsTable      = "sessions/0307-projects-table"
)

// SQL bodies. Keep the DDL byte-stable: the migration framework hashes
// these strings to detect tampering, so any whitespace-meaningful change
// reissues the hash.
const (
	sqlCreateSessionsTable = `
        CREATE TABLE IF NOT EXISTS sessions (
            id              TEXT PRIMARY KEY,
            name            TEXT NOT NULL,
            created_at      INTEGER NOT NULL,
            updated_at      INTEGER NOT NULL,
            last_active_at  INTEGER NOT NULL,
            position        INTEGER NOT NULL,
            draft           TEXT NOT NULL DEFAULT '',
            scroll_position INTEGER NOT NULL DEFAULT 0,
            archived_at     INTEGER
        );
    `

	sqlCreateMessagesTable = `
        CREATE TABLE IF NOT EXISTS session_messages (
            id          TEXT PRIMARY KEY,
            session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            sequence    INTEGER NOT NULL,
            role        TEXT NOT NULL,
            content     TEXT NOT NULL,
            tool_calls  TEXT,
            created_at  INTEGER NOT NULL
        );
    `

	sqlCreateIndexes = `
        CREATE INDEX IF NOT EXISTS idx_session_messages_session_seq
            ON session_messages (session_id, sequence);
        CREATE INDEX IF NOT EXISTS idx_sessions_position
            ON sessions (position);
        CREATE INDEX IF NOT EXISTS idx_sessions_last_active_at
            ON sessions (last_active_at);
    `

	// SQLite cannot ALTER TABLE … DROP COLUMN before 3.35; the Down
	// path is a deliberate no-op so rollback to v302 leaves the
	// columns in place. Re-applying the Up is idempotent because the
	// column already exists, and the runner won't call Up again
	// without a rolled_back ledger row.
	sqlAddSystemPromptColumn = `
        ALTER TABLE sessions
            ADD COLUMN system_prompt TEXT NOT NULL DEFAULT '';
    `

	sqlAddContextKindColumn = `
        ALTER TABLE sessions
            ADD COLUMN context_kind TEXT NOT NULL DEFAULT 'system';
    `

	sqlAddProjectIDColumn = `
        ALTER TABLE sessions
            ADD COLUMN project_id TEXT NULL REFERENCES projects(id) ON DELETE SET NULL;
    `

	sqlCreateProjectsTable = `
        CREATE TABLE IF NOT EXISTS projects (
            id          TEXT PRIMARY KEY,
            name        TEXT NOT NULL,
            description TEXT NOT NULL DEFAULT '',
            created_at  INTEGER NOT NULL,
            updated_at  INTEGER NOT NULL
        );
        CREATE INDEX IF NOT EXISTS idx_projects_name ON projects (name);
    `
)

// Migrations returns the migration set that owns the sessions schema.
// They sit in the reserved 300-399 block; the migration framework
// rejects them if blocks.go is out of sync.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDSessionsTable,
			Version:       300,
			OwningMission: OwningMission,
			UpSource:      sqlCreateSessionsTable,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				_, err := tx.Exec(ctx, sqlCreateSessionsTable)
				return err
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS sessions")
				return err
			},
		},
		{
			ID:            migrationIDMessagesTable,
			Version:       301,
			OwningMission: OwningMission,
			UpSource:      sqlCreateMessagesTable,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				_, err := tx.Exec(ctx, sqlCreateMessagesTable)
				return err
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS session_messages")
				return err
			},
		},
		{
			ID:            migrationIDIndexes,
			Version:       302,
			OwningMission: OwningMission,
			UpSource:      sqlCreateIndexes,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlCreateIndexes) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_sessions_last_active_at",
					"DROP INDEX IF EXISTS idx_sessions_position",
					"DROP INDEX IF EXISTS idx_session_messages_session_seq",
				} {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			ID:            migrationIDSystemPromptColumn,
			Version:       303,
			OwningMission: OwningMission,
			UpSource:      sqlAddSystemPromptColumn,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlAddSystemPromptColumn) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return nil
			},
		},
		{
			ID:            migrationIDContextKindColumn,
			Version:       304,
			OwningMission: OwningMission,
			UpSource:      sqlAddContextKindColumn,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlAddContextKindColumn) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return nil
			},
		},
		{
			ID:            migrationIDProjectIDColumn,
			Version:       306,
			OwningMission: OwningMission,
			UpSource:      sqlAddProjectIDColumn,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlAddProjectIDColumn) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return nil
			},
		},
		{
			ID:            migrationIDProjectsTable,
			Version:       307,
			OwningMission: OwningMission,
			UpSource:      sqlCreateProjectsTable,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlCreateProjectsTable) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
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
	}
}

// RegisterMigrations is a convenience wrapper used at boot. It calls
// Register on every migration returned by Migrations() and aborts on the
// first registration error. Callers MUST register before storage.Open
// so the framework picks them up before applying pending migrations.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitSQL is a tiny semicolon splitter. The DDL above contains no
// quoted semicolons, so a literal split is sufficient. Trim whitespace
// and drop empty entries.
func splitSQL(src string) []string {
	out := make([]string, 0, 4)
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
