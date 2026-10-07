package fleet

// Review F2 (owner ruling 2026-10-06): personal→team is a straight push at
// team_shared; team→org pushes (ensuring the node exists) then opens the
// merge request; personal→org does both. Driven against a fake fleet that
// records the request order.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

type promoteFleet struct {
	mu    sync.Mutex
	calls []string // "push:<classification>:<node id>" | "mr:<to>:<node id>"
}

func (f *promoteFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/context/push":
		var req struct {
			Nodes []struct {
				ID             string `json:"id"`
				Classification string `json:"classification"`
			} `json:"nodes"`
		}
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		for _, n := range req.Nodes {
			f.calls = append(f.calls, "push:"+n.Classification+":"+n.ID)
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted_nodes": len(req.Nodes), "accepted_edges": 0, "conflicts": []any{}})
	case "/api/v1/context/merge-requests":
		var req struct {
			UnitNodeID       string `json:"unit_node_id"`
			ToClassification string `json:"to_classification"`
		}
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.calls = append(f.calls, "mr:"+req.ToClassification+":"+req.UnitNodeID)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"merge_request": map[string]any{
			"id": "mr-1", "unit_node_id": req.UnitNodeID, "from_classification": "team_shared",
			"to_classification": req.ToClassification, "status": "open",
		}})
	default:
		http.NotFound(w, r)
	}
}

func (f *promoteFleet) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func newPromoteRig(t *testing.T) (*Impl, *units.Manager, *promoteFleet) {
	t.Helper()
	fake := &promoteFleet{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC()})
	if err := corefleet.SaveTokens(corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Skipf("keychain unavailable: %v", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })
	client := corefleet.NewClientForTesting(srv.URL)
	caps := corefleet.NewCapabilityPoller(client, t.TempDir())
	caps.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier: "team", Enabled: map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true},
		FetchedAt: time.Now(), Source: "test",
	})
	m := units.NewManager(units.NewMemoryStore())
	syncer := corefleet.NewUnitSyncer(client, m, corefleet.NewUnitMapper(""), caps, t.TempDir())
	return &Impl{Units: m, Syncer: syncer}, m, fake
}

func TestPromote_PersonalToTeam_IsAStraightPush(t *testing.T) {
	impl, m, fake := newPromoteRig(t)
	ctx := context.Background()
	src, _ := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand, Title: "n", Body: "b"})
	res, err := impl.Unit_PromoteAsMergeRequest(ctx, src.ID, "team", "", "")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 1 || calls[0] != "push:team_shared:"+res.UnitNodeID || res.Status != "published" || res.ID != "" {
		t.Fatalf("calls=%v res=%+v, want one team push and no MR", calls, res)
	}
	if got, _ := m.Get(ctx, src.ID); got.Classification != units.ClassPersonal {
		t.Error("the personal source was modified")
	}
	if !corefleet.IsWireUUID(res.UnitNodeID) {
		t.Errorf("node id %q not a UUID", res.UnitNodeID)
	}
}

func TestPromote_TeamToOrg_PushesThenMR(t *testing.T) {
	impl, m, fake := newPromoteRig(t)
	ctx := context.Background()
	src, _ := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand, Title: "n", Body: "b"})
	res, err := impl.Unit_PromoteAsMergeRequest(ctx, src.ID, "org", "", "")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 2 || calls[0] != "push:team_shared:"+res.UnitNodeID || calls[1] != "mr:org_shared:"+res.UnitNodeID || res.ID != "mr-1" {
		t.Fatalf("calls=%v res=%+v, want push at team THEN MR to org on the same node", calls, res)
	}
}

func TestPromote_PersonalToOrg_PushAtTeamThenMR(t *testing.T) {
	impl, m, fake := newPromoteRig(t)
	ctx := context.Background()
	src, _ := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand, Title: "n", Body: "b"})
	res, err := impl.Unit_PromoteAsMergeRequest(ctx, src.ID, "org", "", "")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 2 || calls[0] != "push:team_shared:"+res.UnitNodeID || calls[1] != "mr:org_shared:"+res.UnitNodeID {
		t.Fatalf("calls=%v", calls)
	}
}
