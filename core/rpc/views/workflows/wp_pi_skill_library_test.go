package workflows

// wp_pi_skill_library_test.go — skill-library-01SKLIB01 WP-PI, AC-PI-1.
//
// The mission adds no migration, but WP03 (revocation removal) and WP04
// (R2: restoring the user's own copy on mandate withdrawal) write and
// delete rows of the `workflows` table through the existing store. These
// tests drive those paths against a database the PREVIOUS release produced
// (core/storage/sqlite/testdata/upgrade/v0.91.0/dump.sql, materialised with
// upgradesnap.Materialize + storagesqlite.Open — the TestUpgradePath
// pattern), not Open on an empty directory, and assert through a SEPARATE
// raw connection that the intended rows changed and the snapshot's own
// unrelated row (seed-workflow-1) survived untouched.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

const wpPISnapshotTag = "v0.91.0"

// wpPIUpgradedStore materialises the v0.91.0 snapshot, boots it under HEAD
// and returns the workflows store plus the raw db path for independent reads.
func wpPIUpgradedStore(t *testing.T) (corewf.Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dumpPath := filepath.Join("..", "..", "..", "storage", "sqlite", "testdata", "upgrade", wpPISnapshotTag, "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read %s: %v (AC-PI-1 fixture missing)", dumpPath, err)
	}
	rawPath := filepath.Join(dir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(rawPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s: %v", wpPISnapshotTag, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open on the %s snapshot: %v", wpPISnapshotTag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return corewf.NewSQLiteStore(db), rawPath
}

// rawWorkflowRow reads one row through its own connection (not the store).
func rawWorkflowRow(t *testing.T, path, id string) (name, yamlSource string, ok bool) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.QueryRow(`SELECT name, yaml_source FROM workflows WHERE id = ?`, id).Scan(&name, &yamlSource)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("raw read %s: %v", id, err)
	}
	return name, yamlSource, true
}

func TestWPPI_MandateWithdrawalRestoresUserCopy_OnV091Snapshot(t *testing.T) {
	store, path := wpPIUpgradedStore(t)
	seedName, seedYAML, ok := rawWorkflowRow(t, path, "seed-workflow-1")
	if !ok {
		t.Fatal("snapshot lost its seed workflow row on boot")
	}
	prov := corewf.NewFileProvenanceStore(t.TempDir())
	api := New(Config{Engine: corewf.NewEngine(), Store: store, Provenance: prov})
	ctx := context.Background()

	user := []byte("id: team-flow\nname: Team v1\nversion: 1\nsteps:\n  - name: a\n    kind: shell\n    cmd: echo\n")
	org := []byte("id: team-flow\nname: ORG v2\nversion: 2\nsteps:\n  - name: b\n    kind: shell\n    cmd: true\n")
	if _, err := api.InstallDocument(ctx, user, DocumentOrigin{CatalogID: "cat-v1", Slug: "team-flow", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	_, prior, err := api.InstallMandatedDocument(ctx, org, "cat-v2", "2")
	if err != nil || len(prior) == 0 {
		t.Fatalf("takeover: prior=%d err=%v", len(prior), err)
	}
	if name, _, _ := rawWorkflowRow(t, path, "team-flow"); name != "ORG v2" {
		t.Fatalf("mandated row on disk = %q", name)
	}
	if _, err := api.RemoveMandatedDocument(ctx, "team-flow", "cat-v2", prior, false); err != nil {
		t.Fatal(err)
	}
	name, yamlSrc, ok := rawWorkflowRow(t, path, "team-flow")
	if !ok || name != "Team v1" {
		t.Fatalf("row after withdrawal = %q (present=%v) — want the user's own copy back on disk", name, ok)
	}
	if w, err := corewf.LoadYAML([]byte(yamlSrc)); err != nil || len(w.Steps) != 1 || w.Steps[0].Name != "a" {
		t.Fatalf("restored yaml_source does not decode to the user's document: %v %+v", err, w.Steps)
	}

	// WP03 on the same upgraded table: a revoked user install is removed.
	removed, err := api.RemoveRevokedCatalogDocument(ctx, "team-flow", "cat-v1")
	if err != nil || !removed {
		t.Fatalf("revocation removal: %v %v", removed, err)
	}
	if _, _, ok := rawWorkflowRow(t, path, "team-flow"); ok {
		t.Fatal("revoked workflow still on disk")
	}
	// Unrelated rows the previous release wrote survive every step.
	if n, y, ok := rawWorkflowRow(t, path, "seed-workflow-1"); !ok || n != seedName || y != seedYAML {
		t.Fatalf("seed-workflow-1 changed: present=%v name=%q", ok, n)
	}
}
