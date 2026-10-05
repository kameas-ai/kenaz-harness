package units

// migration_drop_legacy_artifacts.go — units/1105-drop-artifacts-legacy
// (units-debt-01UNITD01 WP02, spec FR-1/FR-2; the dated follow-up
// artifacts-as-units-01DOGF0C filed in docs/unwired-ledger.md).
//
// units/1104 (v0.87.0) copied `artifacts` / `artifact_versions` onto
// kind='artifact' units and RENAMED the originals to `artifacts_legacy` /
// `artifact_versions_legacy`, retained read-only for exactly one release
// (C spec FR-8 step 3: "dropped by a later migration in the next release —
// never in the same migration that copies"). This is that migration.
//
// GUARDED. Each DROP is IF EXISTS: a database where the tables are already
// gone (re-open after a ledger rewind, a hand-repaired install) applies
// cleanly. Nothing else references the legacy tables — the legacy store
// was deleted with the 1104 store switch — and the child table is dropped
// before the parent so the parent's implicit DELETE has nothing to cascade
// into.
//
// VERIFY BEFORE DROPPING (FR-2). The legacy tables may be the only copy of
// an artifact if 1104 never ran or its copy was damaged. Before any DROP,
// inside the same transaction:
//
//  0. Both legacy tables exist, or neither does. 1104 renames them in one
//     transaction, so a lone table is damage.
//  1. 1104's latest ledger row must be 'applied'. Reachable only through
//     ledger surgery: whenever 1104 is not applied the runner re-runs 1104
//     first (version order), which fails on a database without the
//     pre-1104 table names.
//  2. No legacy artifact id may be held by a unit of another kind (the id
//     was squatted after the copy — the legacy row is then the only
//     artifact with that id).
//  3. For every legacy artifact whose kind='artifact' unit exists, every
//     legacy version row must have its unit_versions twin with the same
//     (unit id, version, content hash, byte size, created_at), and a
//     version-less legacy artifact must have its synthesized v1 twin
//     (content hash, byte size, created_at of the parent row) — 1104's
//     VersionsGot / SynthGot probes, re-run. Unit versions are append-only
//     and are only ever removed together with their unit, so a missing or
//     different twin means the copy is damaged.
//
// Any failure returns ErrLegacyArtifactsUnverified: the runner rolls the
// transaction back, no ledger row is written, both legacy tables survive
// intact, and Open fails closed.
//
// ORPHANS ARE QUARANTINED, NOT DROPPED (and not refused). A legacy row whose
// unit is ABSENT is ambiguous: in v0.87.0 every artifact delete and every
// session/project purge (C spec FR-6) removes the artifact UNIT and leaves
// its legacy row behind (artifacts_legacy's session FK is ON DELETE SET
// NULL), but a bug or tamper that deleted units would look the same.
// Refusing would make Open fail for every user who deleted an artifact (or
// all of them) during the retention release — the v0.63.0 failure class —
// so this DEVIATES from FR-2's literal count equality. Dropping would make
// a units-side loss unrecoverable. So each orphaned row (with its legacy
// versions, as JSON) is moved into `artifacts_legacy_orphans` in the same
// transaction and logged at WARN (id, content hash, title); then the big
// tables are dropped. The quarantine table is tiny and bounded: it holds
// only rows orphaned at drop time, nothing ever writes it again, and no
// code reads it — it exists so a lost artifact can be recovered by hand.
// It does not keep the legacy tables alive, so the drop contract's purpose
// (no second artifact store) holds.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationIDDropArtifactsLegacy identifies migration 1105. Exported so
// the populated-snapshot tests in core/storage/sqlite key on the exact ID
// string (the I14 destructive-migration gate looks for it in a _test.go).
const MigrationIDDropArtifactsLegacy = "units/1105-drop-artifacts-legacy"

// ErrLegacyArtifactsUnverified is returned (wrapped, with the migration id
// and the failing check) when the legacy artifact tables cannot be proven
// redundant with their units copy. Nothing is dropped.
var ErrLegacyArtifactsUnverified = errors.New("legacy artifact tables are not verifiably copied onto units; refusing to drop them")

const (
	sqlDropLegacyTableExists = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`

	sqlDropLegacy1104Action = `
    SELECT action FROM harness_migrations WHERE id = ? ORDER BY rowid DESC LIMIT 1`

	sqlDropLegacyParents  = `SELECT COUNT(*) FROM artifacts_legacy`
	sqlDropLegacyMatched  = `SELECT COUNT(*) FROM artifacts_legacy l JOIN units u ON u.id = l.id WHERE u.kind = 'artifact'`
	sqlDropLegacySquatted = `
    SELECT l.id || ' (now a ' || u.kind || ' unit)' FROM artifacts_legacy l JOIN units u ON u.id = l.id
     WHERE u.kind != 'artifact' ORDER BY l.id LIMIT 1`

	// Real versions: every legacy version of a present artifact unit has
	// its twin (1104's VersionsGot predicate).
	sqlDropLegacyVersionsMissing = `
    SELECT v.artifact_id || ' v' || v.version FROM artifact_versions_legacy v
      JOIN units u ON u.id = v.artifact_id AND u.kind = 'artifact'
     WHERE NOT EXISTS (SELECT 1 FROM unit_versions uv
                        WHERE uv.unit_id = v.artifact_id AND uv.version = v.version
                          AND json_extract(uv.metadata, '$.content_hash') = v.content_hash
                          AND json_extract(uv.metadata, '$.byte_size') = v.byte_size
                          AND uv.created_at = v.created_at)
     ORDER BY v.artifact_id, v.version LIMIT 1`

	// Synthesized v1: every version-less legacy artifact whose unit is
	// present has the v1 1104 synthesized from the parent row (1104's
	// SynthGot predicate, plus size and time).
	sqlDropLegacySynthMissing = `
    SELECT l.id || ' synthesized v1' FROM artifacts_legacy l
      JOIN units u ON u.id = l.id AND u.kind = 'artifact'
     WHERE NOT EXISTS (SELECT 1 FROM artifact_versions_legacy v WHERE v.artifact_id = l.id)
       AND NOT EXISTS (SELECT 1 FROM unit_versions uv
                        WHERE uv.unit_id = l.id AND uv.version = 1
                          AND json_extract(uv.metadata, '$.synthesized') = 1
                          AND json_extract(uv.metadata, '$.content_hash') = l.content_hash
                          AND json_extract(uv.metadata, '$.byte_size') = l.byte_size
                          AND uv.created_at = l.created_at)
     ORDER BY l.id LIMIT 1`

	// Orphans: legacy rows with no unit at all (squatted ids refused above).
	sqlDropLegacyOrphans = `
    SELECT l.id, l.content_hash, l.title FROM artifacts_legacy l
     WHERE NOT EXISTS (SELECT 1 FROM units u WHERE u.id = l.id) ORDER BY l.id`
)

// sqlCreateLegacyOrphans is the quarantine table (see the file header).
// Created on every install that reaches 1105, so the schema does not depend
// on whether the legacy tables were still present.
const sqlCreateLegacyOrphans = `
    CREATE TABLE IF NOT EXISTS artifacts_legacy_orphans (
        id               TEXT PRIMARY KEY,
        title            TEXT NOT NULL,
        content_hash     TEXT NOT NULL,
        source_ref_json  TEXT NOT NULL,
        legacy_metadata  TEXT NOT NULL,
        quarantined_at   TEXT NOT NULL
    )`

// sqlQuarantineLegacyOrphans copies every orphaned legacy row — every
// column, plus its legacy version rows — into the quarantine table. First
// quarantine wins (ON CONFLICT DO NOTHING): nothing re-writes a row.
const sqlQuarantineLegacyOrphans = `
    INSERT INTO artifacts_legacy_orphans
        (id, title, content_hash, source_ref_json, legacy_metadata, quarantined_at)
    SELECT l.id, l.title, l.content_hash, l.source_ref_json,
           json_object(
               'dropped_from', 'artifacts_legacy',
               'migration', '` + MigrationIDDropArtifactsLegacy + `',
               'session_id', l.session_id,
               'project_id', l.project_id,
               'mime_type', l.mime_type,
               'byte_size', l.byte_size,
               'source', l.source,
               'scope_kind', l.scope_kind,
               'created_at', l.created_at,
               'versions', json((SELECT json_group_array(json_object(
                                     'version', v.version, 'content_hash', v.content_hash,
                                     'byte_size', v.byte_size, 'mime_type', v.mime_type,
                                     'summary', v.summary, 'path', v.path, 'created_at', v.created_at))
                                   FROM artifact_versions_legacy v WHERE v.artifact_id = l.id))),
           strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
      FROM artifacts_legacy l
     WHERE NOT EXISTS (SELECT 1 FROM units u WHERE u.id = l.id)
    ON CONFLICT(id) DO NOTHING`

// sqlDropArtifactsLegacy drops child before parent (no cascade can fire).
const sqlDropArtifactsLegacy = `
    DROP TABLE IF EXISTS artifact_versions_legacy;
    DROP TABLE IF EXISTS artifacts_legacy;
`

// sqlRecreateArtifactsLegacy is Down: the legacy tables' exact post-1104
// schema (captured from sqlite_master on a HEAD database), recreated
// EMPTY. Rows dropped by Up cannot be restored — operators downgrading
// past 1105 must restore a backup — but the schema lets 1104's Down run
// (rename back, remove the units it copied: none, the tables are empty).
// Registry.Rollback has no production caller (see
// scripts/ci/check-destructive-migration-coverage.sh).
const sqlRecreateArtifactsLegacy = `
    CREATE TABLE IF NOT EXISTS "artifacts_legacy" (
        id              TEXT PRIMARY KEY,
        session_id      TEXT NULL REFERENCES sessions(id) ON DELETE SET NULL,
        project_id      TEXT NULL REFERENCES projects(id) ON DELETE SET NULL,
        title           TEXT NOT NULL DEFAULT '',
        mime_type       TEXT NOT NULL,
        content_hash    TEXT NOT NULL,
        byte_size       INTEGER NOT NULL,
        source          TEXT NOT NULL CHECK (source IN ('code_block','tool_output','user_pin','model_output')),
        source_ref_json TEXT NOT NULL DEFAULT '{}',
        scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project','global')),
        created_at      INTEGER NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_artifacts_session
        ON "artifacts_legacy" (session_id, created_at DESC) WHERE session_id IS NOT NULL;
    CREATE INDEX IF NOT EXISTS idx_artifacts_project
        ON "artifacts_legacy" (project_id, created_at DESC) WHERE project_id IS NOT NULL;
    CREATE INDEX IF NOT EXISTS idx_artifacts_content_hash
        ON "artifacts_legacy" (content_hash);
    CREATE TABLE IF NOT EXISTS "artifact_versions_legacy" (
        id           INTEGER PRIMARY KEY AUTOINCREMENT,
        artifact_id  TEXT NOT NULL REFERENCES "artifacts_legacy"(id) ON DELETE CASCADE,
        version      INTEGER NOT NULL,
        content_hash TEXT NOT NULL,
        byte_size    INTEGER NOT NULL,
        mime_type    TEXT NOT NULL DEFAULT '',
        summary      TEXT,
        path         TEXT,
        created_at   INTEGER NOT NULL,
        UNIQUE (artifact_id, version)
    );
    CREATE INDEX IF NOT EXISTS idx_artifact_versions_artifact
        ON "artifact_versions_legacy" (artifact_id, version DESC);
`

func migration1105() migrations.Migration {
	return migrations.Migration{
		ID:            MigrationIDDropArtifactsLegacy,
		Version:       1105,
		OwningMission: OwningMission,
		UpSource: sqlCreateLegacyOrphans + ";\n" + sqlDropLegacyTableExists + ";\n" + sqlDropLegacy1104Action + ";\n" +
			sqlDropLegacyParents + ";\n" + sqlDropLegacyMatched + ";\n" + sqlDropLegacySquatted + ";\n" +
			sqlDropLegacyVersionsMissing + ";\n" + sqlDropLegacySynthMissing + ";\n" + sqlDropLegacyOrphans + ";\n" +
			sqlQuarantineLegacyOrphans + ";\n" + sqlDropArtifactsLegacy,
		Up:   upDropArtifactsLegacy,
		Down: downDropArtifactsLegacy,
	}
}

// downDropArtifactsLegacy recreates the legacy tables EMPTY (see
// sqlRecreateArtifactsLegacy). The quarantine table is left in place: its
// rows are the only copy of what was quarantined, and a Down must not
// destroy data.
func downDropArtifactsLegacy(ctx context.Context, tx migrations.WriteTx) error {
	for _, stmt := range splitUnitSQL(sqlRecreateArtifactsLegacy) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: down: %w", MigrationIDDropArtifactsLegacy, err)
		}
	}
	return nil
}

func upDropArtifactsLegacy(ctx context.Context, tx migrations.WriteTx) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%s: %w: %s — aborting, nothing dropped",
			MigrationIDDropArtifactsLegacy, ErrLegacyArtifactsUnverified, fmt.Sprintf(format, args...))
	}
	count := func(label, q string, args ...any) (int64, error) {
		var n int64
		if err := tx.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			return 0, fmt.Errorf("%s: %s: %w", MigrationIDDropArtifactsLegacy, label, err)
		}
		return n, nil
	}
	firstRow := func(label, q string) (string, bool, error) {
		var s string
		switch err := tx.QueryRow(ctx, q).Scan(&s); {
		case err == nil:
			return s, true, nil
		case errors.Is(err, sql.ErrNoRows):
			return "", false, nil
		default:
			return "", false, fmt.Errorf("%s: %s: %w", MigrationIDDropArtifactsLegacy, label, err)
		}
	}

	if _, err := tx.Exec(ctx, sqlCreateLegacyOrphans); err != nil {
		return fmt.Errorf("%s: create quarantine table: %w", MigrationIDDropArtifactsLegacy, err)
	}
	hasParents, err := count("probe artifacts_legacy", sqlDropLegacyTableExists, "artifacts_legacy")
	if err != nil {
		return err
	}
	hasVersions, err := count("probe artifact_versions_legacy", sqlDropLegacyTableExists, "artifact_versions_legacy")
	if err != nil {
		return err
	}
	if hasParents == 0 && hasVersions == 0 {
		return nil // already gone — nothing to verify, nothing to drop
	}
	// Check 0: 1104 renames both tables in one transaction.
	if hasParents != hasVersions {
		return refuse("only one legacy table exists (artifacts_legacy=%d, artifact_versions_legacy=%d); 1104 renames both together", hasParents, hasVersions)
	}

	// Check 1: the copy ran.
	var action string
	switch err := tx.QueryRow(ctx, sqlDropLegacy1104Action, MigrationIDArtifactsToUnits).Scan(&action); {
	case errors.Is(err, sql.ErrNoRows):
		return refuse("%s has no ledger row, so the units copy never ran", MigrationIDArtifactsToUnits)
	case err != nil:
		return fmt.Errorf("%s: 1104 ledger probe: %w", MigrationIDDropArtifactsLegacy, err)
	case action != "applied":
		return refuse("%s's latest ledger action is %q, not applied", MigrationIDArtifactsToUnits, action)
	}

	// Check 2: no legacy id squatted by a non-artifact unit.
	if squat, found, err := firstRow("squatted-id probe", sqlDropLegacySquatted); err != nil {
		return err
	} else if found {
		return refuse("legacy artifact %s — its id is held by a unit that is not an artifact", squat)
	}
	// Check 3: every legacy version (real or synthesized) of every present
	// artifact has its exact twin.
	for _, probe := range []struct{ label, q string }{
		{"missing-version probe", sqlDropLegacyVersionsMissing},
		{"missing-synthesized-v1 probe", sqlDropLegacySynthMissing},
	} {
		if missing, found, err := firstRow(probe.label, probe.q); err != nil {
			return err
		} else if found {
			return refuse("legacy %s has no matching unit_versions row (same version, content hash, byte size, created_at)", missing)
		}
	}

	parents, err := count("legacy parents", sqlDropLegacyParents)
	if err != nil {
		return err
	}
	matched, err := count("legacy parents with an artifact unit", sqlDropLegacyMatched)
	if err != nil {
		return err
	}

	// Quarantine the orphans, naming each one at WARN.
	rows, err := tx.Query(ctx, sqlDropLegacyOrphans)
	if err != nil {
		return fmt.Errorf("%s: orphan probe: %w", MigrationIDDropArtifactsLegacy, err)
	}
	type orphan struct{ id, hash, title string }
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.hash, &o.title); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%s: orphan scan: %w", MigrationIDDropArtifactsLegacy, err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%s: orphan rows: %w", MigrationIDDropArtifactsLegacy, err)
	}
	_ = rows.Close()
	if _, err := tx.Exec(ctx, sqlQuarantineLegacyOrphans); err != nil {
		return fmt.Errorf("%s: quarantine orphans: %w", MigrationIDDropArtifactsLegacy, err)
	}
	for _, o := range orphans {
		logging.L().Warn("units.drop_artifacts_legacy.orphan_quarantined",
			"migration", MigrationIDDropArtifactsLegacy,
			"artifact_id", o.id,
			"content_hash", o.hash,
			"title", o.title,
			"quarantine_table", "artifacts_legacy_orphans")
	}

	for _, stmt := range splitUnitSQL(sqlDropArtifactsLegacy) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: drop: %w", MigrationIDDropArtifactsLegacy, err)
		}
	}
	logging.L().Info("units.drop_artifacts_legacy",
		"migration", MigrationIDDropArtifactsLegacy,
		"legacy_artifacts", parents,
		"verified_against_units", matched,
		"quarantined_orphans", len(orphans))
	return nil
}
