package fleet

// unit_server_load_policy_test.go — the sync sidecar records the load_policy
// Fleet holds for a node (ServerLoadPolicy), on every pull and every push.
// The eager always-load path trusts only this, so an admin demotion or a
// non-admin's downgraded push must reach it.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

func sidecarPolicy(t *testing.T, m *units.Manager, nodeID string) string {
	t.Helper()
	st, err := m.GetSyncStateByNodeID(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("sidecar %s: %v", nodeID, err)
	}
	return st.ServerLoadPolicy
}

func TestUnitPull_RecordsServerLoadPolicyEveryPull(t *testing.T) {
	const id = "6b2f6f0e-0000-4000-8000-0000000000f1"
	n := pulledNode(id, "doc", "doc")
	n.LoadPolicy = "always"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := newUnitTestManager()
	s := NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
	ctx := context.Background()

	if _, err := s.PullDown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sidecarPolicy(t, m, id); got != "always" {
		t.Fatalf("first sight: ServerLoadPolicy = %q, want always", got)
	}

	// v2 fast-forward demoted to on_demand.
	srv.mu.Lock()
	srv.page.Nodes[0].Version, srv.page.Nodes[0].LoadPolicy, srv.page.Cursor = 2, "", "c2"
	srv.mu.Unlock()
	if _, err := s.PullDown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sidecarPolicy(t, m, id); got != "on_demand" {
		t.Fatalf("fast-forward: ServerLoadPolicy = %q, want on_demand", got)
	}

	// Same version, policy flipped back (nothing else applied): still recorded.
	srv.mu.Lock()
	srv.page.Nodes[0].LoadPolicy, srv.page.Cursor = "always", "c3"
	srv.mu.Unlock()
	if applied, err := s.PullDown(ctx); err != nil || applied != 0 {
		t.Fatalf("no-change pull: applied=%d err=%v", applied, err)
	}
	if got := sidecarPolicy(t, m, id); got != "always" {
		t.Fatalf("no-change pull: ServerLoadPolicy = %q, want always", got)
	}
}

func TestUnitPush_RecordsWireLoadPolicy(t *testing.T) {
	srv := &lanePullServer{}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := newUnitTestManager()
	ctx := context.Background()
	u, err := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam,
		LoadPolicy: units.LoadAlways, Title: "mine", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	mapper := NewUnitMapper("")
	mapper.SetRoleCheck(func() (bool, bool) { return false, true }) // known non-admin
	s := NewUnitSyncer(makeTestClient(t, hs.URL), m, mapper, makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	st, err := m.GetSyncState(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerLoadPolicy != "on_demand" {
		t.Errorf("non-admin push of an always unit: ServerLoadPolicy = %q, want on_demand (what Fleet holds)", st.ServerLoadPolicy)
	}
}

// An owner's edit of a node pulled with a bootstrap / legacy kind goes back
// with that kind and unit_kind=doc, never retyped as kind=doc.
func TestMapUnitToNode_KeepsBootstrapLegacyKind(t *testing.T) {
	m := NewUnitMapper("")
	for _, k := range []string{"project", "directive", "doc-ref"} {
		u, ok, err := m.PulledNodeToUnit(pulledNode("6b2f6f0e-0000-4000-8000-0000000000f2", k, ""))
		if err != nil || !ok {
			t.Fatalf("map %s: %v", k, err)
		}
		u.Classification = units.ClassTeam
		node, ok, err := m.MapUnitToNode(u)
		if err != nil || !ok {
			t.Fatalf("push map %s: %v", k, err)
		}
		if node.Kind != k || node.UnitKind != "doc" {
			t.Errorf("kind %s pushed as %s/%s, want %s/doc", k, node.Kind, node.UnitKind, k)
		}
	}
	// A label that is not a bootstrap / legacy kind is ignored.
	for _, label := range []string{"skill", "guidance", "root"} {
		md, _ := json.Marshal(map[string]string{FleetKindMetaKey: label})
		node, _, err := m.MapUnitToNode(units.Unit{ID: "u", Kind: units.KindDoc, Scope: units.ScopeGlobal,
			Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand, Metadata: md})
		if err != nil || node.Kind != "doc" {
			t.Errorf("label %q: kind %q err %v, want doc", label, node.Kind, err)
		}
	}
}
