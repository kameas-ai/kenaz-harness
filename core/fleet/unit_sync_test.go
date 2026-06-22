package fleet

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// newUnitTestManager returns a units.Manager backed by the in-memory store.
func newUnitTestManager() *units.Manager {
	return units.NewManager(units.NewMemoryStore())
}

// seedTeamUnit creates a team-classified unit through the manager.
func seedTeamUnit(t *testing.T, m *units.Manager, title, body string) units.Unit {
	t.Helper()
	u, err := m.Create(context.Background(), units.Unit{
		Kind:           units.KindDoc,
		Scope:          units.ScopeProject,
		ScopeID:        "proj-1",
		Classification: units.ClassTeam,
		LoadPolicy:     units.LoadAlways,
		Title:          title,
		Body:           body,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return u
}

// TestUnitSyncer_PushDirty_PersonalNeverPushed verifies the push worklist
// only offers team/org units and records the sidecar baseline; personal
// units never reach the wire (NFR-005).
func TestUnitSyncer_PushDirty_PersonalNeverPushed(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)

	m := newUnitTestManager()
	ctx := context.Background()

	teamU := seedTeamUnit(t, m, "team doc", "shared body")
	// A personal unit that must never be pushed.
	if _, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "s1",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "secret", Body: "do not leak",
	}); err != nil {
		t.Fatalf("Create personal: %v", err)
	}

	syncer := NewUnitSyncer(client, m, NewUnitMapper("team-1"), caps, t.TempDir())

	n, err := syncer.PushDirty(ctx)
	if err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	if n != 1 {
		t.Fatalf("PushDirty accepted %d nodes, want 1 (team only)", n)
	}

	// Exactly one node pushed, and it is the team unit (never the personal one).
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.pushRequests) != 1 {
		t.Fatalf("push requests = %d, want 1", len(fake.pushRequests))
	}
	pushed := fake.pushRequests[0].Nodes
	if len(pushed) != 1 || pushed[0].ID != teamU.ID {
		t.Fatalf("pushed nodes = %+v, want only team unit %q", pushed, teamU.ID)
	}
	if pushed[0].Classification != ClassTeamShared {
		t.Errorf("pushed classification = %q, want team_shared", pushed[0].Classification)
	}

	// Sidecar baseline recorded at the pushed version.
	st, err := m.GetSyncState(ctx, teamU.ID)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if st.SyncedVersion != teamU.Version || st.NodeID != teamU.ID {
		t.Errorf("sidecar = %+v, want synced_version=%d node_id=%q", st, teamU.Version, teamU.ID)
	}
}

// TestUnitSyncer_PushDirty_Idempotent verifies that a second PushDirty with
// no new edits pushes nothing (the sidecar marks the unit clean).
func TestUnitSyncer_PushDirty_Idempotent(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	seedTeamUnit(t, m, "doc", "body")
	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())

	if _, err := syncer.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty 1: %v", err)
	}
	if _, err := syncer.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty 2: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.pushRequests) != 1 {
		t.Fatalf("expected 1 push (second is clean), got %d", len(fake.pushRequests))
	}
}

// TestUnitSyncer_PullDown_CreatesReadLayer verifies a first-sight pulled node
// is created locally as a read layer with the sidecar baseline recorded.
func TestUnitSyncer_PullDown_CreatesReadLayer(t *testing.T) {
	fake := &contextFakeServer{}
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	// Build a node carrying the _unit envelope so scope/load_policy survive.
	meta, _ := json.Marshal(map[string]any{
		unitMetaKey: unitMetaEnvelope{Scope: "project", ScopeID: "proj-x", LoadPolicy: "on_demand"},
	})
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-node-1", Kind: "doc", Title: "Team policy", Body: "v1 body",
			Metadata: meta, Classification: ClassTeamShared, Version: 1, UpdatedAt: updated,
		}},
		Cursor: updated,
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())
	applied, err := syncer.PullDown(ctx)
	if err != nil {
		t.Fatalf("PullDown: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}

	// The local read layer exists, scoped+classified correctly.
	list, err := m.List(ctx, units.UnitFilter{Classification: units.ClassTeam})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("local team units = %d, want 1", len(list))
	}
	got := list[0]
	if got.Body != "v1 body" || got.Scope != units.ScopeProject || got.ScopeID != "proj-x" {
		t.Errorf("read layer fields wrong: %+v", got)
	}
	if got.LoadPolicy != units.LoadOnDemand {
		t.Errorf("load policy = %q, want on_demand", got.LoadPolicy)
	}
	// Sidecar maps server node id → local unit.
	st, err := m.GetSyncStateByNodeID(ctx, "srv-node-1")
	if err != nil {
		t.Fatalf("GetSyncStateByNodeID: %v", err)
	}
	if st.UnitID != got.ID || st.SyncedVersion != 1 {
		t.Errorf("sidecar = %+v, want unit=%q synced=1", st, got.ID)
	}
}

// TestUnitSyncer_PullDown_FastForward verifies a clean server-ahead delta
// (no local edits since sync) fast-forwards the local body and sidecar.
func TestUnitSyncer_PullDown_FastForward(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	// Local unit synced at version 0, node id maps to server.
	local := seedTeamUnit(t, m, "doc", "v0 body")
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: local.ID, NodeID: "srv-ff", SyncedVersion: local.Version, Classification: "team_shared",
	}); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	updated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-ff", Kind: "doc", Title: "doc", Body: "v1 server body",
			Classification: ClassTeamShared, Version: 1, UpdatedAt: updated,
		}},
		Cursor: updated,
	})

	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())
	if _, err := syncer.PullDown(ctx); err != nil {
		t.Fatalf("PullDown: %v", err)
	}

	got, err := m.Get(ctx, local.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "v1 server body" {
		t.Errorf("body = %q, want fast-forwarded server body", got.Body)
	}
	if len(syncer.Conflicts()) != 0 {
		t.Errorf("expected no conflicts on clean fast-forward, got %+v", syncer.Conflicts())
	}
}

// TestUnitSyncer_PullDown_Conflict is the load-bearing read-down conflict
// path: the local clone edited a unit since its last sync AND the server
// advanced the same unit. The syncer must SURFACE the conflict and NOT
// blind-upsert the local body.
func TestUnitSyncer_PullDown_Conflict(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	// Local unit synced at version 0.
	local := seedTeamUnit(t, m, "doc", "base body")
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: local.ID, NodeID: "srv-cf", SyncedVersion: local.Version, Classification: "team_shared",
	}); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	// Local edit AFTER the sync → local.Version (1) > synced (0).
	if _, err := m.Update(ctx, local.ID, "LOCAL edit", nil); err != nil {
		t.Fatalf("Update local: %v", err)
	}

	// Server ALSO advanced the same node to version 2.
	updated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-cf", Kind: "doc", Title: "doc", Body: "SERVER edit",
			Classification: ClassTeamShared, Version: 2, UpdatedAt: updated,
		}},
		Cursor: updated,
	})

	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())
	applied, err := syncer.PullDown(ctx)
	if err != nil {
		t.Fatalf("PullDown: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 (conflict must not apply)", applied)
	}

	// Local body must be untouched (NOT overwritten by the server body).
	got, err := m.Get(ctx, local.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "LOCAL edit" {
		t.Fatalf("local body = %q, want preserved 'LOCAL edit' (no blind upsert)", got.Body)
	}

	// The conflict is surfaced with the right version triple.
	conflicts := syncer.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	c := conflicts[0]
	if c.UnitID != local.ID || c.NodeID != "srv-cf" {
		t.Errorf("conflict ids wrong: %+v", c)
	}
	if c.SyncedVersion != 0 || c.LocalVersion != 1 || c.ServerVersion != 2 {
		t.Errorf("conflict versions = %+v, want synced=0 local=1 server=2", c)
	}
	if syncer.Status().ConflictCount != 1 {
		t.Errorf("status conflict count = %d, want 1", syncer.Status().ConflictCount)
	}
}

// TestUnitSyncer_PullDown_NeverOverwritesPersonal verifies that even if a
// server node id somehow maps to a local personal unit, PullDown leaves it
// untouched (defensive guard for NFR-005).
func TestUnitSyncer_PullDown_NeverOverwritesPersonal(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	personal, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "s",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "mine", Body: "PRIVATE",
	})
	if err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: personal.ID, NodeID: "srv-personal", SyncedVersion: 0, Classification: "team_shared",
	}); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	updated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{{
			ID: "srv-personal", Kind: "doc", Title: "mine", Body: "OVERWRITE ATTEMPT",
			Classification: ClassTeamShared, Version: 9, UpdatedAt: updated,
		}},
		Cursor: updated,
	})

	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())
	if _, err := syncer.PullDown(ctx); err != nil {
		t.Fatalf("PullDown: %v", err)
	}
	got, err := m.Get(ctx, personal.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != "PRIVATE" {
		t.Fatalf("personal body = %q, want untouched 'PRIVATE'", got.Body)
	}
}

// TestUnitSyncer_PushDirty_NoCapIsLocalOnly verifies the syncer is a no-op
// (no error, no network) when the team-graph capability is absent.
func TestUnitSyncer_PushDirty_NoCapIsLocalOnly(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithNoCap(t)
	m := newUnitTestManager()
	ctx := context.Background()

	seedTeamUnit(t, m, "doc", "body")
	syncer := NewUnitSyncer(client, m, NewUnitMapper(""), caps, t.TempDir())
	n, err := syncer.PushDirty(ctx)
	if err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	if n != 0 {
		t.Fatalf("PushDirty = %d, want 0 (no cap → local-only)", n)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.pushRequests) != 0 {
		t.Fatalf("no-cap push must not hit the network, got %d requests", len(fake.pushRequests))
	}
}
