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
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
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

// buildV087State leaves dir/data.db in the exact shape v0.87.0 leaves an
// upgraded install in (see the file comment), seeded with every artifact
// shape. afterCopy, when non-nil, runs against the production storage.DB
// between 1104's copy and the reconstruction — i.e. it acts as the user
// on v0.87.0 (deleting an artifact, purging a session). Returns the
// seeded artifacts and the pre-1104 snapshot tag used.
func buildV087State(t *testing.T, ctx context.Context, dir string, afterCopy func(db storage.DB)) (map[string]seededArtifact, string) {
	t.Helper()
	_, tag := oldestAndNewestPre1104Snapshot(t, upgradeSnapshotRoot)
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

// TestMigration1105_V087SnapshotBoots is P-1 against the REAL 1104-era
// snapshot: the newest committed snapshot whose dump CREATES
// artifacts_legacy (v0.87.0, or a later 1104-era patch tag). It skips only
// while no snapshot tag >= v0.87.0 exists, and FAILS if one does but none
// carries artifacts_legacy (v087SnapshotDecision; review H1).
func TestMigration1105_V087SnapshotBoots(t *testing.T) {
	t.Parallel()
	action, newest, msg := v087SnapshotDecision(t, upgradeSnapshotRoot)
	switch action {
	case v087Skip:
		t.Skip(msg)
	case v087Fail:
		t.Fatal(msg)
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

// drop1105LogCapture records the units.drop_artifacts_legacy* log lines.
// Tests that install it are NOT parallel (logging.Replace is global), so
// no other 1105 test's lines can interleave.
type drop1105LogCapture struct {
	next slog.Handler
	mu   sync.Mutex
	recs []map[string]string
}

func (c *drop1105LogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *drop1105LogCapture) Handle(ctx context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "units.drop_artifacts_legacy") {
		m := map[string]string{"msg": r.Message, "level": r.Level.String()}
		r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.String(); return true })
		c.mu.Lock()
		c.recs = append(c.recs, m)
		c.mu.Unlock()
	}
	if c.next != nil && c.next.Enabled(ctx, r.Level) {
		return c.next.Handle(ctx, r)
	}
	return nil
}
func (c *drop1105LogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *drop1105LogCapture) WithGroup(string) slog.Handler      { return c }

// orphanWarns returns artifact_id -> the WARN line naming it.
func (c *drop1105LogCapture) orphanWarns() map[string]map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[string]string{}
	for _, m := range c.recs {
		if m["msg"] == "units.drop_artifacts_legacy.orphan_quarantined" && m["level"] == "WARN" {
			out[m["artifact_id"]] = m
		}
	}
	return out
}

func captureDrop1105Logs(t *testing.T) *drop1105LogCapture {
	t.Helper()
	prev := logging.Handler()
	c := &drop1105LogCapture{next: prev}
	logging.Replace(c)
	t.Cleanup(func() { logging.Replace(prev) })
	return c
}

// legacyRowSnapshot is one artifacts_legacy row plus its version count, read
// before 1105 runs, to check the quarantine copy against.
type legacyRowSnapshot struct {
	title, hash, sourceRef string
	versions               int
}

func readLegacyRows(t *testing.T, ctx context.Context, path string) map[string]legacyRowSnapshot {
	t.Helper()
	raw := openRawSQLiteAt(t, path)
	defer func() { _ = raw.Close() }()
	rows, err := raw.QueryContext(ctx, `SELECT l.id, l.title, l.content_hash, l.source_ref_json,
	        (SELECT COUNT(*) FROM artifact_versions_legacy v WHERE v.artifact_id = l.id) FROM artifacts_legacy l`)
	if err != nil {
		t.Fatalf("read legacy rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]legacyRowSnapshot{}
	for rows.Next() {
		var id string
		var r legacyRowSnapshot
		if err := rows.Scan(&id, &r.title, &r.hash, &r.sourceRef, &r.versions); err != nil {
			t.Fatalf("scan legacy row: %v", err)
		}
		out[id] = r
	}
	return out
}

// assertQuarantined checks that exactly wantIDs are in
// artifacts_legacy_orphans, each a faithful copy of its legacy row (title,
// hash, source ref verbatim, every legacy version in legacy_metadata), and
// each named by a WARN line.
func assertQuarantined(t *testing.T, ctx context.Context, path string, legacy map[string]legacyRowSnapshot, wantIDs []string, logs *drop1105LogCapture) {
	t.Helper()
	raw := openRawSQLiteAt(t, path)
	defer func() { _ = raw.Close() }()
	rows, err := raw.QueryContext(ctx, `SELECT id, title, content_hash, source_ref_json,
	        json_extract(legacy_metadata, '$.dropped_from'), json_array_length(legacy_metadata, '$.versions'), quarantined_at
	   FROM artifacts_legacy_orphans`)
	if err != nil {
		t.Fatalf("read quarantine: %v", err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var id, title, hash, ref, from, at string
		var nVersions int
		if err := rows.Scan(&id, &title, &hash, &ref, &from, &nVersions, &at); err != nil {
			t.Fatalf("scan quarantine: %v", err)
		}
		got[id] = true
		want, ok := legacy[id]
		if !ok {
			t.Errorf("quarantined %s was never a legacy row", id)
			continue
		}
		if title != want.title || hash != want.hash || ref != want.sourceRef || nVersions != want.versions ||
			from != "artifacts_legacy" || at == "" {
			t.Errorf("quarantine row %s = (%q,%s,%q,from=%s,versions=%d,at=%q); want (%q,%s,%q,from=artifacts_legacy,versions=%d)",
				id, title, hash, ref, from, nVersions, at, want.title, want.hash, want.sourceRef, want.versions)
		}
	}
	_ = rows.Close()
	sort.Strings(wantIDs)
	var gotIDs []string
	for id := range got {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("quarantined ids = %v, want %v", gotIDs, wantIDs)
	}
	warns := logs.orphanWarns()
	for _, id := range wantIDs {
		w, ok := warns[id]
		if !ok {
			t.Errorf("no WARN line names orphan %s", id)
			continue
		}
		if w["content_hash"] != legacy[id].hash || w["title"] != legacy[id].title {
			t.Errorf("WARN for %s = %v; want hash %s title %q", id, w, legacy[id].hash, legacy[id].title)
		}
	}
	if len(warns) != len(wantIDs) {
		t.Errorf("orphan WARN lines = %d, want %d", len(warns), len(wantIDs))
	}
}

// TestMigration1105_QuarantinesArtifactsDeletedSinceCopy pins the
// deliberate FR-2 deviation (orchestrator ruling: quarantine, not refuse):
// on v0.87.0 every artifact delete and every project/session purge (C spec
// FR-6) removes the artifact UNIT and leaves its legacy row. 1105 must not
// refuse (Open would fail for every such user) and must not silently drop
// them either: each is moved to artifacts_legacy_orphans and named at WARN.
// Not parallel: it swaps the global log handler.
func TestMigration1105_QuarantinesArtifactsDeletedSinceCopy(t *testing.T) {
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
	path := filepath.Join(dir, "data.db")
	legacy := readLegacyRows(t, ctx, path)
	logs := captureDrop1105Logs(t)

	db := assert1105ZeroDelta(t, ctx, dir)
	assertQuarantined(t, ctx, path, legacy, deleted, logs)
	store := coreart.NewSQLStore(db)
	for _, id := range deleted {
		if _, err := store.Get(ctx, id); err == nil {
			t.Errorf("artifact %s deleted on v0.87.0 came back as a live artifact after 1105", id)
		}
	}
	isDeleted := map[string]bool{}
	for _, d := range deleted {
		isDeleted[d] = true
	}
	for id := range seeded {
		if !isDeleted[id] {
			if _, err := store.Get(ctx, id); err != nil {
				t.Errorf("surviving artifact %s lost across 1105: %v", id, err)
			}
		}
	}
}

// TestMigration1105_MassLossQuarantinesEverything: every artifact unit is
// gone (a user who deleted all of them on v0.87.0 — or a bug/tamper that
// did). Open must still succeed; every legacy row is quarantined and
// WARNed; the big tables are still dropped. Not parallel (log handler).
func TestMigration1105_MassLossQuarantinesEverything(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	buildV087State(t, ctx, dir, nil)
	path := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, path)
	for _, stmt := range []string{
		"DELETE FROM unit_versions WHERE unit_id IN (SELECT id FROM units WHERE kind = 'artifact')",
		"DELETE FROM unit_edges WHERE from_id IN (SELECT id FROM units WHERE kind = 'artifact') OR to_id IN (SELECT id FROM units WHERE kind = 'artifact')",
		"DELETE FROM unit_sync_state WHERE unit_id IN (SELECT id FROM units WHERE kind = 'artifact')",
		"DELETE FROM units WHERE kind = 'artifact'",
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("mass delete: %v", err)
		}
	}
	_ = raw.Close()
	legacy := readLegacyRows(t, ctx, path)
	var all []string
	for id := range legacy {
		all = append(all, id)
	}
	if len(all) < 10 {
		t.Fatalf("setup: %d legacy rows, want the seeded set", len(all))
	}
	logs := captureDrop1105Logs(t)

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open with every artifact unit gone: %v (mass loss must quarantine, not brick)", err)
	}
	defer func() { _ = db.Close(ctx) }()
	post := openRawSQLiteAt(t, path)
	if n := legacyTableCount(t, ctx, post); n != 0 {
		t.Errorf("legacy tables after mass-loss 1105 = %d, want 0 (still dropped)", n)
	}
	_ = post.Close()
	assertQuarantined(t, ctx, path, legacy, all, logs)
}

// deletedOnV087 is a stable artifact id from seedEveryArtifactShape that
// the deletion test deletes "on v0.87.0".
const deletedOnV087 = "01MIG1104BROKENSOURCEREF00"

// versionLessSeeded is a seedEveryArtifactShape artifact with no legacy
// version rows — 1104 synthesized its v1.
const versionLessSeeded = "01MIG1104ORPHANSESSION0000"

// TestMigration1105_RefusesOnCopyMismatch is P-2: a planted copy mismatch
// makes 1105 refuse — Open fails closed naming the migration and
// ErrLegacyArtifactsUnverified, the legacy tables survive with every row,
// 1105 has no ledger row — and once the damage is repaired the same
// database boots and drops cleanly.
func TestMigration1105_RefusesOnCopyMismatch(t *testing.T) {
	t.Parallel()
	type twin struct {
		unitID  string
		version int
	}
	cases := []struct {
		name, plant, repair, wantInErr string
		// restore, when set, re-writes this unit_versions row exactly as it
		// was before the plant (the repair for twin damage).
		restore *twin
	}{
		{
			name:      "real version twin missing",
			plant:     "DELETE FROM unit_versions WHERE unit_id = 'seed-artifact-1' AND version = 2",
			restore:   &twin{"seed-artifact-1", 2},
			wantInErr: "seed-artifact-1 v2",
		},
		{
			name:      "real version twin byte_size differs",
			plant:     "UPDATE unit_versions SET metadata = json_set(metadata, '$.byte_size', 999999) WHERE unit_id = 'seed-artifact-1' AND version = 2",
			restore:   &twin{"seed-artifact-1", 2},
			wantInErr: "seed-artifact-1 v2",
		},
		{
			name:      "real version twin created_at differs",
			plant:     "UPDATE unit_versions SET created_at = created_at + 1 WHERE unit_id = 'seed-artifact-1' AND version = 2",
			restore:   &twin{"seed-artifact-1", 2},
			wantInErr: "seed-artifact-1 v2",
		},
		{
			name:      "synthesized v1 twin missing",
			plant:     "DELETE FROM unit_versions WHERE unit_id = '" + versionLessSeeded + "' AND version = 1",
			restore:   &twin{versionLessSeeded, 1},
			wantInErr: versionLessSeeded + " synthesized v1",
		},
		{
			name:      "synthesized v1 twin hash differs",
			plant:     "UPDATE unit_versions SET metadata = json_set(metadata, '$.content_hash', 'tampered') WHERE unit_id = '" + versionLessSeeded + "' AND version = 1",
			restore:   &twin{versionLessSeeded, 1},
			wantInErr: versionLessSeeded + " synthesized v1",
		},
		{
			name:      "id squatted by another kind",
			plant:     "UPDATE units SET kind = 'doc' WHERE id = 'seed-artifact-1'",
			repair:    "UPDATE units SET kind = 'artifact' WHERE id = 'seed-artifact-1'",
			wantInErr: "seed-artifact-1 (now a doc unit)",
		},
		{
			// 1104 renames both tables in one transaction: a lone one is damage.
			name:      "lone legacy table",
			plant:     "ALTER TABLE artifact_versions_legacy RENAME TO avl_hidden_by_test",
			repair:    "ALTER TABLE avl_hidden_by_test RENAME TO artifact_versions_legacy",
			wantInErr: "only one legacy table exists",
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
			repair := []string{tc.repair}
			if tc.restore != nil {
				var meta string
				var created int64
				if err := raw.QueryRowContext(ctx,
					"SELECT metadata, created_at FROM unit_versions WHERE unit_id = ? AND version = ?",
					tc.restore.unitID, tc.restore.version).Scan(&meta, &created); err != nil {
					t.Fatalf("read row to plant over: %v", err)
				}
				repair = []string{
					fmt.Sprintf("DELETE FROM unit_versions WHERE unit_id = '%s' AND version = %d", tc.restore.unitID, tc.restore.version),
					fmt.Sprintf("INSERT INTO unit_versions (unit_id, version, body, metadata, created_at) VALUES ('%s', %d, '', '%s', %d)",
						tc.restore.unitID, tc.restore.version, strings.ReplaceAll(meta, "'", "''"), created),
				}
			}
			if _, err := raw.ExecContext(ctx, tc.plant); err != nil {
				t.Fatalf("plant: %v", err)
			}
			preLegacy := map[string]string{}
			for _, table := range []string{"artifacts_legacy", "artifact_versions_legacy", "avl_hidden_by_test"} {
				var n int
				_ = raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n)
				if n == 1 {
					preLegacy[table] = rowsDigest(t, ctx, raw, table)
				}
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
			for _, stmt := range repair {
				if _, err := check.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("repair %q: %v", stmt, err)
				}
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
			var orphans int
			if err := post.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts_legacy_orphans").Scan(&orphans); err != nil || orphans != 0 {
				t.Errorf("quarantined rows after a repaired (complete) copy = %d, %v; want 0", orphans, err)
			}
		})
	}
}

// TestMigration1105_RefusesWhen1104NotApplied exercises check 1, reachable
// only by ledger surgery: the runner keys "applied" on the latest row per
// VERSION, check 1 on the latest row per ID. A 1104 row superseded by
// 'rolled_back' while another row keeps version 1104 'applied' passes the
// runner and must still stop 1105.
func TestMigration1105_RefusesWhen1104NotApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	buildV087State(t, ctx, dir, nil)
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	for _, stmt := range []string{
		`INSERT INTO harness_migrations (version, id, applied_at, content_hash, owning_mission, action)
         SELECT version, id, applied_at, content_hash, owning_mission, 'rolled_back'
           FROM harness_migrations WHERE id = 'units/1104-artifacts-to-units' ORDER BY rowid DESC LIMIT 1`,
		`INSERT INTO harness_migrations (version, id, applied_at, content_hash, owning_mission, action)
         SELECT version, 'units/1104-ledger-surgery', applied_at, content_hash, owning_mission, 'applied'
           FROM harness_migrations WHERE id = 'units/1104-artifacts-to-units' ORDER BY rowid DESC LIMIT 1`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("ledger surgery: %v", err)
		}
	}
	_ = raw.Close()
	_, err := storagesqlite.Open(newConfig(dir))
	if err == nil {
		t.Fatal("Open succeeded although 1104's own latest ledger row is rolled_back")
	}
	for _, want := range []string{migration1105ID, units.ErrLegacyArtifactsUnverified.Error(), `latest ledger action is "rolled_back"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Open error does not contain %q:\n%v", want, err)
		}
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
