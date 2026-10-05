package fleet

// unit_sync_atomic_test.go — units-debt-01UNITD01 WP01 (spec FR-4/FR-5,
// pin P-3; artifacts-as-units-01DOGF0C branch review D-1).
//
// The fleet pull path used to write a pulled unit and its sync-state
// baseline in two separate transactions. A failure between them left a
// shared unit with no baseline (first-sight create) or with a local
// Version ahead of its baseline (fast-forward) — the next push reads it as
// locally-new, the next pull conflicts it against itself.
//
// These tests drive REAL sqlite (storagesqlite.Open, the production units
// SQL store) — never the in-memory store (CLAUDE.md blind spot #2: a memory
// fixture has no transactions to be atomic in). The failure is injected
// BETWEEN the two writes with a SQLite trigger that aborts the sync-state
// write after the units write has already executed inside the same
// statement sequence. With the old two-call code the units row commits
// before the sidecar write fails, so these tests fail on it.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func openUnitSyncSQLite(t *testing.T) (storage.DB, *units.Manager) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{DataDir: t.TempDir(), EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, units.NewManager(units.NewSQLStore(db))
}

func execSQL(t *testing.T, db storage.DB, stmt string) {
	t.Helper()
	if err := db.WriteTx(context.Background(), func(tx storage.WriteTx) error {
		_, err := tx.Exec(context.Background(), stmt)
		return err
	}); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func countRows(t *testing.T, db storage.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// TestUnitSyncer_PullCreate_AtomicWithSyncState (P-3, first-sight create):
// the sync-state INSERT is made to fail after the unit INSERT; NEITHER row
// may persist. Then, with the fault removed, the same pull commits BOTH.
func TestUnitSyncer_PullCreate_AtomicWithSyncState(t *testing.T) {
	db, m := openUnitSyncSQLite(t)
	ctx := context.Background()

	fake := &contextFakeServer{}
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-atomic-1", Kind: "doc", Title: "Team runbook", Body: "server body v3",
			Classification: ClassTeamShared, Version: 3, UpdatedAt: updated,
		}},
		Cursor: updated,
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	syncer := NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())

	// Fault between the two writes: the units INSERT runs, then the
	// sidecar INSERT aborts.
	execSQL(t, db, `CREATE TRIGGER inject_sync_state_fault BEFORE INSERT ON unit_sync_state
        BEGIN SELECT RAISE(ABORT, 'injected sync-state fault'); END`)

	if _, err := syncer.PullDown(ctx); err == nil || !strings.Contains(err.Error(), "injected sync-state fault") {
		t.Fatalf("PullDown with the injected fault = %v; want the injected error", err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM units WHERE classification = 'team'"); n != 0 {
		t.Errorf("team units after the failed pull = %d, want 0 (unit committed without its baseline)", n)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM unit_sync_state"); n != 0 {
		t.Errorf("sync-state rows after the failed pull = %d, want 0", n)
	}
	// The push worklist must not have gained a phantom "locally-new" unit.
	if dirty, err := m.ListDirty(ctx, units.ClassTeam); err != nil || len(dirty) != 0 {
		t.Errorf("ListDirty(team) after the failed pull = %d units, %v; want 0", len(dirty), err)
	}

	// Happy path: fault removed, the same node commits both rows.
	execSQL(t, db, "DROP TRIGGER inject_sync_state_fault")
	applied, err := syncer.PullDown(ctx)
	if err != nil || applied != 1 {
		t.Fatalf("PullDown after removing the fault = %d, %v; want 1, nil", applied, err)
	}
	st, err := m.GetSyncStateByNodeID(ctx, "srv-atomic-1")
	if err != nil {
		t.Fatalf("GetSyncStateByNodeID: %v", err)
	}
	u, err := m.Get(ctx, st.UnitID)
	if err != nil {
		t.Fatalf("Get pulled unit %s: %v", st.UnitID, err)
	}
	if u.Body != "server body v3" || u.Classification != units.ClassTeam {
		t.Errorf("pulled unit = %+v", u)
	}
	if st.SyncedServerVersion != 3 || st.SyncedLocalVersion != u.Version || st.Classification != string(ClassTeamShared) {
		t.Errorf("sidecar = %+v; want server=3 local=%d class=team_shared", st, u.Version)
	}
	if dirty, err := m.ListDirty(ctx, units.ClassTeam); err != nil || len(dirty) != 0 {
		t.Errorf("ListDirty(team) after the committed pull = %d, %v; want 0 (baseline present)", len(dirty), err)
	}
}

// TestUnitSyncer_PullFastForward_AtomicWithSyncState (P-3, fast-forward):
// the sidecar UPDATE is made to fail after the unit's version bump + history
// row; the unit body, version, history and sidecar must all be unchanged.
// Then the fault is removed and the same delta commits both.
func TestUnitSyncer_PullFastForward_AtomicWithSyncState(t *testing.T) {
	db, m := openUnitSyncSQLite(t)
	ctx := context.Background()

	local := seedTeamUnit(t, m, "doc", "v0 body")
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: local.ID, NodeID: "srv-ff-atomic",
		SyncedServerVersion: 1, SyncedLocalVersion: local.Version,
		Classification: string(ClassTeamShared),
	}); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	fake := &contextFakeServer{}
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-ff-atomic", Kind: "doc", Title: "doc", Body: "v2 server body",
			Classification: ClassTeamShared, Version: 2, UpdatedAt: updated,
		}},
		Cursor: updated,
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	syncer := NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())

	// The upsert hits the existing row, so it is the DO UPDATE arm that
	// must fail — after the unit_versions INSERT and units UPDATE ran.
	execSQL(t, db, `CREATE TRIGGER inject_sync_state_fault BEFORE UPDATE ON unit_sync_state
        BEGIN SELECT RAISE(ABORT, 'injected sync-state fault'); END`)

	if _, err := syncer.PullDown(ctx); err == nil || !strings.Contains(err.Error(), "injected sync-state fault") {
		t.Fatalf("PullDown with the injected fault = %v; want the injected error", err)
	}
	got, err := m.Get(ctx, local.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "v0 body" || got.Version != local.Version {
		t.Errorf("unit after the failed fast-forward = body %q v%d; want the untouched %q v%d", got.Body, got.Version, "v0 body", local.Version)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ?", local.ID); n != 0 {
		t.Errorf("history rows after the failed fast-forward = %d, want 0", n)
	}
	st, err := m.GetSyncState(ctx, local.ID)
	if err != nil || st.SyncedServerVersion != 1 || st.SyncedLocalVersion != local.Version {
		t.Errorf("sidecar after the failed fast-forward = %+v, %v; want server=1 local=%d", st, err, local.Version)
	}

	execSQL(t, db, "DROP TRIGGER inject_sync_state_fault")
	if applied, err := syncer.PullDown(ctx); err != nil || applied != 1 {
		t.Fatalf("PullDown after removing the fault = %d, %v; want 1, nil", applied, err)
	}
	got, err = m.Get(ctx, local.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	st, err = m.GetSyncState(ctx, local.ID)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got.Body != "v2 server body" || got.Version != local.Version+1 {
		t.Errorf("unit after the committed fast-forward = body %q v%d", got.Body, got.Version)
	}
	if st.SyncedServerVersion != 2 || st.SyncedLocalVersion != got.Version {
		t.Errorf("sidecar after the committed fast-forward = %+v; want server=2 local=%d", st, got.Version)
	}
	if len(syncer.Conflicts()) != 0 {
		t.Errorf("conflicts after a clean fast-forward = %+v", syncer.Conflicts())
	}
}
