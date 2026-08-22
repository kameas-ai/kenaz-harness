package scheduler

// upgrade_path_test.go — automation-actually-runs-01PMZ404 UNIT-18
// (WP-PI), AC-PI-1.
//
// CLAUDE.md blind spot #3: every OTHER test of the nil-dispatcher
// honesty fix (fire_test.go) starts from a database opened fresh on an
// empty directory. On a fresh database the migration high-water mark
// starts at 0 and every migration applies in one ascending pass, so a
// migration-SELECTION defect — or, as here, a behavioural regression
// that only shows up on a row a PREVIOUS release already wrote — cannot
// be observed. This file boots the row from a database a previous
// release actually produced (core/storage/sqlite/testdata/upgrade/),
// not from Open() on an empty directory, per WP-PI's mandate that every
// mission carries this coverage.
//
// The row: an ENABLED workflow_schedules entry, inserted directly into
// the v0.68.0 snapshot's raw sqlite file BEFORE storagesqlite.Open runs
// any pending migration — simulating a schedule a real user created on
// that release, sitting there with last_fired_at still NULL (never
// fired). This is exactly AC-PI-1's prescribed shape: "boot the v0.64.0
// snapshot [here, the newest available, v0.68.0] with an enabled
// workflow_schedules row, fire a nil-dispatcher tick, and assert
// last_fired_at is unchanged."

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

	_ "modernc.org/sqlite"
)

// upgradeSnapshotTag is the newest snapshot committed under
// core/storage/sqlite/testdata/upgrade/ at the time this test was
// written. Bump it if a newer one lands and this test is revisited;
// it is not auto-discovered like TestUpgradePath's loop because this
// test's job is a single targeted scenario, not chain-wide coverage.
const upgradeSnapshotTag = "v0.68.0"

// openRawSQLiteAt mirrors core/storage/sqlite/upgrade_path_test.go's
// helper of the same name (unexported there, so duplicated here rather
// than crossing a package boundary for one helper).
func openRawSQLiteAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite at %s: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	return db
}

// upgradeDumpPath resolves the snapshot's dump.sql relative to THIS
// package directory (core/workflows/scheduler), reaching across to
// core/storage/sqlite/testdata/upgrade/ — the canonical location
// scripts/ci/upgrade-snapshot.sh writes to and TestUpgradePath reads
// from. No second materialiser: this file reuses upgradesnap.Materialize,
// the same helper TestUpgradePath uses.
func upgradeDumpPath(tag string) string {
	return filepath.Join("..", "..", "storage", "sqlite", "testdata", "upgrade", tag, "dump.sql")
}

// TestFireSync_UpgradedDatabase_NilDispatcher_LastFiredAtStaysUnchanged
// is AC-PI-1's falsifiability assertion for this mission. It boots an
// ENABLED workflow_schedules row that a previous release (v0.68.0)
// wrote — not a row this test creates fresh — fires a nil-dispatcher
// scheduled tick against it, and asserts last_fired_at is still NULL
// by a raw SELECT against the SAME sqlite file storagesqlite.Open
// migrated, not through the Storage interface or the in-memory
// RunSummary.
func TestFireSync_UpgradedDatabase_NilDispatcher_LastFiredAtStaysUnchanged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpPath := upgradeDumpPath(upgradeSnapshotTag)
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read %s: %v — is core/storage/sqlite/testdata/upgrade/%s/dump.sql still committed?", dumpPath, err, upgradeSnapshotTag)
	}

	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s snapshot: %v", upgradeSnapshotTag, err)
	}

	// Insert the "a previous release's user already created this
	// schedule" row directly into the raw file, BEFORE storagesqlite.Open
	// (and therefore before any pending migration) ever touches this
	// directory under HEAD. last_fired_at is NULL — never fired — which
	// is the precondition the assertion below depends on.
	const workflowID = "wf-upgrade-schedule-probe"
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO workflows (id, name, description, yaml_source, version, hash, created_at, updated_at)
		 VALUES (?, 'Upgrade schedule probe', '', 'id: wf-upgrade-schedule-probe\nname: probe\nversion: 1\nsteps: []\n', 1, 'deadbeef', 1700000000, 1700000000)`,
		workflowID,
	); err != nil {
		t.Fatalf("seed workflows row: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO workflow_schedules (workflow_id, cron, timezone, enabled, last_fired_at, last_run_id)
		 VALUES (?, '0 7 * * *', 'UTC', 1, NULL, NULL)`,
		workflowID,
	); err != nil {
		t.Fatalf("seed workflow_schedules row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after seeding: %v", err)
	}

	// Boot under HEAD — the real production Open path, applying whatever
	// migrations landed between v0.68.0 and now.
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open on the %s snapshot (with seeded schedule row) failed: %v", upgradeSnapshotTag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	// Read the row back through the PRODUCTION Storage interface, not a
	// hand-rolled SELECT, to prove SQLiteStorage.LoadAll actually sees a
	// row a previous release wrote (not just a row this test's own
	// Upsert would produce).
	store := NewSQLiteStorage(db)
	stored, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	found := false
	for _, s := range stored {
		if s.WorkflowID == workflowID {
			found = true
			if !s.Enabled {
				t.Fatalf("seeded schedule row loaded as disabled — precondition broken")
			}
		}
	}
	if !found {
		t.Fatalf("LoadAll did not return the seeded %q row — the upgraded schedule is invisible to the scheduler", workflowID)
	}

	// s.fireSync is package-internal; New() with a nil Dispatcher and
	// this Store reproduces the exact pre-UNIT-2 production shape (no
	// wfsched.Dispatcher constructed) against a database this test did
	// NOT create fresh.
	s, err := New(ctx, Config{Store: store, Dispatcher: nil})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, fireErr := s.fireSync(ctx, workflowID, true /* scheduled */)
	if fireErr == nil {
		t.Fatal("fireSync on the upgraded database returned nil error with no dispatcher wired")
	}

	// The durable claim, by raw SELECT against the SAME file
	// storagesqlite.Open migrated — not the Storage interface, not the
	// in-memory RunSummary (spec §10 rule 1 / CLAUDE.md blind spot #2).
	rows, err := db.Reader().Query(ctx,
		`SELECT last_fired_at, last_run_id FROM workflow_schedules WHERE workflow_id = ?`, workflowID)
	if err != nil {
		t.Fatalf("SELECT workflow_schedules: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no workflow_schedules row for %q after Open", workflowID)
	}
	var lastFiredAt *int64
	var lastRunID *string
	if err := rows.Scan(&lastFiredAt, &lastRunID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if lastFiredAt != nil {
		t.Errorf("last_fired_at = %v, want NULL (unchanged from the upgraded snapshot's state) — "+
			"a nil-dispatcher tick against an UPGRADED database advanced the durable column", *lastFiredAt)
	}
	if lastRunID != nil {
		t.Errorf("last_run_id = %v, want NULL", *lastRunID)
	}
}
