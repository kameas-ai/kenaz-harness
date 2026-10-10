// Package mlstore is the durable half of the harness ML producer
// (ml-producer-01MLPRD01 WP02): the ml_outbox table the shipper drains and
// the ml_tasks table that keeps per-session task counters across restarts.
//
// It is its own package (rather than living in core/mlproducer) so
// core/storage/sqlite can register its migrations without importing the
// recorder and, through it, core/agentgraph.
package mlstore

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the ml-producer
// block (versions 1700-1799, core/storage/migrations/blocks.go). 1700 is
// the first range above laya-advisors' 1600-1699, the highest claimed
// block when this was reserved.
const MigrationOwner = "ml-producer"

// MigrationIDInit is the stable migration ID for ml_outbox + ml_tasks
// (version 1700).
const MigrationIDInit = "ml-producer/1700-ml-outbox-and-tasks"

// sqlInit is the DDL for migration 1700. Purely additive: CREATE ... IF
// NOT EXISTS on two new tables, nothing else touched.
//
//   - ml_outbox.seq is INTEGER PRIMARY KEY AUTOINCREMENT on purpose: with
//     AUTOINCREMENT sqlite never reuses a rowid, even after every row is
//     deleted (sqlite_sequence keeps the high-water mark). seq is the
//     event's kameas.ml.row_id / device_event_id, and fleet keys events by
//     it — a reused seq after a purge would be silently de-duplicated
//     against an older, different event.
//   - tbl is "events" | "tasks"; op is "insert" | "upsert"; row_id is the
//     decimal seq for an event and the task id for a task; body is the
//     JSON record body (never logged).
//   - ml_tasks holds the live task state so counters survive a restart.
//     completed_at = 0 means open. last_upsert_at drives the 60 s upsert
//     throttle.
const sqlInit = `
	CREATE TABLE IF NOT EXISTS ml_outbox (
	  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	  tbl        TEXT NOT NULL,
	  op         TEXT NOT NULL,
	  row_id     TEXT NOT NULL,
	  body       TEXT NOT NULL,
	  created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS ml_tasks (
	  task_id        TEXT PRIMARY KEY,
	  session_hash   TEXT NOT NULL,
	  repo_root_hash TEXT NOT NULL DEFAULT '',
	  phase          TEXT NOT NULL DEFAULT 'idle',
	  files_json     TEXT NOT NULL DEFAULT '{}',
	  started_at     INTEGER NOT NULL,
	  last_active    INTEGER NOT NULL,
	  completed_at   INTEGER NOT NULL DEFAULT 0,
	  commit_count   INTEGER NOT NULL DEFAULT 0,
	  test_runs      INTEGER NOT NULL DEFAULT 0,
	  test_fails     INTEGER NOT NULL DEFAULT 0,
	  last_upsert_at INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_ml_tasks_open_last_active
	  ON ml_tasks(completed_at, last_active)
`

// Migrations returns the migration set owned by the ml-producer mission.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            MigrationIDInit,
			Version:       1700,
			OwningMission: MigrationOwner,
			UpSource:      sqlInit,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlInit) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is best-effort: the outbox is unsent, consent-gated
			// telemetry and the task table is rebuildable bookkeeping.
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_ml_tasks_open_last_active",
					"DROP TABLE IF EXISTS ml_tasks",
					"DROP TABLE IF EXISTS ml_outbox",
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
// with reg (called from core/storage/sqlite.Open).
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitSQL is a semicolon splitter; the DDL above has no quoted semicolons.
func splitSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
