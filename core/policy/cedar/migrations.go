package cedar

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the
// cedar-policy block (versions 1300-1399, core/storage/migrations/blocks.go).
//
// finding-58-cedar-decision-persistence: before this migration, Cedar's
// audit-decision log (Engine.decisions) had no persistent backing in
// production — Options.Decisions was never set at either real
// cedar.NewEngine call site (core/rpc/api.go buildCedarEngineOrNil /
// buildCedarGate), so both fell back to NewMemoryDecisionStore(0): a
// 256-entry in-memory ring that is silently truncated while running and
// wholly lost on restart. This is the audit trail for every
// permission-gate decision (trust/compliance-relevant per this repo's
// disposition rules) — the table this file creates is what
// SQLDecisionStore (sql_decision_store.go) writes to.
const MigrationOwner = "cedar-policy"

// migrationIDPolicyDecisionsInit is the stable migration ID for the
// policy_decisions table (version 1300).
const migrationIDPolicyDecisionsInit = "cedar-policy/1300-policy-decisions"

// sqlPolicyDecisionsInit is the DDL for migration 1300 — the durable
// backing table for the Cedar engine's audit-decision log.
// CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS throughout so
// it is safe to run against a populated database: it creates, it never
// destroys (mirrors core/tasks/migrations.go's pattern).
//
// id is an autoincrementing surrogate key so SQLDecisionStore can order
// "most recent N" cheaply (ORDER BY id DESC) without relying on
// evaluated_at, which is wall-clock time and not guaranteed monotonic
// across rapid consecutive decisions. evaluated_at is stored as Unix
// millis (INTEGER) for cheap comparison/indexing; the Go layer
// reconstructs time.Time via time.UnixMilli.
const sqlPolicyDecisionsInit = `
	CREATE TABLE IF NOT EXISTS policy_decisions (
	  id             INTEGER PRIMARY KEY AUTOINCREMENT,
	  outcome        TEXT NOT NULL,
	  action         TEXT NOT NULL,
	  principal      TEXT NOT NULL,
	  resource       TEXT NOT NULL,
	  matched_policy TEXT NOT NULL DEFAULT '',
	  reason         TEXT NOT NULL DEFAULT '',
	  evaluated_at   INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_policy_decisions_evaluated_at ON policy_decisions(evaluated_at);
`

// Migrations returns the migration set owned by the cedar-policy
// mission. Register with the storage migration registry at boot via
// RegisterMigrations, before storage.Open applies pending migrations.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDPolicyDecisionsInit,
			Version:       1300,
			OwningMission: MigrationOwner,
			UpSource:      sqlPolicyDecisionsInit,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitPolicyDecisionsSQL(sqlPolicyDecisionsInit) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is best-effort: drops the table and its index. Losing
			// decision history on rollback is the accepted posture for a
			// purely observational audit table (no other subsystem's
			// correctness depends on it) — mirrors core/tasks's Down.
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_policy_decisions_evaluated_at",
					"DROP TABLE IF EXISTS policy_decisions",
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

// splitPolicyDecisionsSQL is a tiny semicolon splitter mirroring
// core/tasks/migrations.go's splitTasksSQL — the DDL above contains no
// quoted semicolons, so a literal split is sufficient.
func splitPolicyDecisionsSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
