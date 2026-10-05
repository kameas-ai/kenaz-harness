package sqlite_test

// migration_1105_test.go — populated-snapshot coverage for
// "units/1105-drop-artifacts-legacy" (units-debt-01UNITD01 WP02; spec
// FR-1, FR-2; pins P-1, P-2; WP-PI AC-PI-1/AC-PI-3).
//
// 1105 drops the *_legacy tables units/1104 retained for one release. It
// is destructive, so every test here runs it against a database a PREVIOUS
// RELEASE produced, populated, through the production storagesqlite.Open.
//
// THE v0.87.0 SHAPE. 1105's real input is a database v0.87.0 left behind:
// 1104 applied, artifacts on units, legacy tables renamed and populated.
// Two ways to get one:
//
//   - FROM A v0.87.0-ERA SNAPSHOT (TestMigration1105_V087SnapshotBoots):
//     the newest committed snapshot, when its dump already carries
//     artifacts_legacy. That snapshot did not exist when this file was
//     written (newest was v0.86.0); the test skips until it lands and then
//     runs with no edit.
//   - RECONSTRUCTED (buildV087State, every other test): the newest
//     PRE-1104 snapshot, seeded with every artifact shape, VACUUMed INTO a
//     copy (the exact pre-1104 rows), booted through production Open
//     (1104 copies + renames, 1105 drops), then 1105 is rewound — its
//     ledger row deleted and its production Down run (empty legacy tables,
//     exact post-1104 schema) — and the legacy tables are refilled from the
//     copy. The result is byte-for-byte what 1104 left: the units side is
//     the real 1104 output, the legacy side the real 1104 input.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/units"

	_ "modernc.org/sqlite"
)

const migration1105ID = "units/1105-drop-artifacts-legacy"

func TestMigration1105_IDIsPinned(t *testing.T) {
	t.Parallel()
	if units.MigrationIDDropArtifactsLegacy != migration1105ID {
		t.Fatalf("units.MigrationIDDropArtifactsLegacy = %q, want %q (the I14 gate and these tests key on the literal)",
			units.MigrationIDDropArtifactsLegacy, migration1105ID)
	}
}

// snapshotTagsByGeneration returns the newest committed snapshot whose dump
// predates 1104 (has `artifacts`, no `artifacts_legacy`), and the newest
// committed snapshot overall plus whether IT carries `artifacts_legacy`.
func snapshotTagsByGeneration(t *testing.T) (newestPre1104, newest string, newestHasLegacy bool) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "upgrade"))
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() && upgradesnap.IsSnapshotTag(e.Name()) {
			if _, err := os.Stat(filepath.Join("testdata", "upgrade", e.Name(), "dump.sql")); err == nil {
				tags = append(tags, e.Name())
			}
		}
	}
	tags = upgradesnap.SortedSnapshotTags(tags)
	if len(tags) == 0 {
		t.Fatal("no committed snapshots")
	}
	hasLegacy := func(tag string) bool {
		dump, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
		if err != nil {
			t.Fatalf("read %s dump: %v", tag, err)
		}
		return strings.Contains(string(dump), "artifacts_legacy")
	}
	newest = tags[len(tags)-1]
	newestHasLegacy = hasLegacy(newest)
	for i := len(tags) - 1; i >= 0; i-- {
		if !hasLegacy(tags[i]) {
			return tags[i], newest, newestHasLegacy
		}
	}
	t.Fatal("no pre-1104 snapshot committed")
	return "", "", false
}

// buildV087State leaves dir/data.db in the exact shape v0.87.0 leaves an
// upgraded install in (see the file comment), seeded with every artifact
// shape. afterCopy, when non-nil, runs against the production storage.DB
// between 1104's copy and the reconstruction — i.e. it acts as the user
// on v0.87.0 (deleting an artifact, purging a session). Returns the
// seeded artifacts and the pre-1104 snapshot tag used.
func buildV087State(t *testing.T, ctx context.Context, dir string, afterCopy func(db storage.DB)) (map[string]seededArtifact, string) {
	t.Helper()
	tag, _, _ := snapshotTagsByGeneration(t)
	raw := materializeSnapshot(t, dir, tag)
	seeded := seedEveryArtifactShape(t, ctx, raw)
	pre := filepath.Join(dir, "pre1104.db")
	if _, err := raw.ExecContext(ctx, "VACUUM INTO ?", pre); err != nil {
		t.Fatalf("vacuum into pre-1104 copy: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the seeded %s snapshot (1104 + 1105): %v", tag, err)
	}
	if afterCopy != nil {
		afterCopy(db)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw = openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	defer func() { _ = raw.Close() }()
	rewindDropArtifactsLegacy(t, ctx, raw)
	if _, err := raw.ExecContext(ctx, "ATTACH DATABASE ? AS pre", pre); err != nil {
		t.Fatalf("attach pre-1104 copy: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO artifacts_legacy (id, session_id, project_id, title, mime_type, content_hash, byte_size,
                                       source, source_ref_json, scope_kind, created_at)
         SELECT id, session_id, project_id, title, mime_type, content_hash, byte_size,
                source, source_ref_json, scope_kind, created_at FROM pre.artifacts`,
		`INSERT INTO artifact_versions_legacy (id, artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at)
         SELECT id, artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at FROM pre.artifact_versions`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("refill legacy tables: %v", err)
		}
	}
	if _, err := raw.ExecContext(ctx, "DETACH DATABASE pre"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	// Unrelated rows in the two sidecar tables 1105 must not touch (they
	// are empty in every committed snapshot, which would make the
	// zero-delta digest vacuous for them — AC-PI-3).
	for _, stmt := range []string{
		`INSERT INTO unit_edges (id, from_id, to_id, kind, version, created_at)
         VALUES ('seed-edge-1105', 'seed-unit-1', 'seed-unit-2', 'references', 1, 1700000002000)`,
		`INSERT INTO unit_sync_state (unit_id, node_id, synced_version, classification, last_synced,
                                      synced_server_version, synced_local_version)
         VALUES ('seed-unit-1', 'node-seed-unit-1', 0, 'team_shared', 1700000002001, 3, 1)`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed sidecar row: %v", err)
		}
	}
	var n int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ?", migration1105ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("setup: 1105 ledger rows after rewind = %d, %v", n, err)
	}
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts_legacy").Scan(&n); err != nil || n < len(seeded) {
		t.Fatalf("setup: artifacts_legacy rows = %d, %v; want >= %d seeded", n, err, len(seeded))
	}
	return seeded, tag
}

// unitsState is everything 1105 must leave untouched: the full content of
// the four units tables, and the media refcount of every hash any unit or
// unit version names.
type unitsState struct {
	digests   map[string]string
	refcounts map[string]int
}

func captureUnitsState(t *testing.T, ctx context.Context, path string) unitsState {
	t.Helper()
	raw := openRawSQLiteAt(t, path)
	defer func() { _ = raw.Close() }()
	st := unitsState{digests: map[string]string{}, refcounts: map[string]int{}}
	for _, table := range []string{"units", "unit_versions", "unit_edges", "unit_sync_state"} {
		st.digests[table] = rowsDigest(t, ctx, raw, table)
	}
	rows, err := raw.QueryContext(ctx, `
        SELECT json_extract(metadata, '$.content_hash') FROM units WHERE json_extract(metadata, '$.content_hash') IS NOT NULL
        UNION
        SELECT json_extract(metadata, '$.content_hash') FROM unit_versions WHERE json_extract(metadata, '$.content_hash') IS NOT NULL`)
	if err != nil {
		t.Fatalf("hashes: %v", err)
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan hash: %v", err)
		}
		hashes = append(hashes, h)
	}
	_ = rows.Close()
	// The production refcount query (coreart's units-backed store) over the
	// same rows, read through a raw handle so capturing state does not
	// itself run Open (and with it 1105).
	for _, h := range hashes {
		var n int
		if err := raw.QueryRowContext(ctx, `
            SELECT (SELECT COUNT(*) FROM units WHERE json_extract(metadata, '$.content_hash') = ?)
                 + (SELECT COUNT(*) FROM unit_versions WHERE json_extract(metadata, '$.content_hash') = ?)`, h, h).Scan(&n); err != nil {
			t.Fatalf("refcount %s: %v", h, err)
		}
		st.refcounts[h] = n
	}
	return st
}

func legacyTableCount(t *testing.T, ctx context.Context, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('artifacts_legacy','artifact_versions_legacy')").Scan(&n); err != nil {
		t.Fatalf("legacy table probe: %v", err)
	}
	return n
}

// assert1105ZeroDelta opens dir (in v0.87.0 shape) through production Open
// and asserts P-1: 1105 applied, legacy tables gone, units-side state and
// media refcounts identical, and the production refcount source agrees.
func assert1105ZeroDelta(t *testing.T, ctx context.Context, dir string) storage.DB {
	t.Helper()
	path := filepath.Join(dir, "data.db")
	before := captureUnitsState(t, ctx, path)
	if len(before.refcounts) == 0 {
		t.Fatal("setup: no artifact hashes on units — the zero-delta check would be vacuous")
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open (applies %s): %v", migration1105ID, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if p, _ := db.Migrations().Pending(); len(p) != 0 {
		t.Fatalf("Pending after Open = %d", len(p))
	}
	var applied int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ? AND action = 'applied'", migration1105ID).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("1105 ledger rows = %d, %v; want 1", applied, err)
	}

	after := captureUnitsState(t, ctx, path)
	post := openRawSQLiteAt(t, path)
	if n := legacyTableCount(t, ctx, post); n != 0 {
		t.Errorf("legacy tables after 1105 = %d, want 0", n)
	}
	_ = post.Close()
	for table, d := range before.digests {
		if after.digests[table] != d {
			t.Errorf("%s changed across 1105 — the drop may remove nothing but the legacy tables", table)
		}
	}
	store := coreart.NewSQLStore(db)
	src := coreart.ArtifactsRefcountSource{Store: store}
	for h, want := range before.refcounts {
		if after.refcounts[h] != want {
			t.Errorf("refcount(%s) = %d after 1105, want %d", h, after.refcounts[h], want)
		}
		got, err := src.Refcount(ctx, h)
		if err != nil {
			t.Fatalf("production Refcount(%s): %v", h, err)
		}
		if got < 1 {
			t.Errorf("production Refcount(%s) = %d after 1105 — media GC would collect a live artifact blob", h, got)
		}
	}
	return db
}

// TestMigration1105_PopulatedV087StateDropsWithZeroUnitsDelta is P-1 on the
// reconstructed v0.87.0 shape: every seeded artifact shape (every source x
// scope, with and without versions, orphaned session, project link,
// unparseable source ref) reads back field-for-field through the
// production store after the drop, and nothing on the units side moved.
func TestMigration1105_PopulatedV087StateDropsWithZeroUnitsDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	seeded, tag := buildV087State(t, ctx, dir, nil)
	t.Logf("v0.87.0 shape reconstructed from the %s snapshot + %d seeded artifacts", tag, len(seeded))

	db := assert1105ZeroDelta(t, ctx, dir)
	store := coreart.NewSQLStore(db)
	for id, want := range seeded {
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Errorf("Get(%s) after 1105: %v", id, err)
			continue
		}
		if got.ContentHash != want.hash || got.Title != want.title || got.ScopeKind != want.scope || got.CreatedAt.UnixNano() != want.created {
			t.Errorf("artifact %s after 1105 = %+v, want %+v", id, got, want)
		}
		vs, err := store.ListVersions(ctx, id)
		if err != nil {
			t.Fatalf("ListVersions(%s): %v", id, err)
		}
		wantN := len(want.versions)
		if wantN == 0 {
			wantN = 1 // 1104's synthesized v1
		}
		if len(vs) != wantN {
			t.Errorf("artifact %s versions after 1105 = %d, want %d", id, len(vs), wantN)
		}
	}
	// The snapshot's own seed artifact (testdata/upgrade/seed.sql) too.
	if vs, err := store.ListVersions(ctx, "seed-artifact-1"); err != nil || len(vs) != 2 {
		t.Errorf("seed-artifact-1 versions after 1105 = %d, %v; want 2", len(vs), err)
	}
}

// TestMigration1105_V087SnapshotBoots is P-1 against the REAL v0.87.0-era
// snapshot: it runs only once the newest committed snapshot's dump carries
// artifacts_legacy (i.e. was taken by a build that shipped 1104). Until
// testdata/upgrade/v0.87.0/ is committed it skips, naming why.
func TestMigration1105_V087SnapshotBoots(t *testing.T) {
	t.Parallel()
	_, newest, hasLegacy := snapshotTagsByGeneration(t)
	if !hasLegacy {
		t.Skipf("newest committed snapshot %s predates units/1104 (no artifacts_legacy in its dump); "+
			"this test activates when the v0.87.0 snapshot is committed — until then P-1 rides on the "+
			"reconstructed v0.87.0 state (TestMigration1105_PopulatedV087StateDropsWithZeroUnitsDelta)", newest)
	}
	ctx := context.Background()
	dir := t.TempDir()
	raw := materializeSnapshot(t, dir, newest)
	var legacyRows int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts_legacy").Scan(&legacyRows); err != nil {
		t.Fatalf("count %s artifacts_legacy: %v", newest, err)
	}
	if legacyRows == 0 {
		t.Logf("WARNING: %s artifacts_legacy is empty; the drop runs against an empty table here", newest)
	}
	_ = raw.Close()
	assert1105ZeroDelta(t, ctx, dir)
}

// TestMigration1105_ToleratesArtifactsDeletedSinceCopy pins the deliberate
// FR-2 deviation: on v0.87.0 every artifact delete and every session purge
// (C spec FR-6) removes the artifact UNIT and leaves its legacy row. Those
// rows are copies of data the user deleted; 1105 must drop them, not
// refuse — a refusal would make Open fail for every such user.
func TestMigration1105_ToleratesArtifactsDeletedSinceCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	var deleted []string
	seeded, _ := buildV087State(t, ctx, dir, func(db storage.DB) {
		store := coreart.NewSQLStore(db)
		// One single-artifact delete (the Library "delete" path)...
		if _, err := store.Delete(ctx, deletedOnV087); err != nil {
			t.Fatalf("delete artifact %s on v0.87.0: %v", deletedOnV087, err)
		}
		deleted = append(deleted, deletedOnV087)
		// ...and a project purge (the project-delete observer path), which
		// deletes every project-scoped artifact unit and nulls the project
		// link on the rest.
		purger, ok := store.(coreart.ScopePurger)
		if !ok {
			t.Fatalf("production artifacts store %T is not a ScopePurger", store)
		}
		if _, err := purger.PurgeProject(ctx, "seed-project-1"); err != nil {
			t.Fatalf("purge project on v0.87.0: %v", err)
		}
	})
	for id, a := range seeded {
		if a.scope == "project" {
			deleted = append(deleted, id)
		}
	}
	if len(deleted) < 2 {
		t.Fatalf("setup: deleted %d artifacts on v0.87.0, want >= 2", len(deleted))
	}
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	var orphaned int
	if err := raw.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM artifacts_legacy l WHERE NOT EXISTS (SELECT 1 FROM units u WHERE u.id = l.id)").Scan(&orphaned); err != nil || orphaned == 0 {
		t.Fatalf("setup: legacy rows without a unit = %d, %v; want > 0 (the deleted artifacts)", orphaned, err)
	}
	_ = raw.Close()

	db := assert1105ZeroDelta(t, ctx, dir)
	store := coreart.NewSQLStore(db)
	for _, id := range deleted {
		if _, err := store.Get(ctx, id); err == nil {
			t.Errorf("artifact %s deleted on v0.87.0 came back after 1105", id)
		}
	}
	for id := range seeded {
		isDeleted := false
		for _, d := range deleted {
			isDeleted = isDeleted || d == id
		}
		if !isDeleted {
			if _, err := store.Get(ctx, id); err != nil {
				t.Errorf("surviving artifact %s lost across 1105: %v", id, err)
			}
		}
	}
}

// deletedOnV087 is a stable artifact id from seedEveryArtifactShape that
// the deletion test deletes "on v0.87.0".
const deletedOnV087 = "01MIG1104BROKENSOURCEREF00"

// TestMigration1105_RefusesOnCopyMismatch is P-2: a planted copy mismatch
// makes 1105 refuse — Open fails closed naming the migration and
// ErrLegacyArtifactsUnverified, both legacy tables survive with every row,
// 1105 has no ledger row — and once the damage is repaired the same
// database boots and drops cleanly.
func TestMigration1105_RefusesOnCopyMismatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, plant, repair, wantInErr string
	}{
		{
			// A version row of a still-present artifact lost its units twin:
			// the legacy row is the only copy of that revision.
			name:      "version twin missing",
			plant:     "DELETE FROM unit_versions WHERE unit_id = 'seed-artifact-1' AND version = 2",
			repair:    "", // set below from the deleted row
			wantInErr: "seed-artifact-1 v2",
		},
		{
			// A legacy artifact's id is now held by a non-artifact unit: the
			// legacy row is the only artifact with that id.
			name:      "id squatted by another kind",
			plant:     "UPDATE units SET kind = 'doc' WHERE id = 'seed-artifact-1'",
			repair:    "UPDATE units SET kind = 'artifact' WHERE id = 'seed-artifact-1'",
			wantInErr: "seed-artifact-1 (now a doc unit)",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dir := t.TempDir()
			buildV087State(t, ctx, dir, nil)
			path := filepath.Join(dir, "data.db")

			raw := openRawSQLiteAt(t, path)
			repair := tc.repair
			if repair == "" {
				var meta string
				var created int64
				if err := raw.QueryRowContext(ctx,
					"SELECT metadata, created_at FROM unit_versions WHERE unit_id = 'seed-artifact-1' AND version = 2").Scan(&meta, &created); err != nil {
					t.Fatalf("read row to plant over: %v", err)
				}
				repair = fmt.Sprintf("INSERT INTO unit_versions (unit_id, version, body, metadata, created_at) VALUES ('seed-artifact-1', 2, '', '%s', %d)",
					strings.ReplaceAll(meta, "'", "''"), created)
			}
			if _, err := raw.ExecContext(ctx, tc.plant); err != nil {
				t.Fatalf("plant: %v", err)
			}
			preLegacy := map[string]string{
				"artifacts_legacy":         rowsDigest(t, ctx, raw, "artifacts_legacy"),
				"artifact_versions_legacy": rowsDigest(t, ctx, raw, "artifact_versions_legacy"),
			}
			_ = raw.Close()

			_, err := storagesqlite.Open(newConfig(dir))
			if err == nil {
				t.Fatal("Open succeeded over a planted copy mismatch — 1105 dropped the only copy")
			}
			for _, want := range []string{migration1105ID, units.ErrLegacyArtifactsUnverified.Error(), tc.wantInErr} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Open error does not contain %q:\n%v", want, err)
				}
			}

			check := openRawSQLiteAt(t, path)
			for table, d := range preLegacy {
				if got := rowsDigest(t, ctx, check, table); got != d {
					t.Errorf("%s altered by the refused migration", table)
				}
			}
			var n int
			if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ?", migration1105ID).Scan(&n); err != nil || n != 0 {
				t.Errorf("1105 ledger rows after refusal = %d, %v; want 0", n, err)
			}
			if _, err := check.ExecContext(ctx, repair); err != nil {
				t.Fatalf("repair: %v", err)
			}
			_ = check.Close()

			db, err := storagesqlite.Open(newConfig(dir))
			if err != nil {
				t.Fatalf("Open after repairing the copy: %v", err)
			}
			defer func() { _ = db.Close(ctx) }()
			post := openRawSQLiteAt(t, path)
			defer func() { _ = post.Close() }()
			if n := legacyTableCount(t, ctx, post); n != 0 {
				t.Errorf("legacy tables after the repaired reopen = %d, want 0", n)
			}
		})
	}
}

// TestMigration1105_FreshInstallAndReopenAfterRewind is FR-1(c) plus the
// repair-path tolerance: an empty-dir Open applies the full chain (1104
// renames empty tables, 1105 drops them); rewinding 1105's ledger row on
// that database — the shape the repair path's re-apply produces — reopens
// cleanly with the tables already gone (the IF EXISTS guard), and a third
// Open applies nothing.
func TestMigration1105_FreshInstallAndReopenAfterRewind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("fresh Open: %v", err)
	}
	var applied int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ? AND action='applied'", migration1105ID).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("fresh install: 1105 ledger rows = %d, %v; want 1", applied, err)
	}
	_ = db.Close(ctx)

	raw := openRaw(t, dir)
	if n := legacyTableCount(t, ctx, raw); n != 0 {
		t.Errorf("fresh install: legacy tables = %d, want 0", n)
	}
	if _, err := raw.ExecContext(ctx, "DELETE FROM harness_migrations WHERE id = ?", migration1105ID); err != nil {
		t.Fatalf("rewind 1105 ledger row: %v", err)
	}
	_ = raw.Close()

	db, err = storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after rewinding 1105 (tables already gone): %v", err)
	}
	if p, _ := db.Migrations().Pending(); len(p) != 0 {
		t.Errorf("Pending after reopen = %d", len(p))
	}
	_ = db.Close(ctx)
	db, err = storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("third Open: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	rows, err := db.Reader().Query(ctx, "SELECT id FROM harness_migrations WHERE id = ? ORDER BY rowid", migration1105ID)
	if err != nil {
		t.Fatalf("ledger rows: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	_ = rows.Close()
	sort.Strings(ids)
	if len(ids) != 1 {
		t.Errorf("1105 ledger rows after rewind + two reopens = %v, want exactly one", ids)
	}
}
