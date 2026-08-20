package trust

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// OwningMission is the block name registered in
// core/storage/migrations.CanonicalBlocks under which this package's
// migrations are versioned. Per the bundle-download-and-verify-01PMZ909
// mission's migration allocation, the trust-anchors table is versioned
// under the "bundle" block (700-799) — this feature (a persisted
// AnchorStore for signature verification) is scoped and shipped inside
// that mission even though the code lives in core/trust, not
// core/bundle. There is no separate "trust" block reserved; do not add
// one — see CanonicalBlocks in core/storage/migrations/blocks.go.
const OwningMission = "bundle"

const (
	// migrationIDTrustAnchorsInit identifies migration 700 — the
	// initial trust_anchors table landing (UNIT-3).
	migrationIDTrustAnchorsInit = "bundle/700-trust-anchors-init"
)

// sqlTrustAnchorsSchema is the DDL for migration 700.
//
// One row per Anchor (core/trust/types.go). kind is stored as the
// int(AnchorKind) it already is in memory (no CHECK enum — AnchorKind
// is a closed Go type, and a CHECK here would need updating in lockstep
// with an unexported const block change no migration test would catch).
// pubkey_fingerprint and prevkey_fingerprint are both indexed because
// FindByKeyID (the verify pipeline's hot path, verify.go step 4) must
// resolve either the current or the previous (rotation-overlap) key.
// removed is the tombstone bit — Install/Update always write 0;
// Tombstone flips it to 1 without deleting the row, preserving the
// "removed but resolvable" semantics AnchorStore.FindByKeyID promises
// (verify.go step 4 distinguishes RejAnchorRemoved from
// RejAnchorMissing on exactly this).
const sqlTrustAnchorsSchema = `
	CREATE TABLE IF NOT EXISTS trust_anchors (
		anchor_id           TEXT PRIMARY KEY,
		kind                INTEGER NOT NULL DEFAULT 0,
		org_id              TEXT NOT NULL DEFAULT '',
		peer_id             TEXT NOT NULL DEFAULT '',
		algorithm           TEXT NOT NULL DEFAULT '',
		installed_at        INTEGER NOT NULL,
		installed_by        TEXT NOT NULL DEFAULT '',
		metadata            TEXT NOT NULL DEFAULT '{}',
		pubkey_algorithm    TEXT NOT NULL DEFAULT '',
		pubkey_bytes        BLOB NOT NULL,
		pubkey_fingerprint  TEXT NOT NULL DEFAULT '',
		prevkey_algorithm   TEXT,
		prevkey_bytes       BLOB,
		prevkey_fingerprint TEXT,
		overlap_ends        INTEGER,
		removed             INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_trust_anchors_pubkey_fingerprint
		ON trust_anchors (pubkey_fingerprint);

	CREATE INDEX IF NOT EXISTS idx_trust_anchors_prevkey_fingerprint
		ON trust_anchors (prevkey_fingerprint);

	CREATE INDEX IF NOT EXISTS idx_trust_anchors_peer_id
		ON trust_anchors (peer_id, removed);
`

// Migrations returns the migration set that owns the trust_anchors
// schema.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDTrustAnchorsInit,
			Version:       700,
			OwningMission: OwningMission,
			UpSource:      sqlTrustAnchorsSchema,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitTrustSQL(sqlTrustAnchorsSchema) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// CREATE TABLE / CREATE INDEX only (AC-PI-3): a fresh
			// install and an upgraded install both simply gain the
			// table. Down is best-effort for local rollback testing;
			// operators downgrading past 700 restore from backup, same
			// convention as core/units's migration 1100.
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_trust_anchors_peer_id",
					"DROP INDEX IF EXISTS idx_trust_anchors_prevkey_fingerprint",
					"DROP INDEX IF EXISTS idx_trust_anchors_pubkey_fingerprint",
					"DROP TABLE IF EXISTS trust_anchors",
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
// into the given registry. Callers MUST register before storage.Open
// applies pending migrations — see core/storage/sqlite/sqlite.go, which
// registers this alongside session/slashcmd/units.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitTrustSQL is a tiny semicolon splitter — the DDL above contains no
// quoted semicolons, so a literal split is sufficient. Mirrors
// core/units's splitUnitSQL / core/session's splitSQL (each package
// keeps its own tiny copy rather than sharing one across a migration
// boundary that must stay independently auditable).
func splitTrustSQL(src string) []string {
	out := make([]string, 0, 8)
	for _, stmt := range strings.Split(src, ";") {
		s := strings.TrimSpace(stmt)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
