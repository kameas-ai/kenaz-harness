package units_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// seedUnit creates a unit through the manager and returns it.
func seedUnit(t *testing.T, m *units.Manager, class units.Classification) units.Unit {
	t.Helper()
	u, err := m.Create(context.Background(), units.Unit{
		Kind:           units.KindDoc,
		Scope:          units.ScopeProject,
		ScopeID:        "proj-1",
		Classification: class,
		LoadPolicy:     units.LoadAlways,
		Title:          "t",
		Body:           "b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return u
}

// runSyncStateSuite exercises the sidecar against a given store, so both
// the in-memory and SQL implementations are covered identically.
func runSyncStateSuite(t *testing.T, store units.Store) {
	m := units.NewManager(store)
	ctx := context.Background()

	t.Run("missing_returns_not_found", func(t *testing.T) {
		_, err := m.GetSyncState(ctx, "nope")
		if !errors.Is(err, units.ErrSyncStateNotFound) {
			t.Fatalf("GetSyncState missing: got %v, want ErrSyncStateNotFound", err)
		}
		_, err = m.GetSyncStateByNodeID(ctx, "nope-node")
		if !errors.Is(err, units.ErrSyncStateNotFound) {
			t.Fatalf("GetSyncStateByNodeID missing: got %v, want ErrSyncStateNotFound", err)
		}
	})

	t.Run("upsert_get_roundtrip", func(t *testing.T) {
		u := seedUnit(t, m, units.ClassTeam)
		ts := time.Now().UTC().Truncate(time.Millisecond)
		want := units.SyncState{
			UnitID:         u.ID,
			NodeID:         "node-" + u.ID,
			SyncedVersion:  3,
			Classification: "team_shared",
			LastSynced:     ts,
		}
		if _, err := m.UpsertSyncState(ctx, want); err != nil {
			t.Fatalf("UpsertSyncState: %v", err)
		}
		got, err := m.GetSyncState(ctx, u.ID)
		if err != nil {
			t.Fatalf("GetSyncState: %v", err)
		}
		if got.NodeID != want.NodeID || got.SyncedVersion != 3 || got.Classification != "team_shared" {
			t.Fatalf("roundtrip mismatch: got %+v want %+v", got, want)
		}
		// Lookup by node id must resolve to the same unit (no re-discovery).
		byNode, err := m.GetSyncStateByNodeID(ctx, want.NodeID)
		if err != nil {
			t.Fatalf("GetSyncStateByNodeID: %v", err)
		}
		if byNode.UnitID != u.ID {
			t.Fatalf("byNode.UnitID = %q, want %q", byNode.UnitID, u.ID)
		}
	})

	t.Run("upsert_replaces", func(t *testing.T) {
		u := seedUnit(t, m, units.ClassTeam)
		if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "n1", SyncedVersion: 1, Classification: "team_shared"}); err != nil {
			t.Fatalf("UpsertSyncState 1: %v", err)
		}
		if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "n1", SyncedVersion: 5, Classification: "org_shared"}); err != nil {
			t.Fatalf("UpsertSyncState 2: %v", err)
		}
		got, err := m.GetSyncState(ctx, u.ID)
		if err != nil {
			t.Fatalf("GetSyncState: %v", err)
		}
		if got.SyncedVersion != 5 || got.Classification != "org_shared" {
			t.Fatalf("replace mismatch: got %+v", got)
		}
	})

	t.Run("node_id_unique", func(t *testing.T) {
		a := seedUnit(t, m, units.ClassTeam)
		b := seedUnit(t, m, units.ClassTeam)
		if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: a.ID, NodeID: "shared-node", SyncedVersion: 1, Classification: "team_shared"}); err != nil {
			t.Fatalf("UpsertSyncState a: %v", err)
		}
		// Mapping the SAME node id to a DIFFERENT unit must fail rather than
		// silently re-homing the node.
		if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: b.ID, NodeID: "shared-node", SyncedVersion: 1, Classification: "team_shared"}); err == nil {
			t.Fatalf("UpsertSyncState b: expected node_id collision error, got nil")
		}
	})

	t.Run("delete", func(t *testing.T) {
		u := seedUnit(t, m, units.ClassTeam)
		if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "del-node", SyncedVersion: 1, Classification: "team_shared"}); err != nil {
			t.Fatalf("UpsertSyncState: %v", err)
		}
		if err := m.DeleteSyncState(ctx, u.ID); err != nil {
			t.Fatalf("DeleteSyncState: %v", err)
		}
		if _, err := m.GetSyncState(ctx, u.ID); !errors.Is(err, units.ErrSyncStateNotFound) {
			t.Fatalf("after delete: got %v, want ErrSyncStateNotFound", err)
		}
	})

	t.Run("last_synced_defaults_now", func(t *testing.T) {
		u := seedUnit(t, m, units.ClassTeam)
		before := time.Now().UTC().Add(-time.Second)
		got, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: u.ID, NodeID: "ls-" + u.ID, SyncedVersion: 1, Classification: "team_shared"})
		if err != nil {
			t.Fatalf("UpsertSyncState: %v", err)
		}
		if got.LastSynced.Before(before) {
			t.Fatalf("LastSynced not defaulted to now: %v", got.LastSynced)
		}
	})
}

func TestSyncState_Mem(t *testing.T) {
	t.Parallel()
	runSyncStateSuite(t, units.NewMemoryStore())
}

func TestSyncState_SQL(t *testing.T) {
	t.Parallel()
	runSyncStateSuite(t, units.NewSQLStore(openTestDB(t)))
}

// TestListDirty verifies the push worklist: never-synced + version-ahead
// units are dirty; synced-and-unchanged units are not; personal units are
// never returned.
func TestListDirty(t *testing.T) {
	t.Parallel()
	m := units.NewManager(units.NewMemoryStore())
	ctx := context.Background()

	// team unit, never synced → dirty.
	a := seedUnit(t, m, units.ClassTeam)
	// team unit, synced at current version → clean.
	b := seedUnit(t, m, units.ClassTeam)
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: b.ID, NodeID: "node-b", SyncedVersion: b.Version, Classification: "team_shared"}); err != nil {
		t.Fatalf("UpsertSyncState b: %v", err)
	}
	// team unit, synced then edited → dirty (version ahead).
	c := seedUnit(t, m, units.ClassTeam)
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: c.ID, NodeID: "node-c", SyncedVersion: c.Version, Classification: "team_shared"}); err != nil {
		t.Fatalf("UpsertSyncState c: %v", err)
	}
	if _, err := m.Update(ctx, c.ID, "edited", nil); err != nil {
		t.Fatalf("Update c: %v", err)
	}
	// personal unit, never synced → must NEVER appear.
	_ = seedUnit(t, m, units.ClassPersonal)

	dirty, err := m.ListDirty(ctx, units.ClassTeam)
	if err != nil {
		t.Fatalf("ListDirty: %v", err)
	}
	got := map[string]bool{}
	for _, u := range dirty {
		got[u.ID] = true
	}
	if !got[a.ID] {
		t.Errorf("expected never-synced unit %q dirty", a.ID)
	}
	if got[b.ID] {
		t.Errorf("synced-unchanged unit %q must not be dirty", b.ID)
	}
	if !got[c.ID] {
		t.Errorf("expected version-ahead unit %q dirty", c.ID)
	}

	// ListDirty(personal) is always empty — personal units never sync.
	pd, err := m.ListDirty(ctx, units.ClassPersonal)
	if err != nil {
		t.Fatalf("ListDirty personal: %v", err)
	}
	if len(pd) != 0 {
		t.Errorf("ListDirty(personal) = %d, want 0", len(pd))
	}
}
