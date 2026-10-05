package units

// migration_artifacts_to_units.go — units/1104-artifacts-to-units
// (artifacts-as-units-01DOGF0C WP04, spec FR-3; decision record
// docs/missions/artifacts-as-units.md D1-D6).
//
// Copies every row of the sessions-DB `artifacts` / `artifact_versions`
// tables into `units` (kind='artifact') / `unit_versions`, verifies the
// copy row-by-row inside the same transaction, then RENAMES the legacy
// tables to `artifacts_legacy` / `artifact_versions_legacy`. Nothing is
// dropped: dropping in-place is how sessions/0327 and sessions/0332
// destroyed artifact_versions (CLAUDE.md blind spot #3). The drop is a
// separate migration in the NEXT release.
//
// WHY THE UNITS BLOCK (decision record D5). The runner applies pending
// migrations in ascending version order across all blocks. A sessions/03xx
// copy would run BEFORE units/1100-init on a fresh install, when `units`
// does not exist. 1104 runs after every sessions migration in every apply
// pass, so it always sees the final artifacts schema and post-dedupe
// session_messages ids, whatever ids the sibling missions' sessions
// migrations take.
//
// ALL OR NOTHING. Any verification mismatch returns an error; the runner's
// write transaction rolls back the copy AND the renames, no ledger row is
// written, and Open fails loudly rather than booting on a half-moved
// library. Ids are preserved (D2); a pre-existing unit with an artifact's
// id aborts before anything is written.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationIDArtifactsToUnits identifies migration 1104. Exported so the
// populated-snapshot tests in core/storage/sqlite reference the exact ID
// string (the I14 destructive-migration gate keys on it).
const MigrationIDArtifactsToUnits = "units/1104-artifacts-to-units"

// sqlArtifactsToUnitsCopy is the copy half of 1104 (decision record D1).
//
//   - units: one row per artifact; scope_id derived from the scope;
//     classification 'personal' (never fleet-synced, D3); load_policy
//     'on_demand'; empty body (bytes stay in the media CAS); version = the
//     newest revision, or 1 when a v1 is synthesized below.
//   - unit_versions: one row per artifact_versions row whose parent exists.
//   - synthesized v1 for every artifact with no version rows — the
//     sessions/0327 damage is indistinguishable from "never revised" (D4).
//   - expression indexes so media GC's per-file refcount probe and the
//     session filter are index lookups, not table scans (D8).
const sqlArtifactsToUnitsCopy = `
    INSERT INTO units
        (id, kind, scope, scope_id, classification, version, load_policy,
         title, body, metadata, created_at, updated_at)
    SELECT a.id, 'artifact', a.scope_kind,
           CASE a.scope_kind
               WHEN 'session' THEN COALESCE(a.session_id, '')
               WHEN 'project' THEN COALESCE(a.project_id, '')
               ELSE '' END,
           'personal',
           COALESCE((SELECT MAX(v.version) FROM artifact_versions v WHERE v.artifact_id = a.id), 1),
           'on_demand',
           a.title,
           '',
           json_object(
               'content_hash', a.content_hash,
               'byte_size', a.byte_size,
               'mime_type', a.mime_type,
               'source', a.source,
               'source_ref', CASE WHEN json_valid(a.source_ref_json) AND json_type(a.source_ref_json) = 'object'
                                  THEN json(a.source_ref_json) END,
               'source_ref_raw', CASE WHEN json_valid(a.source_ref_json) AND json_type(a.source_ref_json) = 'object'
                                      THEN NULL ELSE a.source_ref_json END,
               'session_id', a.session_id,
               'project_id', a.project_id,
               'legacy_artifact_id', a.id),
           a.created_at,
           COALESCE((SELECT MAX(v.created_at) FROM artifact_versions v WHERE v.artifact_id = a.id), a.created_at)
      FROM artifacts a;

    INSERT INTO unit_versions (unit_id, version, body, metadata, created_at)
    SELECT v.artifact_id, v.version, '',
           json_object(
               'content_hash', v.content_hash,
               'byte_size', v.byte_size,
               'mime_type', v.mime_type,
               'summary', v.summary,
               'path', v.path,
               'legacy_version_id', v.id),
           v.created_at
      FROM artifact_versions v
     WHERE v.artifact_id IN (SELECT id FROM artifacts);

    INSERT INTO unit_versions (unit_id, version, body, metadata, created_at)
    SELECT a.id, 1, '',
           json_object(
               'content_hash', a.content_hash,
               'byte_size', a.byte_size,
               'mime_type', a.mime_type,
               'summary', NULL,
               'path', NULL,
               'synthesized', json('true')),
           a.created_at
      FROM artifacts a
     WHERE NOT EXISTS (SELECT 1 FROM artifact_versions v WHERE v.artifact_id = a.id);

    CREATE INDEX IF NOT EXISTS idx_units_meta_content_hash
        ON units (json_extract(metadata, '$.content_hash'));

    CREATE INDEX IF NOT EXISTS idx_unit_versions_meta_content_hash
        ON unit_versions (json_extract(metadata, '$.content_hash'));

    CREATE INDEX IF NOT EXISTS idx_units_meta_session
        ON units (json_extract(metadata, '$.session_id'));
`

// sqlArtifactsToUnitsVerify holds the verification probes, in order:
// id collisions (must be 0, run BEFORE the copy), then the expected and
// matched counts for parents, real versions and synthesized versions.
// Kept as SQL text so it is part of UpSource (and so of the ledger hash).
const (
	sqlArtifactsToUnitsCollisions = `SELECT COUNT(*) FROM artifacts a JOIN units u ON u.id = a.id`

	// sqlArtifactsToUnitsBadMetadata finds a pre-existing units /
	// unit_versions row whose metadata is not valid JSON. The expression
	// indexes this migration adds evaluate json_extract(metadata, ...) on
	// EVERY row — one malformed row would fail the CREATE INDEX with an
	// opaque "malformed JSON" (and, had the index existed, every later write
	// to that row). Probed first so the abort names the row (review F6).
	sqlArtifactsToUnitsBadMetadata = `
    SELECT 'units ' || id FROM units WHERE NOT json_valid(metadata)
    UNION ALL
    SELECT 'unit_versions ' || id || ' (unit ' || unit_id || ')' FROM unit_versions WHERE NOT json_valid(metadata)
    LIMIT 1`

	sqlArtifactsToUnitsParentsWant = `SELECT COUNT(*) FROM artifacts`
	sqlArtifactsToUnitsParentsGot  = `
    SELECT COUNT(*) FROM artifacts a JOIN units u ON u.id = a.id
     WHERE u.kind = 'artifact'
       AND u.scope = a.scope_kind
       AND u.title = a.title
       AND u.created_at = a.created_at
       AND json_extract(u.metadata, '$.content_hash') = a.content_hash
       AND json_extract(u.metadata, '$.byte_size') = a.byte_size
       AND json_extract(u.metadata, '$.mime_type') = a.mime_type
       AND json_extract(u.metadata, '$.source') = a.source`

	sqlArtifactsToUnitsVersionsWant = `
    SELECT COUNT(*) FROM artifact_versions v WHERE v.artifact_id IN (SELECT id FROM artifacts)`
	sqlArtifactsToUnitsVersionsGot = `
    SELECT COUNT(*) FROM artifact_versions v
      JOIN unit_versions uv ON uv.unit_id = v.artifact_id AND uv.version = v.version
     WHERE json_extract(uv.metadata, '$.content_hash') = v.content_hash
       AND json_extract(uv.metadata, '$.byte_size') = v.byte_size
       AND uv.created_at = v.created_at`

	sqlArtifactsToUnitsSynthWant = `
    SELECT COUNT(*) FROM artifacts a
     WHERE NOT EXISTS (SELECT 1 FROM artifact_versions v WHERE v.artifact_id = a.id)`
	sqlArtifactsToUnitsSynthGot = `
    SELECT COUNT(*) FROM unit_versions uv JOIN artifacts a ON a.id = uv.unit_id
     WHERE uv.version = 1
       AND json_extract(uv.metadata, '$.synthesized') = 1
       AND json_extract(uv.metadata, '$.content_hash') = a.content_hash`

	sqlArtifactsToUnitsHistoryTotal = `
    SELECT COUNT(*) FROM unit_versions uv JOIN units u ON u.id = uv.unit_id
     WHERE u.kind = 'artifact' AND u.id IN (SELECT id FROM artifacts)`
)

// sqlArtifactsToUnitsRename retires the legacy tables WITHOUT dropping
// them. legacy_alter_table is off (SQLite default), so renaming
// `artifacts` rewrites artifact_versions' FK to the new name; no DROP
// means no implicit DELETE and no cascade.
const sqlArtifactsToUnitsRename = `
    ALTER TABLE artifact_versions RENAME TO artifact_versions_legacy;
    ALTER TABLE artifacts RENAME TO artifacts_legacy;
`

func migration1104() migrations.Migration {
	return migrations.Migration{
		ID:            MigrationIDArtifactsToUnits,
		Version:       1104,
		OwningMission: OwningMission,
		UpSource: sqlArtifactsToUnitsBadMetadata + ";\n" + sqlArtifactsToUnitsCollisions + ";\n" + sqlArtifactsToUnitsCopy +
			sqlArtifactsToUnitsParentsWant + ";\n" + sqlArtifactsToUnitsParentsGot + ";\n" +
			sqlArtifactsToUnitsVersionsWant + ";\n" + sqlArtifactsToUnitsVersionsGot + ";\n" +
			sqlArtifactsToUnitsSynthWant + ";\n" + sqlArtifactsToUnitsSynthGot + ";\n" +
			sqlArtifactsToUnitsHistoryTotal + ";\n" + sqlArtifactsToUnitsRename,
		Up:   upArtifactsToUnits,
		Down: downArtifactsToUnits,
	}
}

// sqlArtifactsToUnitsDown restores the pre-1104 shape: the legacy tables
// get their names back and the units COPIED from them are removed (with
// their history). Artifact units captured AFTER the upgrade are left in
// place — they have no legacy row to fall back to, and deleting a user's
// data on a schema rollback would be worse than leaving inert rows the
// pre-1104 code never reads. Registry.Rollback has no production caller
// (see scripts/ci/check-destructive-migration-coverage.sh); this exists
// so the rollback path for the units block keeps working in tests.
const sqlArtifactsToUnitsDown = `
    ALTER TABLE artifacts_legacy RENAME TO artifacts;
    ALTER TABLE artifact_versions_legacy RENAME TO artifact_versions;
    DELETE FROM unit_versions WHERE unit_id IN (SELECT id FROM artifacts)
        AND unit_id IN (SELECT id FROM units WHERE kind = 'artifact');
    DELETE FROM unit_edges WHERE from_id IN (SELECT id FROM units WHERE kind = 'artifact' AND id IN (SELECT id FROM artifacts))
        OR to_id IN (SELECT id FROM units WHERE kind = 'artifact' AND id IN (SELECT id FROM artifacts));
    DELETE FROM unit_sync_state WHERE unit_id IN (SELECT id FROM units WHERE kind = 'artifact' AND id IN (SELECT id FROM artifacts));
    DELETE FROM units WHERE kind = 'artifact' AND id IN (SELECT id FROM artifacts);
    DROP INDEX IF EXISTS idx_units_meta_content_hash;
    DROP INDEX IF EXISTS idx_unit_versions_meta_content_hash;
    DROP INDEX IF EXISTS idx_units_meta_session;
`

func downArtifactsToUnits(ctx context.Context, tx migrations.WriteTx) error {
	for _, stmt := range splitUnitSQL(sqlArtifactsToUnitsDown) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: down: %w", MigrationIDArtifactsToUnits, err)
		}
	}
	return nil
}

func upArtifactsToUnits(ctx context.Context, tx migrations.WriteTx) error {
	count := func(label, q string) (int64, error) {
		var n int64
		if err := tx.QueryRow(ctx, q).Scan(&n); err != nil {
			return 0, fmt.Errorf("%s: %w", label, err)
		}
		return n, nil
	}

	var bad string
	switch err := tx.QueryRow(ctx, sqlArtifactsToUnitsBadMetadata).Scan(&bad); {
	case err == nil:
		return fmt.Errorf("%s: %s has metadata that is not valid JSON; repair or remove that row before upgrading "+
			"(the migration's json_extract indexes cannot be built over it) — aborting, nothing written",
			MigrationIDArtifactsToUnits, bad)
	case errors.Is(err, sql.ErrNoRows):
		// every existing metadata value parses
	default:
		return fmt.Errorf("%s: metadata validity probe: %w", MigrationIDArtifactsToUnits, err)
	}

	collisions, err := count("id collision probe", sqlArtifactsToUnitsCollisions)
	if err != nil {
		return err
	}
	if collisions != 0 {
		return fmt.Errorf("%s: %d artifact id(s) already exist as unit ids; refusing to merge (ids must be preserved)",
			MigrationIDArtifactsToUnits, collisions)
	}

	for _, stmt := range splitUnitSQL(sqlArtifactsToUnitsCopy) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: copy: %w", MigrationIDArtifactsToUnits, err)
		}
	}

	checks := []struct {
		label     string
		want, got string
	}{
		{"artifacts → units (id, scope, title, created_at, hash, size, mime, source)", sqlArtifactsToUnitsParentsWant, sqlArtifactsToUnitsParentsGot},
		{"artifact_versions → unit_versions (version, hash, size, created_at)", sqlArtifactsToUnitsVersionsWant, sqlArtifactsToUnitsVersionsGot},
		{"synthesized v1 for version-less artifacts", sqlArtifactsToUnitsSynthWant, sqlArtifactsToUnitsSynthGot},
	}
	var wantHistory int64
	for _, c := range checks {
		want, err := count(c.label+" (want)", c.want)
		if err != nil {
			return err
		}
		got, err := count(c.label+" (got)", c.got)
		if err != nil {
			return err
		}
		if want != got {
			return fmt.Errorf("%s: verification failed for %s: %d source rows, %d matching copies — aborting, nothing written",
				MigrationIDArtifactsToUnits, c.label, want, got)
		}
		if c.label != checks[0].label {
			wantHistory += want
		}
	}
	history, err := count("artifact unit history total", sqlArtifactsToUnitsHistoryTotal)
	if err != nil {
		return err
	}
	if history != wantHistory {
		return fmt.Errorf("%s: verification failed: %d unit_versions rows for migrated artifacts, want exactly %d",
			MigrationIDArtifactsToUnits, history, wantHistory)
	}

	for _, stmt := range splitUnitSQL(sqlArtifactsToUnitsRename) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: rename legacy tables: %w", MigrationIDArtifactsToUnits, err)
		}
	}
	return nil
}
