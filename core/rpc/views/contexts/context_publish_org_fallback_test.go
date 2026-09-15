package contexts_test

// context_publish_org_fallback_test.go pins the finding #97 fix:
//
//   - Publishing at "team" layer with no team_id resolves to "org"
//     instead of failing (the fleet enroll handler can hand back an empty
//     team_id — see core/rpc/views/contexts/impl.go for the full context).
//   - Context_Publish reports EffectiveLayer honestly: "org" on fallback,
//     "team" when a real team_id carries the request through unchanged.
//
// THROWAWAY: once fleet always returns a real team_id (see the deletion
// trigger documented on Context_Publish in impl.go), the first of these
// two cases stops being reachable and this test (or at least its first
// half) should be deleted alongside the fallback it pins.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

// pushedNode mirrors the wire shape of a single node in the push request
// body (core/fleet/context_graph_sync.go's contextNodeInput) — just the
// fields this test needs to assert on.
type pushedNode struct {
	ID             string  `json:"id"`
	Classification string  `json:"classification"`
	TeamID         *string `json:"team_id"`
}

type pushedRequest struct {
	Nodes []pushedNode `json:"nodes"`
}

// fakePushServer records the last /api/v1/context/push request it saw and
// always accepts it.
type fakePushServer struct {
	mu   sync.Mutex
	last pushedRequest
}

func (s *fakePushServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/context/push" {
		http.NotFound(w, r)
		return
	}
	var req pushedRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.last = req
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted_nodes": len(req.Nodes),
		"accepted_edges": 0,
		"conflicts":      []any{},
	})
}

func (s *fakePushServer) snapshot() pushedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// setupPublishTest wires a contextsview.API with a real ContextGraphSyncer
// pointed at an httptest server, with the shared_team_graph capability
// forced on (mirrors setupUnpublishTest in core/rpc/views/catalog/impl_test.go).
func setupPublishTest(t *testing.T) (*contextsview.API, *fakePushServer) {
	t.Helper()
	fake := &fakePushServer{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "test",
		APIBaseURL: srv.URL,
		FetchedAt:  time.Now().UTC(),
	})
	ts := corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	client := corefleet.NewClientForTesting(srv.URL)
	poller := corefleet.NewCapabilityPoller(client, t.TempDir())
	poller.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier:      "enterprise",
		Enabled:   map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true},
		FetchedAt: time.Now(),
	})
	syncer := corefleet.NewContextGraphSyncer(client, t.TempDir(), poller)
	api := contextsview.New(nil).WithSyncer(syncer)
	return api, fake
}

// TestContextPublish_TeamWithNoTeamID_FallsBackToOrg is the core finding
// #97 regression: a "team" layer publish with no team_id must not fail —
// it must resolve to "org" and say so.
func TestContextPublish_TeamWithNoTeamID_FallsBackToOrg(t *testing.T) {
	api, fake := setupPublishTest(t)

	result, err := api.Context_Publish(context.Background(), contextsview.ContextPublishRequest{
		NodeID:  "node-1",
		Layer:   "team",
		Kind:    "guidance",
		Title:   "House style",
		Body:    "Prefer table-driven tests.",
		Version: 1,
		// TeamID intentionally empty.
	})
	if err != nil {
		t.Fatalf("Context_Publish: %v", err)
	}
	if result.EffectiveLayer != "org" {
		t.Errorf("EffectiveLayer = %q, want %q", result.EffectiveLayer, "org")
	}

	sent := fake.snapshot()
	if len(sent.Nodes) != 1 {
		t.Fatalf("server saw %d nodes, want 1", len(sent.Nodes))
	}
	if sent.Nodes[0].Classification != "org_shared" {
		t.Errorf("classification sent = %q, want org_shared", sent.Nodes[0].Classification)
	}
	if sent.Nodes[0].TeamID != nil {
		t.Errorf("team_id sent = %v, want nil", *sent.Nodes[0].TeamID)
	}
}

// TestContextPublish_TeamWithTeamID_StaysTeam verifies the fallback is
// conditional on an EMPTY team_id — once fleet hands back a real one
// (post-#97 fleet fix), a team publish must still resolve to "team",
// not be forced to "org" by this interim code.
func TestContextPublish_TeamWithTeamID_StaysTeam(t *testing.T) {
	api, fake := setupPublishTest(t)

	result, err := api.Context_Publish(context.Background(), contextsview.ContextPublishRequest{
		NodeID:  "node-2",
		Layer:   "team",
		Kind:    "guidance",
		Title:   "House style",
		Body:    "Prefer table-driven tests.",
		TeamID:  "team-uuid-1234",
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Context_Publish: %v", err)
	}
	if result.EffectiveLayer != "team" {
		t.Errorf("EffectiveLayer = %q, want %q", result.EffectiveLayer, "team")
	}

	sent := fake.snapshot()
	if len(sent.Nodes) != 1 {
		t.Fatalf("server saw %d nodes, want 1", len(sent.Nodes))
	}
	if sent.Nodes[0].Classification != "team_shared" {
		t.Errorf("classification sent = %q, want team_shared", sent.Nodes[0].Classification)
	}
	if sent.Nodes[0].TeamID == nil || *sent.Nodes[0].TeamID != "team-uuid-1234" {
		t.Errorf("team_id sent = %v, want team-uuid-1234", sent.Nodes[0].TeamID)
	}
}

// TestContextPublish_OrgLayer_NeverFallsBack verifies an explicit "org"
// request is untouched by the fallback (it never had a team_id to check).
func TestContextPublish_OrgLayer_NeverFallsBack(t *testing.T) {
	api, fake := setupPublishTest(t)

	result, err := api.Context_Publish(context.Background(), contextsview.ContextPublishRequest{
		NodeID:  "node-3",
		Layer:   "org",
		Kind:    "guidance",
		Title:   "Org-wide policy",
		Body:    "Everyone should know this.",
		Version: 1,
	})
	if err != nil {
		t.Fatalf("Context_Publish: %v", err)
	}
	if result.EffectiveLayer != "org" {
		t.Errorf("EffectiveLayer = %q, want %q", result.EffectiveLayer, "org")
	}

	sent := fake.snapshot()
	if len(sent.Nodes) != 1 || sent.Nodes[0].Classification != "org_shared" {
		t.Errorf("sent = %+v, want a single org_shared node", sent.Nodes)
	}
}
