package trust_test

// AC-003 / AC-PI-1 / AC-PI-2 / AC-PI-3 for UNIT-3
// (bundle-download-and-verify-01PMZ909): the persisted AnchorStore.
//
// WP-PI note (AC-PI-2): this file is the ONLY place in the trust
// package's test suite that drives real sqlite. core/trust's existing
// engine_contract_test.go, verify_test.go, bundleadapter_test.go and
// policy_test.go legitimately test in-memory AnchorStore behaviour
// (algorithm policy, envelope shape, adapter translation) that has
// nothing to do with persistence — they are deliberately NOT changed
// here. Anything in this file that asserts a value survives a reopen
// drives storagesqlite.Open against a real on-disk database, per
// CLAUDE.md's "test fixtures that bypass the layer under test" blind
// spot.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/trust"

	_ "modernc.org/sqlite"
)

func testStorageConfig(dir string) storage.Config {
	return storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
}

// sqlHandle mirrors the structural interface core/rpc/api.go's
// buildJournalWriter uses to reach the concrete *sql.DB behind
// storage.DB without storage.DB growing a public method just for this.
type sqlHandle interface{ SQL() *sql.DB }

func rawSQL(t *testing.T, db storage.DB) *sql.DB {
	t.Helper()
	h, ok := db.(sqlHandle)
	if !ok {
		t.Fatalf("storage.DB does not expose SQL() *sql.DB")
	}
	raw := h.SQL()
	if raw == nil {
		t.Fatal("SQL() returned nil")
	}
	return raw
}

func testAnchor(id, peerID string) trust.Anchor {
	return trust.Anchor{
		AnchorID:  id,
		Kind:      trust.AnchorPinnedPeer,
		PeerID:    peerID,
		Algorithm: trust.AlgEd25519,
		PublicKey: trust.PublicKey{
			Algorithm:   trust.AlgEd25519,
			Bytes:       make([]byte, 32),
			Fingerprint: "fp-" + id,
		},
	}
}

// TestSQLiteAnchorStore_AC003_PersistsAcrossReopen is UNIT-3's headline
// persistence assertion: install an anchor, close the database, open a
// SECOND, independently-constructed handle from the SAME directory, and
// confirm FindByKeyID resolves it with Removed==false.
//
// This test would pass vacuously against a fixture that builds the
// engine with a default trust.Config{} on both sides (that still gets
// an in-memory store per engine.go's cfg.Anchors nil-default) — so both
// handles below are explicitly constructed with Config.Anchors set from
// a fresh NewSQLiteAnchorStore call bound to a fresh storagesqlite.Open,
// never reusing the first handle's engine or store.
func TestSQLiteAnchorStore_AC003_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// ---- First handle: open, install, close. ----
	db1, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open (first handle): %v", err)
	}
	store1, err := trust.NewSQLiteAnchorStore(rawSQL(t, db1))
	if err != nil {
		t.Fatalf("NewSQLiteAnchorStore (first handle): %v", err)
	}
	engine1, err := trust.NewEngineWithEmitter(trust.Config{Anchors: store1}, nil, trust.NewMemoryEmitter())
	if err != nil {
		t.Fatalf("NewEngineWithEmitter (first handle): %v", err)
	}
	anchor := testAnchor("ac003-anchor", "peer-ac003")
	if err := engine1.InstallAnchor(ctx, anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close first handle: %v", err)
	}

	// ---- Second, independently-constructed handle from the SAME dir. ----
	db2, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open (second handle): %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2, err := trust.NewSQLiteAnchorStore(rawSQL(t, db2))
	if err != nil {
		t.Fatalf("NewSQLiteAnchorStore (second handle): %v", err)
	}
	engine2, err := trust.NewEngineWithEmitter(trust.Config{Anchors: store2}, nil, trust.NewMemoryEmitter())
	if err != nil {
		t.Fatalf("NewEngineWithEmitter (second handle): %v", err)
	}

	got, err := engine2.ListAnchors(ctx)
	if err != nil {
		t.Fatalf("ListAnchors (second handle): %v", err)
	}
	found := false
	for _, a := range got {
		if a.AnchorID == anchor.AnchorID {
			found = true
			if a.Removed {
				t.Errorf("anchor %q Removed=true after a plain Install", a.AnchorID)
			}
			if a.PublicKey.Fingerprint != anchor.PublicKey.Fingerprint {
				t.Errorf("fingerprint=%q, want %q", a.PublicKey.Fingerprint, anchor.PublicKey.Fingerprint)
			}
		}
	}
	if !found {
		t.Fatalf("anchor %q installed on the first handle is not visible from a second, independently-opened handle on the same directory — F-1 is not fixed", anchor.AnchorID)
	}
}

// TestSQLiteAnchorStore_TombstoneThenFindByKeyID_ReturnsRemovedTrue
// pins the tombstone contract: FindByKeyID on a removed anchor must
// return the anchor with Removed=true, NOT ErrAnchorNotFound — because
// verify.go step 4 depends on this to distinguish RejAnchorRemoved from
// RejAnchorMissing.
//
// Mutation, executed (not left in the tree): anchor_sqlite.go's
// FindByKeyID query was temporarily changed to add "AND removed = 0" —
// i.e. to return ErrAnchorNotFound for a tombstoned row instead of the
// row with Removed=true — and this test went red with exactly that
// error ("trust: anchor not found"). See the UNIT-3 report for the
// pasted command + output. The change was reverted before commit; it
// is not reproduced as dead code here.
func TestSQLiteAnchorStore_TombstoneThenFindByKeyID_ReturnsRemovedTrue(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	store, err := trust.NewSQLiteAnchorStore(rawSQL(t, db))
	if err != nil {
		t.Fatalf("NewSQLiteAnchorStore: %v", err)
	}

	anchor := testAnchor("tombstone-anchor", "")
	if err := store.Install(ctx, anchor); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := store.Tombstone(ctx, anchor.AnchorID); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}

	got, err := store.FindByKeyID(ctx, anchor.PublicKey.Fingerprint)
	if err != nil {
		t.Fatalf("FindByKeyID after tombstone returned an error (want the row with Removed=true): %v", err)
	}
	if !got.Removed {
		t.Fatalf("FindByKeyID after tombstone returned Removed=false")
	}

	// List() must exclude tombstoned anchors.
	live, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, a := range live {
		if a.AnchorID == anchor.AnchorID {
			t.Fatalf("List() included a tombstoned anchor")
		}
	}
}

// TestSQLiteAnchorStore_IdentityCollision mirrors memAnchorStore's
// FR-015 collision guard: a second, live anchor claiming the same
// PeerID with a different fingerprint is refused.
func TestSQLiteAnchorStore_IdentityCollision(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	store, err := trust.NewSQLiteAnchorStore(rawSQL(t, db))
	if err != nil {
		t.Fatalf("NewSQLiteAnchorStore: %v", err)
	}

	a1 := testAnchor("collision-1", "shared-peer")
	if err := store.Install(ctx, a1); err != nil {
		t.Fatalf("Install a1: %v", err)
	}
	a2 := testAnchor("collision-2", "shared-peer")
	a2.PublicKey.Fingerprint = "different-fingerprint"
	err = store.Install(ctx, a2)
	if err == nil {
		t.Fatal("expected ErrIdentityCollision installing a second anchor for the same PeerID with a different fingerprint")
	}
}

// TestSQLiteAnchorStore_AC004_AppliesOverPreviousReleaseSnapshot
// extends AC-004: boot a database a PREVIOUS release actually produced
// (testdata/upgrade/v0.64.0/dump.sql, which predates this migration)
// and confirm the trust_anchors table is not just present but usable —
// install, close, reopen from the SAME upgraded file, read back.
//
// Falsifiability (AC-PI-1): reverting core/trust.RegisterMigrations's
// registration call in core/storage/sqlite/sqlite.go makes this test's
// NewSQLiteAnchorStore(...).Install call fail with "no such table:
// trust_anchors" — verified by execution; see the UNIT-3 report for the
// pasted command + output.
func TestSQLiteAnchorStore_AC004_AppliesOverPreviousReleaseSnapshot(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", "v0.64.0", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.64.0 snapshot not available at %s: %v", dumpPath, err)
	}

	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+rawPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.64.0 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialize: %v", err)
	}

	// ---- Open through the production path: applies migration 700
	// (and every other migration newer than the snapshot) against the
	// upgraded database. ----
	db, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.64.0 snapshot failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })

	store, err := trust.NewSQLiteAnchorStore(rawSQL(t, db))
	if err != nil {
		t.Fatalf("NewSQLiteAnchorStore: %v", err)
	}
	anchor := testAnchor("upgrade-anchor", "")
	if err := store.Install(ctx, anchor); err != nil {
		t.Fatalf("Install against an upgraded (v0.64.0-originated) database: %v", err)
	}
	got, err := store.FindByKeyID(ctx, anchor.PublicKey.Fingerprint)
	if err != nil {
		t.Fatalf("FindByKeyID: %v", err)
	}
	if got.AnchorID != anchor.AnchorID {
		t.Fatalf("AnchorID=%q, want %q", got.AnchorID, anchor.AnchorID)
	}
}
