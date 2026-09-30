// Package labels is the label-capture bridge for laya-advisors-01LAYA001
// WP08 (spec §4, design §5.2): "every recommendation + user action becomes
// a training example." It owns the advice_labels table and the
// CaptureAdvisor decorator (capture.go) that wraps a production
// core/advice.Advisor so every Recommend outcome — including below-75 and
// decision=false rows the chip never shows — lands a row, and every later
// user action (accept / dismiss / auto-act) updates that row in place.
//
// Local-only in v1 (spec §4: "Capture is local-only in v1. Export to
// kenaz-ml for fine-tuning is a separate mission with its own consent +
// redaction review"). Nothing in this package ever crosses a network
// boundary.
package labels

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the
// laya-advisors block (versions 1600-1699,
// core/storage/migrations/blocks.go). Chosen after checking finding
// #55's two collision pairs (a2a/signed-cards-trust sharing 600-699;
// bundle/shared-context-distribution sharing 700-799, both already
// resolved in blocks.go) and confirming 1600-1699 was the first
// unclaimed range above the highest block reserved at the time
// (1500-1599, shared-context-distribution).
const MigrationOwner = "laya-advisors"

// migrationIDAdviceLabelsInit is the stable migration ID for the
// advice_labels table (version 1600).
const migrationIDAdviceLabelsInit = "laya-advisors/1600-advice-labels"

// sqlAdviceLabelsInit is the DDL for migration 1600 — the durable label
// corpus backing the advisor seam's training data (spec §4 / design
// §5.2). CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so this
// is safe to run against a populated database (mirrors
// core/policy/cedar/migrations.go's pattern) — this is also the FIRST
// migration in the tree to land against a snapshot lineage whose `units`
// table already carries real rows (v0.83.0's two KindDoc seed rows), so
// it must touch nothing outside its own new table.
//
// Column notes:
//   - features_json is the full extracted feature vector (spec §4: "the
//     full feature vector as JSON") — advice.Features marshaled via
//     encoding/json, the SAME bytes advice.FeaturesHash hashes.
//   - shown is design §5.2's explicit propensity field: whether the ≥75
//     chip gate actually rendered this recommendation (spec §2d:
//     below-threshold recommendations are still captured with
//     shown=false). confidence (already a required field) doubles as
//     design §5.2's "confidence-at-decision-time" — a Recommendation
//     carries exactly one confidence value, so a second column would
//     only duplicate it.
//   - user_action starts 'ignored' at insert time (spec enum: accepted /
//     dismissed / ignored / auto_acted) and is updated in place by
//     UpdateAction when the user (or an auto-act call site) later acts
//     on the SAME (session_id, kind, features_hash) key — see
//     capture.go's RecordAction.
//   - session_id is the LOCAL session id (spec §4: "never exported
//     as-is" — this package never exports anything; the field is local
//     by construction in v1).
const sqlAdviceLabelsInit = `
	CREATE TABLE IF NOT EXISTS advice_labels (
	  id             INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind           TEXT NOT NULL,
	  prompt_version TEXT NOT NULL,
	  features_hash  TEXT NOT NULL,
	  features_json  TEXT NOT NULL DEFAULT '{}',
	  model_id       TEXT NOT NULL,
	  rung           TEXT NOT NULL,
	  decision       INTEGER NOT NULL,
	  confidence     INTEGER NOT NULL,
	  shown          INTEGER NOT NULL,
	  user_action    TEXT NOT NULL DEFAULT 'ignored',
	  latency_ms     INTEGER NOT NULL DEFAULT 0,
	  session_id     TEXT NOT NULL,
	  created_at     INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_advice_labels_session_kind_hash
	  ON advice_labels(session_id, kind, features_hash);

	CREATE INDEX IF NOT EXISTS idx_advice_labels_kind_created_at
	  ON advice_labels(kind, created_at DESC);
`

// Migrations returns the migration set owned by the laya-advisors
// mission.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDAdviceLabelsInit,
			Version:       1600,
			OwningMission: MigrationOwner,
			UpSource:      sqlAdviceLabelsInit,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitAdviceLabelsSQL(sqlAdviceLabelsInit) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is best-effort: drops the table and its indexes. Losing
			// captured labels on rollback is the accepted posture for a
			// purely observational training corpus (spec §4: local-only,
			// nothing else in the product depends on it for correctness) —
			// mirrors core/policy/cedar's Down.
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_advice_labels_kind_created_at",
					"DROP INDEX IF EXISTS idx_advice_labels_session_kind_hash",
					"DROP TABLE IF EXISTS advice_labels",
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
// with reg. Callers must call this before storage.Open applies pending
// migrations — mirrors every other mission's RegisterMigrations
// (core/storage/sqlite/sqlite.go's Open wires these in one place).
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitAdviceLabelsSQL is a tiny semicolon splitter mirroring
// core/policy/cedar/migrations.go's splitPolicyDecisionsSQL — the DDL
// above contains no quoted semicolons, so a literal split is sufficient.
func splitAdviceLabelsSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
