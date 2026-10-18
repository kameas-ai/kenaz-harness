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
//   - features_complete (review promotion, laya-advisors-01LAYA001 WP07/
//     WP08 review round, 2026-09-29): the placeholder-row discriminator.
//     Without this, a compact_now/escalate_model row built from
//     advice_hook.go's documented zero-value placeholder Snapshot (no
//     ChatRunner-visible accessor for real token-span/tool-failure data
//     exists yet) is byte-for-byte indistinguishable from a row where the
//     model genuinely saw no signal — training on the two as if they were
//     the same thing would teach a future fine-tune that "no data wired"
//     means "nothing happening." DEFAULT 1 (true, complete) is the safe
//     default for every kind that does not opt into reporting
//     incompleteness (branch_now's decision-bearing fields are all real
//     data, so it never sets this false). Set by CaptureAdvisor.Recommend
//     (capture.go) via a type assertion against the kind-owned
//     advice.FeaturesCompleteness interface — the bridge never
//     hardcodes a kind id to decide this; each kind's own Features type
//     (compactnow.Features, escalatemodel.Features) is the "justify
//     site" that knows whether ITS OWN Snapshot input was placeholder
//     data, via a FeaturesIncomplete field advice_hook.go sets when
//     building that kind's Snapshot.
//
//     This migration has not shipped in any release yet (WP08 landed on
//     an unreleased branch), so the column is added in place — amending
//     sqlAdviceLabelsInit directly, not a second migration — per the
//     migration-immutability rule's own precondition ("once applied
//     anywhere"), which does not yet hold here.
const sqlAdviceLabelsInit = `
	CREATE TABLE IF NOT EXISTS advice_labels (
	  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind               TEXT NOT NULL,
	  prompt_version     TEXT NOT NULL,
	  features_hash      TEXT NOT NULL,
	  features_json      TEXT NOT NULL DEFAULT '{}',
	  features_complete  INTEGER NOT NULL DEFAULT 1,
	  model_id           TEXT NOT NULL,
	  rung               TEXT NOT NULL,
	  decision           INTEGER NOT NULL,
	  confidence         INTEGER NOT NULL,
	  shown              INTEGER NOT NULL,
	  user_action        TEXT NOT NULL DEFAULT 'ignored',
	  latency_ms         INTEGER NOT NULL DEFAULT 0,
	  session_id         TEXT NOT NULL,
	  created_at         INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_advice_labels_session_kind_hash
	  ON advice_labels(session_id, kind, features_hash);

	CREATE INDEX IF NOT EXISTS idx_advice_labels_kind_created_at
	  ON advice_labels(kind, created_at DESC);
`

// migrationIDAdviceLabelsRevision is the stable migration ID for the
// label push lane's revision column + push-cursor table (version 1601).
const migrationIDAdviceLabelsRevision = "laya-advisors/1601-advice-labels-revision"

// sqlAdviceLabelsRevision is the DDL for migration 1601
// (laya-advisors-01LAYA001 WP14, design Amendment A3.3 "label ingest
// contract frozen: rows carry a monotonic revision").
//
// WHY A MIGRATION (derivation was exhausted first): advice_labels has no
// updated-at / action-version column, and UpdateAction rewrites
// user_action in place, leaving NO trace that a row changed. A revision
// cannot be derived from id/created_at (constant per row) nor from
// user_action alone (accepted -> dismissed would not be monotonic), and
// without a change marker the push lane cannot even FIND a row whose
// action changed after its first push. 1600 shipped in v0.84.0, so it
// cannot be amended in place.
//
// revision is a TABLE-GLOBAL monotonic change counter: an insert takes
// MAX(revision)+1 and an action change takes MAX(revision)+1 again, so
// every row's revision strictly increases each time it changes AND an
// updated old row sorts AFTER every already-pushed row. That is what
// makes the frozen "ack = cursor over (ts, revision)" work with one
// durable cursor per kind: ordering by revision alone is total, ts rides
// along on the wire. Existing rows backfill revision = id (insertion
// order == id order, so the counter stays monotonic).
//
// advice_label_push_cursor is the durable, per-(sink, kind) ack cursor.
// sink names the consumer ("sidecar" today) so a later export lane can
// keep its own cursor without a schema change. The harness DB stays the
// source of truth: deleting this table's rows is a full re-push from
// zero (mirror loss).
//
// Purely additive (ALTER ADD COLUMN, UPDATE of the new column only,
// CREATE ... IF NOT EXISTS) — nothing destructive in Up.
const sqlAdviceLabelsRevision = `
	ALTER TABLE advice_labels ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

	UPDATE advice_labels SET revision = id;

	CREATE INDEX IF NOT EXISTS idx_advice_labels_revision
	  ON advice_labels(revision);

	CREATE INDEX IF NOT EXISTS idx_advice_labels_kind_revision
	  ON advice_labels(kind, revision);

	CREATE TABLE IF NOT EXISTS advice_label_push_cursor (
	  sink             TEXT NOT NULL,
	  kind             TEXT NOT NULL,
	  cursor_ts        INTEGER NOT NULL DEFAULT 0,
	  cursor_revision  INTEGER NOT NULL DEFAULT 0,
	  updated_at       INTEGER NOT NULL DEFAULT 0,
	  PRIMARY KEY (sink, kind)
	);
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
		{
			ID:            migrationIDAdviceLabelsRevision,
			Version:       1601,
			OwningMission: MigrationOwner,
			UpSource:      sqlAdviceLabelsRevision,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitAdviceLabelsSQL(sqlAdviceLabelsRevision) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is best-effort, same posture as 1600's: the cursor and
			// the revision column are rebuildable push bookkeeping.
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP TABLE IF EXISTS advice_label_push_cursor",
					"DROP INDEX IF EXISTS idx_advice_labels_kind_revision",
					"DROP INDEX IF EXISTS idx_advice_labels_revision",
					"ALTER TABLE advice_labels DROP COLUMN revision",
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
