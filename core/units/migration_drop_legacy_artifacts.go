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
//  1. 1104's latest ledger row must be 'applied'.
//  2. No legacy artifact id may be held by a unit of another kind (the id
//     was squatted after the copy — the legacy row is then the only
//     artifact with that id).
//  3. For every legacy artifact whose kind='artifact' unit exists, every
//     legacy version row must have its unit_versions twin with the same
//     (unit id, version, content hash). Unit versions are append-only and
//     are only ever removed together with their unit, so a missing twin
//     means the copy is damaged.
//
// Any failure returns ErrLegacyArtifactsUnverified: the runner rolls the
// transaction back, no ledger row is written, both legacy tables survive
// intact, and Open fails closed.
//
// DELIBERATE DEVIATION from FR-2's literal wording ("count(artifacts_legacy)
// == count(units WHERE kind='artifact') for the same IDs"). A legacy row
// whose unit is ABSENT is not evidence of a missing copy: in v0.87.0 every
// artifact delete and every session/project purge (C spec FR-6) removes the
// artifact UNIT and leaves its legacy row behind (artifacts_legacy's
// session FK is ON DELETE SET NULL). Refusing on that count would make Open
// fail on every install whose user deleted an artifact or a session with
// artifacts during the retention release — the v0.63.0 failure class. Such
// rows are copies of data the user deleted; they are counted and logged,
// then dropped with the table. The equality FR-2 asks for still holds for
// every id the units table holds at all (check 2), and a 1104 that never
// ran is caught by check 1 (and, before that, by the runner re-applying
// 1104, which fails on a database without the pre-1104 table names).

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

	sqlDropLegacyVersionsWant = `
    SELECT COUNT(*) FROM artifact_versions_legacy v
      JOIN units u ON u.id = v.artifact_id AND u.kind = 'artifact'`
	sqlDropLegacyVersionsGot = `
    SELECT COUNT(*) FROM artifact_versions_legacy v
      JOIN units u ON u.id = v.artifact_id AND u.kind = 'artifact'
      JOIN unit_versions uv ON uv.unit_id = v.artifact_id AND uv.version = v.version
     WHERE json_extract(uv.metadata, '$.content_hash') = v.content_hash`
	sqlDropLegacyVersionsMissing = `
    SELECT v.artifact_id || ' v' || v.version FROM artifact_versions_legacy v
      JOIN units u ON u.id = v.artifact_id AND u.kind = 'artifact'
     WHERE NOT EXISTS (SELECT 1 FROM unit_versions uv
                        WHERE uv.unit_id = v.artifact_id AND uv.version = v.version
                          AND json_extract(uv.metadata, '$.content_hash') = v.content_hash)
     ORDER BY v.artifact_id, v.version LIMIT 1`
)

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
		UpSource: sqlDropLegacyTableExists + ";\n" + sqlDropLegacy1104Action + ";\n" +
			sqlDropLegacyParents + ";\n" + sqlDropLegacyMatched + ";\n" + sqlDropLegacySquatted + ";\n" +
			sqlDropLegacyVersionsWant + ";\n" + sqlDropLegacyVersionsGot + ";\n" + sqlDropLegacyVersionsMissing + ";\n" +
			sqlDropArtifactsLegacy,
		Up:   upDropArtifactsLegacy,
		Down: downDropArtifactsLegacy,
	}
}

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

	var parents, matched int64
	if hasParents == 1 {
		// Check 2: no legacy id squatted by a non-artifact unit.
		if squat, found, err := firstRow("squatted-id probe", sqlDropLegacySquatted); err != nil {
			return err
		} else if found {
			return refuse("legacy artifact %s — its id is held by a unit that is not an artifact", squat)
		}
		if parents, err = count("legacy parents", sqlDropLegacyParents); err != nil {
			return err
		}
		if matched, err = count("legacy parents with an artifact unit", sqlDropLegacyMatched); err != nil {
			return err
		}
	}
	if hasParents == 1 && hasVersions == 1 {
		// Check 3: every version of every still-present artifact has its twin.
		want, err := count("legacy versions of present artifacts", sqlDropLegacyVersionsWant)
		if err != nil {
			return err
		}
		got, err := count("legacy versions with a matching unit version", sqlDropLegacyVersionsGot)
		if err != nil {
			return err
		}
		if want != got {
			first, _, _ := firstRow("missing-version probe", sqlDropLegacyVersionsMissing)
			return refuse("%d legacy version row(s) of present artifacts, %d with a matching unit_versions row (first missing: %s)",
				want, got, first)
		}
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
		"deleted_since_copy", parents-matched)
	return nil
}
