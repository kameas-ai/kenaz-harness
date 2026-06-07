package narrative

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the
// memory-rag block (versions 800-899).
const MigrationOwner = "memory-rag"

// SQL DDL for each migration.
const sqlNarrativeJobsPending = `
	CREATE TABLE IF NOT EXISTS narrative_jobs_pending (
		id          TEXT    PRIMARY KEY,
		session_id  TEXT    NOT NULL,
		turn_id     TEXT    NOT NULL,
		project_id  TEXT    NOT NULL DEFAULT '',
		attempt     INTEGER NOT NULL DEFAULT 0,
		retry_at    INTEGER NOT NULL,
		last_error  TEXT    NOT NULL DEFAULT '',
		payload_json TEXT   NOT NULL DEFAULT '{}',
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL,
		status      TEXT    NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending','retrying','succeeded','failed'))
	);

	CREATE INDEX IF NOT EXISTS idx_narrative_jobs_scan
		ON narrative_jobs_pending(status, retry_at);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_narrative_jobs_turn_key
		ON narrative_jobs_pending(session_id, turn_id)
		WHERE status NOT IN ('succeeded','failed');
`

const sqlNarrativeMetrics = `
	CREATE TABLE IF NOT EXISTS narrative_metrics (
		chunk_id         TEXT    PRIMARY KEY,
		retrievals       INTEGER NOT NULL DEFAULT 0,
		citations        INTEGER NOT NULL DEFAULT 0,
		user_pins        INTEGER NOT NULL DEFAULT 0,
		last_retrieved_at INTEGER,
		last_cited_at    INTEGER,
		window_start     INTEGER NOT NULL,
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_narrative_metrics_score
		ON narrative_metrics(retrievals, citations, user_pins);
`

// Migrations returns the migration set owned by the memory-narrative-layer
// mission. Register with the storage migration registry at boot via
// RegisterMigrations.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            "memory-rag/0821-narrative-jobs-pending",
			Version:       821,
			OwningMission: MigrationOwner,
			UpSource:      sqlNarrativeJobsPending,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlNarrativeJobsPending) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_narrative_jobs_turn_key",
					"DROP INDEX IF EXISTS idx_narrative_jobs_scan",
					"DROP TABLE IF EXISTS narrative_jobs_pending",
				} {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			ID:            "memory-rag/0822-narrative-metrics",
			Version:       822,
			OwningMission: MigrationOwner,
			UpSource:      sqlNarrativeMetrics,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlNarrativeMetrics) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_narrative_metrics_score",
					"DROP TABLE IF EXISTS narrative_metrics",
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

// splitSQL splits multi-statement SQL on semicolons, trimming whitespace.
// Mirrors the helper in core/slashcmd/migrations.go.
func splitSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
