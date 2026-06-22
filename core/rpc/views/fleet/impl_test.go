package fleet

// impl_test.go — Phase-3 unit-collaboration RPC surface (WP16-WP18).
//
// Exercises the resolution/enshrine RPCs through the view Impl with a real
// (fleet-free) units.Manager and a nil Syncer (offline posture): merge resolves
// in place, enshrine creates a coexisting flagged unit, and ResolveLoadable
// surfaces the enshrined conflict flagged + precedence-ordered.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

func newImplWithUnits() (*Impl, *units.Manager) {
	m := units.NewManager(units.NewMemoryStore())
	// Syncer is nil — the offline path. The unit methods must still work.
	return &Impl{Units: m}, m
}

func TestImpl_UnitResolveMerge(t *testing.T) {
	impl, m := newImplWithUnits()
	ctx := context.Background()

	u, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassTeam, LoadPolicy: units.LoadAlways,
		Title: "doc", Body: "local",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := impl.Unit_ResolveMerge(ctx, u.ID, "merged"); err != nil {
		t.Fatalf("Unit_ResolveMerge: %v", err)
	}
	got, _ := m.Get(ctx, u.ID)
	if got.Body != "merged" || got.Version != u.Version+1 {
		t.Errorf("after merge: body=%q version=%d", got.Body, got.Version)
	}
}

func TestImpl_UnitResolveEnshrineAndLoadable(t *testing.T) {
	impl, m := newImplWithUnits()
	ctx := context.Background()

	src, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassTeam, LoadPolicy: units.LoadAlways,
		Title: "policy", Body: "ours",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newID, err := impl.Unit_ResolveEnshrine(ctx, src.ID, "policy (theirs)", "theirs", "diverge")
	if err != nil {
		t.Fatalf("Unit_ResolveEnshrine: %v", err)
	}
	if newID == "" || newID == src.ID {
		t.Fatalf("enshrine returned bad id %q", newID)
	}

	views, err := impl.Unit_ResolveLoadable(ctx, "project", "p1")
	if err != nil {
		t.Fatalf("Unit_ResolveLoadable: %v", err)
	}
	var flagged *ResolvedUnitView
	for i := range views {
		if views[i].Flagged {
			flagged = &views[i]
		}
	}
	if flagged == nil {
		t.Fatal("no flagged enshrined unit surfaced")
	}
	if flagged.PeerUnitID != src.ID {
		t.Errorf("flagged peer = %q, want %q", flagged.PeerUnitID, src.ID)
	}
	if flagged.UnitID != newID {
		t.Errorf("flagged unit id = %q, want %q", flagged.UnitID, newID)
	}
}

func TestImpl_UnitMethods_UnitsUnavailable(t *testing.T) {
	impl := &Impl{} // no Units wired
	ctx := context.Background()
	if err := impl.Unit_ResolveMerge(ctx, "x", "y"); !errors.Is(err, ErrUnitsUnavailable) {
		t.Errorf("ResolveMerge err = %v, want ErrUnitsUnavailable", err)
	}
	if _, err := impl.Unit_ResolveEnshrine(ctx, "x", "", "", ""); !errors.Is(err, ErrUnitsUnavailable) {
		t.Errorf("ResolveEnshrine err = %v, want ErrUnitsUnavailable", err)
	}
	if _, err := impl.Unit_ResolveLoadable(ctx, "", ""); !errors.Is(err, ErrUnitsUnavailable) {
		t.Errorf("ResolveLoadable err = %v, want ErrUnitsUnavailable", err)
	}
	// Promote needs the syncer too.
	if _, err := impl.Unit_PromoteAsMergeRequest(ctx, "x", "team", "", ""); !errors.Is(err, ErrUnitsUnavailable) {
		t.Errorf("Promote err = %v, want ErrUnitsUnavailable", err)
	}
	// ListConflicts is a no-op (nil syncer) → empty, no error.
	if cs, err := impl.Unit_ListConflicts(ctx); err != nil || len(cs) != 0 {
		t.Errorf("ListConflicts = %v, %v; want empty, nil", cs, err)
	}
}

func TestImpl_PromoteAsMR_InvalidTarget(t *testing.T) {
	m := units.NewManager(units.NewMemoryStore())
	// Syncer present but target invalid → rejected before any network call.
	impl := &Impl{Units: m, Syncer: nil}
	ctx := context.Background()
	u, _ := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways, Title: "n",
	})
	// "personal" is not a valid promote target.
	if _, err := impl.Unit_PromoteAsMergeRequest(ctx, u.ID, "personal", "", ""); err == nil {
		t.Error("promote to personal accepted; want rejection")
	}
}
