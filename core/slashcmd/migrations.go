package slashcmd

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the
// user-slash-commands block (versions 1000-1099).
const MigrationOwner = "user-slash-commands"

// migrationIDSlashCommandsUser is the stable migration ID for the
// slash_commands_user table (version 1000).
//
// Cross-mission note: the model-invoked-skills-catalog-01KZNP3E
// mission will add a follow-up migration at version 1001 to add the
// model_invokable DB column. Until that migration lands, model_invokable
// is sourced exclusively from the markdown frontmatter at runtime.
// DO NOT add a model_invokable column here; leave room for version 1001.
const migrationIDSlashCommandsUser = "user-slash-commands/1000-slash-commands-user"

const sqlSlashCommandsUser = `
	CREATE TABLE IF NOT EXISTS slash_commands_user (
		name             TEXT PRIMARY KEY,
		scope            TEXT NOT NULL,
		project_id       TEXT,
		kind             TEXT NOT NULL,
		description      TEXT NOT NULL DEFAULT '',
		when_to_use      TEXT NOT NULL DEFAULT '',
		does_not_handle  TEXT NOT NULL DEFAULT '',
		model_invokable  INTEGER NOT NULL DEFAULT 0,
		payload_path     TEXT NOT NULL,
		icon             TEXT,
		hidden_from_panel INTEGER NOT NULL DEFAULT 0,
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_slash_user_scope
		ON slash_commands_user(scope, project_id);

	CREATE INDEX IF NOT EXISTS idx_slash_user_model_invokable
		ON slash_commands_user(model_invokable);
`

// Migrations returns the migration set owned by the user-slash-commands
// mission. Register with the storage migration registry at boot via
// RegisterMigrations.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDSlashCommandsUser,
			Version:       1000,
			OwningMission: MigrationOwner,
			UpSource:      sqlSlashCommandsUser,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQLMigration(sqlSlashCommandsUser) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_slash_user_model_invokable",
					"DROP INDEX IF EXISTS idx_slash_user_scope",
					"DROP TABLE IF EXISTS slash_commands_user",
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

// RegisterMigrations registers every migration returned by Migrations()
// with reg. Callers must call this before storage.Open so the framework
// picks them up before applying pending migrations.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitSQLMigration splits multi-statement SQL on semicolons. No
// quoted semicolons appear in the DDL above, so a literal split is
// sufficient. Mirrors the helper in core/session/migrations.go.
func splitSQLMigration(src string) []string {
	out := make([]string, 0, 8)
	cur := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ';' {
			s := trimSQLSpace(string(cur))
			if s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		s := trimSQLSpace(string(cur))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimSQLSpace(s string) string {
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
