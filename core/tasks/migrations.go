package tasks

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the tasks
// block (versions 1200-1299, core/storage/migrations/blocks.go).
const MigrationOwner = "tasks"

// migrationIDTasksInit is the stable migration ID for the tasks table
// (version 1200).
const migrationIDTasksInit = "tasks/1200-tasks-init"

// sqlTasksInit is the DDL for migration 1200 — the background-task
// registry's persistence table. CREATE TABLE IF NOT EXISTS / CREATE INDEX
// IF NOT EXISTS throughout, so it is safe to run against a populated
// database: it creates, it never destroys (spec.md §5.1, AC-PI-3).
const sqlTasksInit = `
	CREATE TABLE IF NOT EXISTS tasks (
	  id               TEXT PRIMARY KEY,
	  kind             TEXT NOT NULL,
	  owner_session_id TEXT,
	  cmd              TEXT,
	  description      TEXT,
	  status           TEXT NOT NULL,
	  exit_code        INTEGER,
	  started_at       INTEGER NOT NULL,
	  ended_at         INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_owner  ON tasks(owner_session_id);
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
`

// Migrations returns the migration set owned by the tasks mission.
// Register with the storage migration registry at boot via
// RegisterMigrations, before storage.Open applies pending migrations.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDTasksInit,
			Version:       1200,
			OwningMission: MigrationOwner,
			UpSource:      sqlTasksInit,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitTasksSQL(sqlTasksInit) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is best-effort: drops the table and its indexes. Any
			// task history recorded after upgrade is lost on rollback,
			// which is the accepted posture for a purely observational
			// table (no other subsystem's correctness depends on it).
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_tasks_status",
					"DROP INDEX IF EXISTS idx_tasks_owner",
					"DROP TABLE IF EXISTS tasks",
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

// splitTasksSQL is a tiny semicolon splitter mirroring
// core/slashcmd/migrations.go's splitSQLMigration and
// core/units/migrations.go's splitUnitSQL — the DDL above contains no
// quoted semicolons, so a literal split is sufficient.
func splitTasksSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
