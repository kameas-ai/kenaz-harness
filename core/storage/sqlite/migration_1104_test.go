package sqlite_test

// migration_1104_test.go — populated-snapshot coverage for
// "units/1104-artifacts-to-units" (artifacts-as-units-01DOGF0C WP04;
// spec FR-3, FR-4; pins P-1, P-2, P-3; WP-PI AC-PI-1/AC-PI-3).
//
// Every test here boots from a database a PREVIOUS RELEASE produced
// (testdata/upgrade/<tag>/dump.sql, materialised behind the harness's
// back), seeds it with artifacts of every Source x every scope, with and
// without version rows, then runs the production storagesqlite.Open —
// which is what applies 1104. No empty database, no in-memory store, no
// hand-built registry (CLAUDE.md blind spots #2/#3).
//
// The tags: the NEWEST committed snapshot (the database an upgrading user
// actually has) and the OLDEST (v0.63.0). No committed snapshot predates
// sessions/0327 — the chain starts at v0.63.0, whose ledger already
// carries 0327..0331 — so the 0327 damage ("artifacts with zero version
// rows") is reproduced the only way it can be: by seeding artifacts with
// no artifact_versions rows, which is exactly the post-damage state.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/units"

	_ "modernc.org/sqlite"
)

const migration1104ID = "units/1104-artifacts-to-units"

func TestMigration1104_IDIsPinned(t *testing.T) {
	t.Parallel()
	if units.MigrationIDArtifactsToUnits != migration1104ID {
		t.Fatalf("units.MigrationIDArtifactsToUnits = %q, want %q (the I14 gate and the upgrade tests key on the literal)",
			units.MigrationIDArtifactsToUnits, migration1104ID)
	}
}

// snapshotTags returns the oldest and newest committed snapshot tags.
func oldestAndNewestSnapshot(t *testing.T) (string, string) {
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
	return tags[0], tags[len(tags)-1]
}

// materializeSnapshot writes <tag>/dump.sql into dir/data.db and returns
// a raw handle on it (foreign_keys ON, like production).
func materializeSnapshot(t *testing.T, dir, tag string) *sql.DB {
	t.Helper()
	dump, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s dump: %v", tag, err)
	}
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(context.Background(), raw, string(dump)); err != nil {
		t.Fatalf("materialise %s: %v", tag, err)
	}
	return raw
}

type seededArtifact struct {
	id, sessionID, projectID, title, mime, hash, source, scope, sourceRef string
	size, created                                                         int64
	versions                                                              []seededVersion
}

type seededVersion struct {
	version int
	hash    string
	size    int64
	summary sql.NullString
	path    sql.NullString
	created int64
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// seedEveryArtifactShape inserts one artifact per (source x scope), half
// with version rows and half without, plus the edge shapes the legacy
// table can hold: a NULL session (orphaned by a session delete under the
// SET NULL FK), a project link on a session-scoped row, and an unparseable
// source_ref_json. Returns what was written, keyed by id.
func seedEveryArtifactShape(t *testing.T, ctx context.Context, raw *sql.DB) map[string]seededArtifact {
	t.Helper()
	out := map[string]seededArtifact{}
	sources := []string{"code_block", "tool_output", "user_pin", "model_output"}
	scopes := []string{"session", "project", "global"}
	n := 0
	for _, src := range sources {
		for _, scope := range scopes {
			n++
			a := seededArtifact{
				id:        fmt.Sprintf("01MIG1104%03d%s", n, strings.Repeat("0", 26-12)),
				sessionID: "seed-session-1",
				title:     src + " in " + scope,
				mime:      "text/plain",
				hash:      sha(fmt.Sprintf("capture-%d", n)),
				size:      int64(100 + n),
				source:    src,
				scope:     scope,
				sourceRef: fmt.Sprintf(`{"message_id":"seed-msg-2","code_block_index":%d,"filename":"f%d.go"}`, n, n),
				created:   1700000500000 + int64(n),
			}
			a.id = a.id[:26]
			if scope == "project" {
				a.projectID = "seed-project-1"
			}
			if n%2 == 0 { // with versions
				a.versions = []seededVersion{
					{1, sha(fmt.Sprintf("rev1-%d", n)), 200, sql.NullString{String: "first", Valid: true}, sql.NullString{String: "/p/1", Valid: true}, a.created + 10},
					{2, sha(fmt.Sprintf("rev2-%d", n)), 201, sql.NullString{}, sql.NullString{}, a.created + 20},
				}
			}
			out[a.id] = a
		}
	}
	orphan := seededArtifact{id: "01MIG1104ORPHANSESSION0000", title: "orphan", mime: "image/png", hash: sha("orphan"), size: 7,
		source: "tool_output", scope: "session", sourceRef: `{"message_id":"m"}`, created: 1700000600000}
	linked := seededArtifact{id: "01MIG1104SESSIONWITHPROJ00", sessionID: "seed-session-1", projectID: "seed-project-1", title: "linked",
		mime: "text/markdown", hash: sha("linked"), size: 8, source: "user_pin", scope: "session", sourceRef: `{"message_id":"m"}`, created: 1700000600001}
	broken := seededArtifact{id: "01MIG1104BROKENSOURCEREF00", sessionID: "seed-session-1", title: "broken ref",
		mime: "text/plain", hash: sha("broken"), size: 9, source: "code_block", scope: "global", sourceRef: `not json`, created: 1700000600002}
	for _, a := range []seededArtifact{orphan, linked, broken} {
		out[a.id] = a
	}

	for _, a := range out {
		var sess, proj any
		if a.sessionID != "" {
			sess = a.sessionID
		}
		if a.projectID != "" {
			proj = a.projectID
		}
		if _, err := raw.ExecContext(ctx, `
            INSERT INTO artifacts (id, session_id, project_id, title, mime_type, content_hash,
                                   byte_size, source, source_ref_json, scope_kind, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.id, sess, proj, a.title, a.mime, a.hash, a.size, a.source, a.sourceRef, a.scope, a.created); err != nil {
			t.Fatalf("seed artifact %s: %v", a.id, err)
		}
		for _, v := range a.versions {
			if _, err := raw.ExecContext(ctx, `
                INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at)
                VALUES (?, ?, ?, ?, 'text/plain', ?, ?, ?)`,
				a.id, v.version, v.hash, v.size, v.summary, v.path, v.created); err != nil {
				t.Fatalf("seed version %s/%d: %v", a.id, v.version, err)
			}
		}
	}
	return out
}

// writeBlob drops a CAS file for hash with no media_artifacts row, so the
// ONLY thing that can keep it alive through GC is a refcount source.
func writeBlob(t *testing.T, dir, hash string) {
	t.Helper()
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, hash), []byte("blob "+hash), 0o600); err != nil {
		t.Fatalf("write blob: %v", err)
	}
}

// TestMigration1104_PopulatedSnapshots is the headline test: for the
// oldest and the newest committed snapshot, seeded with every artifact
// shape, production Open copies every artifact and version onto units
// with ids, hashes, versions and scopes intact, synthesizes v1 only for
// version-less artifacts, keeps the legacy tables under *_legacy, leaves
// every document unit byte-identical, and media GC afterwards collects
// zero artifact blobs (P-1, P-2, P-3).
func TestMigration1104_PopulatedSnapshots(t *testing.T) {
	t.Parallel()
	oldest, newest := oldestAndNewestSnapshot(t)
	for _, tag := range []string{oldest, newest} {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dir := t.TempDir()
			raw := materializeSnapshot(t, dir, tag)
			seeded := seedEveryArtifactShape(t, ctx, raw)
			// A document unit WITH history beside the seed docs, so the
			// "unrelated unit_versions survive" assertion has a row to
			// guard (AC-PI-3).
			if _, err := raw.ExecContext(ctx, `
                INSERT INTO unit_versions (unit_id, version, body, metadata, created_at)
                VALUES ('seed-unit-1', 1, 'seed unit body one', '{}', 1700000001250)`); err != nil {
				t.Fatalf("seed doc unit version: %v", err)
			}
			var legacyArtifacts, legacyVersions int
			_ = raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts").Scan(&legacyArtifacts)
			_ = raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifact_versions").Scan(&legacyVersions)
			preDocs := snapshotNonArtifactUnits(t, ctx, raw)
			if !strings.Contains(preDocs["unit_versions"], "seed-unit-1") {
				t.Fatalf("setup: no document unit_versions row to guard")
			}

			// Blobs for every hash an artifact (head or version) names, plus
			// one unreferenced control blob that GC MUST collect.
			var liveHashes []string
			for _, a := range seeded {
				liveHashes = append(liveHashes, a.hash)
				for _, v := range a.versions {
					liveHashes = append(liveHashes, v.hash)
				}
			}
			for _, h := range liveHashes {
				writeBlob(t, dir, h)
			}
			control := sha("nobody references me")
			writeBlob(t, dir, control)
			if err := raw.Close(); err != nil {
				t.Fatalf("close raw: %v", err)
			}

			db, err := storagesqlite.Open(newConfig(dir))
			if err != nil {
				t.Fatalf("Open on populated %s snapshot (applies %s): %v", tag, migration1104ID, err)
			}
			t.Cleanup(func() { _ = db.Close(context.Background()) })
			if p, _ := db.Migrations().Pending(); len(p) != 0 {
				t.Fatalf("Pending after Open = %d", len(p))
			}

			// ---- P-1: every artifact reads back through the production
			// store, field for field.
			store := coreart.NewSQLStore(db)
			for id, want := range seeded {
				got, err := store.Get(ctx, id)
				if err != nil {
					t.Errorf("Get(%s) after migration: %v", id, err)
					continue
				}
				wantProj := want.projectID
				gotProj := ""
				if got.ProjectID != nil {
					gotProj = *got.ProjectID
				}
				if got.ID != id || got.SessionID != want.sessionID || gotProj != wantProj || got.Title != want.title ||
					got.MimeType != want.mime || got.ContentHash != want.hash || got.ByteSize != want.size ||
					got.Source != want.source || got.ScopeKind != want.scope || got.CreatedAt.UnixNano() != want.created {
					t.Errorf("artifact %s after migration = %+v\nwant %+v", id, got, want)
				}
				if want.sourceRef != "not json" && got.SourceRef.MessageID == "" {
					t.Errorf("artifact %s lost its source_ref: %+v", id, got.SourceRef)
				}
				vs, err := store.ListVersions(ctx, id)
				if err != nil {
					t.Fatalf("ListVersions(%s): %v", id, err)
				}
				if len(want.versions) == 0 {
					// D4: version-less → synthesized v1 from the parent row.
					if len(vs) != 1 || vs[0].Version != 1 || vs[0].ContentHash != want.hash || vs[0].ByteSize != want.size {
						t.Errorf("artifact %s (no legacy versions) versions = %+v, want one synthesized v1 = the capture", id, vs)
					}
					continue
				}
				if len(vs) != len(want.versions) {
					t.Errorf("artifact %s versions = %d, want %d", id, len(vs), len(want.versions))
					continue
				}
				for i, v := range want.versions {
					g := vs[i]
					if g.Version != v.version || g.ContentHash != v.hash || g.ByteSize != v.size || g.CreatedAt.UnixNano() != v.created ||
						(g.Summary != nil) != v.summary.Valid || (g.Path != nil) != v.path.Valid {
						t.Errorf("artifact %s v%d = %+v, want %+v", id, v.version, g, v)
					}
				}
			}
			// Scope / scope_id / classification on the unit rows directly.
			um := units.NewManager(units.NewSQLStore(db))
			for id, want := range seeded {
				u, err := um.Get(ctx, id)
				if err != nil {
					continue // already reported above
				}
				wantScopeID := ""
				switch want.scope {
				case "session":
					wantScopeID = want.sessionID
				case "project":
					wantScopeID = want.projectID
				}
				if string(u.Kind) != "artifact" || string(u.Scope) != want.scope || u.ScopeID != wantScopeID ||
					u.Classification != units.ClassPersonal || u.LoadPolicy != units.LoadOnDemand || u.Body != "" {
					t.Errorf("unit %s = kind %s scope %s/%q class %s policy %s; want artifact %s/%q personal on_demand",
						id, u.Kind, u.Scope, u.ScopeID, u.Classification, u.LoadPolicy, want.scope, wantScopeID)
				}
			}
			var brokenRaw string
			if err := db.Reader().QueryRow(ctx,
				"SELECT json_extract(metadata, '$.source_ref_raw') FROM units WHERE id = '01MIG1104BROKENSOURCEREF00'").Scan(&brokenRaw); err != nil || brokenRaw != "not json" {
				t.Errorf("unparseable source_ref_json not kept verbatim: %q, %v", brokenRaw, err)
			}

			// ---- P-3 (one-release pin, superseded): 1104 retained the
			// legacy tables under *_legacy names; units/1105
			// (units-debt-01UNITD01) dropped them in the next release, so
			// after the full chain no artifact table of either name exists
			// and every legacy row is accounted for on units (below, and
			// version-for-version in migration_1105_test.go).
			var n int
			if err := db.Reader().QueryRow(ctx,
				"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('artifacts','artifact_versions','artifacts_legacy','artifact_versions_legacy')").Scan(&n); err != nil || n != 0 {
				t.Errorf("legacy artifact tables still present: %d, %v", n, err)
			}
			if err := db.Reader().QueryRow(ctx, `SELECT COUNT(*) FROM unit_versions uv JOIN units u ON u.id = uv.unit_id
			     WHERE u.kind = 'artifact' AND json_extract(uv.metadata, '$.synthesized') IS NULL`).Scan(&n); err != nil || n != legacyVersions {
				t.Errorf("copied artifact versions = %d, %v; want %d (one per legacy version row)", n, err, legacyVersions)
			}
			if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM units WHERE kind='artifact'").Scan(&n); err != nil || n != legacyArtifacts {
				t.Errorf("artifact units = %d, %v; want %d (one per legacy artifact)", n, err, legacyArtifacts)
			}

			// ---- P-5 (WP05): no MIGRATED artifact unit is in the fleet
			// push worklist for either shared classification.
			for _, class := range []units.Classification{units.ClassTeam, units.ClassOrg} {
				dirty, err := um.ListDirty(ctx, class)
				if err != nil {
					t.Fatalf("ListDirty(%s): %v", class, err)
				}
				for _, u := range dirty {
					if u.Kind == units.KindArtifact {
						t.Errorf("migrated artifact unit %s is in the %s push worklist", u.ID, class)
					}
				}
			}
			var syncRows int
			if err := db.Reader().QueryRow(ctx,
				"SELECT COUNT(*) FROM unit_sync_state s JOIN units u ON u.id = s.unit_id WHERE u.kind = 'artifact'").Scan(&syncRows); err != nil || syncRows != 0 {
				t.Errorf("artifact units with a sync sidecar row = %d, %v; want 0", syncRows, err)
			}

			// ---- AC-PI-3: unrelated (document) units and their history
			// survive byte-identical.
			post := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
			postDocs := snapshotNonArtifactUnits(t, ctx, post)
			_ = post.Close()
			for table, before := range preDocs {
				if postDocs[table] != before {
					t.Errorf("document %s rows changed across the migration:\nbefore:\n%s\nafter:\n%s", table, before, postDocs[table])
				}
			}

			// ---- P-2: media GC after migration collects zero artifact
			// blobs (and does collect the unreferenced control, proving the
			// pass actually ran).
			media := attachments.NewSQLMediaStore(db, dir)
			media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
			media.RegisterRefcountSource(coreart.ArtifactsRefcountSource{Store: store})
			removed, err := media.PruneOrphans(ctx)
			if err != nil {
				t.Fatalf("PruneOrphans: %v", err)
			}
			if removed != 1 {
				t.Errorf("GC removed %d blobs, want exactly 1 (the unreferenced control)", removed)
			}
			var lost []string
			for _, h := range liveHashes {
				if _, err := os.Stat(filepath.Join(dir, "media", h)); err != nil {
					lost = append(lost, h)
				}
			}
			sort.Strings(lost)
			if len(lost) > 0 {
				t.Errorf("media GC deleted %d artifact blob(s) after migration: %v", len(lost), lost)
			}
			if _, err := os.Stat(filepath.Join(dir, "media", control)); err == nil {
				t.Error("unreferenced control blob survived GC — the pass did not run")
			}
		})
	}
}

// TestMigration1104_AbortsOnIDCollisionWithoutPartialState pins the
// all-or-nothing guarantee on a populated snapshot: when a unit already
// holds an artifact's id, Open fails, and NOTHING the migration does is
// left behind — the legacy tables keep their names and rows, no artifact
// unit exists, and 1104 has no ledger row.
func TestMigration1104_AbortsOnIDCollisionWithoutPartialState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, newest := oldestAndNewestSnapshot(t)
	dir := t.TempDir()
	raw := materializeSnapshot(t, dir, newest)
	seedEveryArtifactShape(t, ctx, raw)
	if _, err := raw.ExecContext(ctx, `
        INSERT INTO units (id, kind, scope, scope_id, classification, version, load_policy, title, body, metadata, created_at, updated_at)
        VALUES ('seed-artifact-1', 'doc', 'global', '', 'personal', 0, 'on_demand', 'squatter', '', '{}', 1, 1)`); err != nil {
		t.Fatalf("seed colliding unit: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if db, err := storagesqlite.Open(newConfig(dir)); err == nil {
		_ = db.Close(ctx)
		t.Fatal("Open succeeded despite an artifact id already present as a unit id")
	} else if !strings.Contains(err.Error(), migration1104ID) {
		t.Fatalf("Open error does not name %s: %v", migration1104ID, err)
	}

	check := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	defer func() { _ = check.Close() }()
	var n int
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts").Scan(&n); err != nil || n == 0 {
		t.Errorf("legacy `artifacts` table not intact after the aborted migration: %d rows, %v", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM units WHERE kind='artifact'").Scan(&n); err != nil || n != 0 {
		t.Errorf("artifact units after abort = %d, %v; want 0 (partial copy leaked)", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ?", migration1104ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("ledger rows for the aborted 1104 = %d, %v; want 0", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'artifacts_legacy'").Scan(&n); err != nil || n != 0 {
		t.Errorf("artifacts_legacy exists after abort (%d) — the rename escaped the rolled-back transaction", n)
	}
}

// TestMigration1104_SyntheticTenThousand measures 1104 on the newest
// snapshot inflated to 10,000 artifacts (half with two versions) — the
// spec §6 batching question. Skipped under -short; the number it logs is
// recorded in docs/missions/artifacts-as-units.md D10.
func TestMigration1104_SyntheticTenThousand(t *testing.T) {
	if testing.Short() {
		t.Skip("timing run; not part of -short")
	}
	ctx := context.Background()
	_, newest := oldestAndNewestSnapshot(t)
	dir := t.TempDir()
	raw := materializeSnapshot(t, dir, newest)
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	const total = 10000
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("01SYNTH%019d", i)
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO artifacts (id, session_id, project_id, title, mime_type, content_hash,
                                   byte_size, source, source_ref_json, scope_kind, created_at)
            VALUES (?, 'seed-session-1', NULL, ?, 'text/plain', ?, 10, 'code_block', '{"message_id":"m"}', 'session', ?)`,
			id, fmt.Sprintf("synthetic %d", i), sha(id), int64(1700001000000+i)); err != nil {
			t.Fatalf("insert synthetic artifact: %v", err)
		}
		if i%2 == 0 {
			for v := 1; v <= 2; v++ {
				if _, err := tx.ExecContext(ctx, `
                    INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size, mime_type, created_at)
                    VALUES (?, ?, ?, 10, 'text/plain', ?)`, id, v, sha(fmt.Sprintf("%s-%d", id, v)), int64(1700002000000+i)); err != nil {
					t.Fatalf("insert synthetic version: %v", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = raw.Close()

	start := time.Now()
	db, err := storagesqlite.Open(newConfig(dir))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Open with 10k artifacts: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	var n int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM units WHERE kind='artifact'").Scan(&n); err != nil || n != total+1 {
		t.Fatalf("artifact units = %d, %v; want %d", n, err, total+1)
	}
	t.Logf("Open (all pending migrations incl. %s) over %d artifacts + %d versions: %s", migration1104ID, total+1, total+2, elapsed)
}

// assertAbortedWithoutPartialState checks the all-or-nothing outcome of a
// failed 1104 on dir: legacy tables intact under their OLD names, no
// artifact unit, no *_legacy table, no ledger row.
func assertAbortedWithoutPartialState(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	check := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	defer func() { _ = check.Close() }()
	var n int
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts").Scan(&n); err != nil || n == 0 {
		t.Errorf("legacy `artifacts` not intact after abort: %d rows, %v", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifact_versions").Scan(&n); err != nil || n == 0 {
		t.Errorf("legacy `artifact_versions` not intact after abort: %d rows, %v", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM units WHERE kind='artifact'").Scan(&n); err != nil || n != 0 {
		t.Errorf("artifact units after abort = %d, %v; want 0", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM unit_versions uv JOIN artifacts a ON a.id = uv.unit_id").Scan(&n); err != nil || n != 0 {
		t.Errorf("copied unit_versions after abort = %d, %v; want 0", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ?", migration1104ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("ledger rows for the aborted 1104 = %d, %v; want 0", n, err)
	}
	if err := check.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('artifacts_legacy','artifact_versions_legacy','idx_units_meta_content_hash')").Scan(&n); err != nil || n != 0 {
		t.Errorf("objects created by the aborted 1104 survived (%d)", n)
	}
}

// TestMigration1104_AbortsOnMalformedUnitMetadata (review F6): a
// pre-existing unit whose metadata is not JSON would make the migration's
// json_extract indexes unbuildable. Open must fail naming that unit, write
// nothing, and boot once the row is repaired.
func TestMigration1104_AbortsOnMalformedUnitMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, newest := oldestAndNewestSnapshot(t)
	dir := t.TempDir()
	raw := materializeSnapshot(t, dir, newest)
	seedEveryArtifactShape(t, ctx, raw)
	if _, err := raw.ExecContext(ctx, "UPDATE units SET metadata = '{broken' WHERE id = 'seed-unit-2'"); err != nil {
		t.Fatalf("seed malformed metadata: %v", err)
	}
	_ = raw.Close()

	_, err := storagesqlite.Open(newConfig(dir))
	if err == nil {
		t.Fatal("Open succeeded over a unit with malformed metadata")
	}
	if !strings.Contains(err.Error(), "seed-unit-2") || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("abort error does not name the offending unit clearly: %v", err)
	}
	assertAbortedWithoutPartialState(t, ctx, dir)

	fix := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if _, err := fix.ExecContext(ctx, "UPDATE units SET metadata = '{}' WHERE id = 'seed-unit-2'"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	_ = fix.Close()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open after repairing the row: %v", err)
	}
	_ = db.Close(ctx)
}

// TestMigration1104_PostCopyMismatchRollsBackEverything (review F7): the
// collision test aborts BEFORE the copy; this one forces the abort AFTER
// it. A trigger tampers with one copied version row's hash, so the
// in-transaction verification fails once the INSERTs have already run.
// Everything must roll back — copy, indexes, renames, ledger — and the
// database must reopen cleanly once the tamper is gone.
func TestMigration1104_PostCopyMismatchRollsBackEverything(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, newest := oldestAndNewestSnapshot(t)
	dir := t.TempDir()
	raw := materializeSnapshot(t, dir, newest)
	seedEveryArtifactShape(t, ctx, raw)
	if _, err := raw.ExecContext(ctx, `
        CREATE TRIGGER tamper_1104 AFTER INSERT ON unit_versions
        WHEN NEW.unit_id = 'seed-artifact-1' AND NEW.version = 2
        BEGIN
            UPDATE unit_versions SET metadata = json_set(metadata, '$.content_hash', 'tampered') WHERE id = NEW.id;
        END`); err != nil {
		t.Fatalf("create tamper trigger: %v", err)
	}
	_ = raw.Close()

	_, err := storagesqlite.Open(newConfig(dir))
	if err == nil {
		t.Fatal("Open succeeded although a copied version row no longer matches its source")
	}
	if !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("abort error is not the post-copy verification: %v", err)
	}
	assertAbortedWithoutPartialState(t, ctx, dir)

	fix := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if _, err := fix.ExecContext(ctx, "DROP TRIGGER tamper_1104"); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	_ = fix.Close()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after the abort: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	var n int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM harness_migrations WHERE id = ? AND action = 'applied'", migration1104ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("1104 ledger rows after the clean reopen = %d, %v; want 1", n, err)
	}
}
