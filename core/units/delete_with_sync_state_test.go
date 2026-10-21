package units_test

// delete_with_sync_state_test.go — Store.DeleteWithSyncState, the primitive
// behind the fleet pull path's tombstone apply. Real sqlite (CLAUDE.md blind
// spot #2): the point is that no row anywhere still holds the body.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func countSQL(t *testing.T, db storage.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestSQLStore_DeleteWithSyncState_RemovesUnitHistoryEdgesSidecar(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()

	shared, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand,
		Title: "shared", Body: "withdrawn-body-v0",
	})
	if err != nil {
		t.Fatalf("Create shared: %v", err)
	}
	if _, err := m.Update(ctx, shared.ID, "withdrawn-body-v1", nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	personal, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand,
		Title: "mine", Body: "my notes",
	})
	if err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	if _, err := m.AddEdge(ctx, units.Edge{FromID: personal.ID, ToID: shared.ID, Kind: units.EdgeReferences}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: shared.ID, NodeID: "node-1", SyncedServerVersion: 3, Classification: "team_shared",
	}); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}

	hadUnpushed, err := m.DeleteWithSyncState(ctx, shared.ID)
	if err != nil {
		t.Fatalf("DeleteWithSyncState: %v", err)
	}
	// Version 1 (one local Update) vs the sidecar's SyncedLocalVersion 0.
	if !hadUnpushed {
		t.Error("hadUnpushedEdits = false; want true (local v1 ahead of synced-local 0)")
	}

	if _, err := m.Get(ctx, shared.ID); !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("Get after delete = %v, want ErrUnitNotFound", err)
	}
	if n := countSQL(t, db, "SELECT COUNT(*) FROM units WHERE body LIKE 'withdrawn-body%'"); n != 0 {
		t.Errorf("units rows still holding the body = %d", n)
	}
	if n := countSQL(t, db, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ? OR body LIKE 'withdrawn-body%'", shared.ID); n != 0 {
		t.Errorf("history rows left = %d (older bodies must go too)", n)
	}
	if n := countSQL(t, db, "SELECT COUNT(*) FROM unit_edges WHERE from_id = ? OR to_id = ?", shared.ID, shared.ID); n != 0 {
		t.Errorf("edges left = %d", n)
	}
	if _, err := m.GetSyncStateByNodeID(ctx, "node-1"); !errors.Is(err, units.ErrSyncStateNotFound) {
		t.Errorf("sidecar after delete = %v, want ErrSyncStateNotFound", err)
	}
	// The personal unit that referenced it is untouched.
	got, err := m.Get(ctx, personal.ID)
	if err != nil || got.Body != "my notes" {
		t.Errorf("personal unit after delete = %+v, %v; want untouched", got, err)
	}
}

func TestSQLStore_DeleteWithSyncState_RefusesPersonal(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()
	p, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand,
		Title: "mine", Body: "keep me",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.DeleteWithSyncState(ctx, p.ID); !errors.Is(err, units.ErrPersonalUnit) {
		t.Fatalf("DeleteWithSyncState(personal) = %v, want ErrPersonalUnit", err)
	}
	if got, err := m.Get(ctx, p.ID); err != nil || got.Body != "keep me" {
		t.Errorf("personal unit = %+v, %v; want untouched", got, err)
	}
}

func TestSQLStore_DeleteWithSyncState_OrphanSidecarRemoved(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()
	u, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand, Title: "t", Body: "b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "node-o", Classification: "team_shared"}); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if _, err := m.DeleteWithSyncState(ctx, u.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// Second call on a now-missing unit is a nil no-op.
	if _, err := m.DeleteWithSyncState(ctx, u.ID); err != nil {
		t.Fatalf("delete of a missing unit = %v, want nil", err)
	}
	if n := countSQL(t, db, "SELECT COUNT(*) FROM unit_sync_state"); n != 0 {
		t.Errorf("sidecar rows = %d, want 0", n)
	}
}

// The child deletes must be explicit, not left to ON DELETE CASCADE. The
// production DSN forces foreign_keys(1) and the pragma cannot be switched
// inside a transaction, so the property is pinned with a trigger instead:
// deleting the units row while any child row still exists aborts. With
// explicit child deletes the trigger never fires; a delete that relied on
// cascade (parent first) would trip it.
func TestSQLStore_DeleteWithSyncState_DoesNotRelyOnCascade(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()
	a, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand, Title: "a", Body: "b0",
	})
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand, Title: "b", Body: "x",
	})
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if _, err := m.Update(ctx, a.ID, "b1", nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := m.AddEdge(ctx, units.Edge{FromID: b.ID, ToID: a.ID, Kind: units.EdgeReferences}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: a.ID, NodeID: "node-c", Classification: "team_shared"}); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `CREATE TRIGGER no_cascade_reliance BEFORE DELETE ON units
            WHEN EXISTS (SELECT 1 FROM unit_versions WHERE unit_id = OLD.id)
              OR EXISTS (SELECT 1 FROM unit_edges WHERE from_id = OLD.id OR to_id = OLD.id)
              OR EXISTS (SELECT 1 FROM unit_sync_state WHERE unit_id = OLD.id)
            BEGIN SELECT RAISE(ABORT, 'child rows still present: delete relied on cascade'); END`)
		return err
	}); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if _, err := m.DeleteWithSyncState(ctx, a.ID); err != nil {
		t.Fatalf("DeleteWithSyncState: %v", err)
	}
	if _, err := m.Get(ctx, a.ID); !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("unit still present: %v", err)
	}
}

func TestSQLStore_DemoteToPersonal_KeepsContentDropsSidecar(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()
	u, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p",
		Classification: units.ClassOrg, LoadPolicy: units.LoadOnDemand, Title: "mine", Body: "v0",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Update(ctx, u.ID, "v1", nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "node-d", Classification: "org_shared"}); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if err := m.DemoteToPersonal(ctx, u.ID); err != nil {
		t.Fatalf("DemoteToPersonal: %v", err)
	}
	got, err := m.Get(ctx, u.ID)
	if err != nil || got.Classification != units.ClassPersonal || got.Body != "v1" || got.Version != 1 {
		t.Errorf("demoted unit = %+v, %v; want personal v1 body intact", got, err)
	}
	if n := countSQL(t, db, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ?", u.ID); n != 1 {
		t.Errorf("history rows = %d, want 1 (kept)", n)
	}
	if _, err := m.GetSyncState(ctx, u.ID); !errors.Is(err, units.ErrSyncStateNotFound) {
		t.Errorf("sidecar after demote = %v, want gone", err)
	}
	if dirty, err := m.ListDirty(ctx, units.ClassOrg); err != nil || len(dirty) != 0 {
		t.Errorf("ListDirty(org) = %d, %v; want 0", len(dirty), err)
	}
	if err := m.DemoteToPersonal(ctx, "missing"); !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("DemoteToPersonal(missing) = %v, want ErrUnitNotFound", err)
	}
}
