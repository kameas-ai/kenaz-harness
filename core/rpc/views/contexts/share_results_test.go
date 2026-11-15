package contexts_test

// share_results_test.go — kenaz-fleet spec 018 H-3 through Context_Publish:
// the explicit audience on the wire, Fleet's per-item results mapped to
// published / waiting for review / refused, and the share ledger recording
// only `published` while it tracks pending reviews by merge_request_id.

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

// resultsPush answers push with a per-item results[] row built by reply and
// records each pushed node. Race-safe.
type resultsPush struct {
	mu    sync.Mutex
	nodes []map[string]any
	reply func(id string) map[string]any
}

func (s *resultsPush) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/context/push" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Nodes []map[string]any `json:"nodes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id := ""
	if len(req.Nodes) > 0 {
		id, _ = req.Nodes[0]["id"].(string)
	}
	s.mu.Lock()
	s.nodes = append(s.nodes, req.Nodes...)
	reply := s.reply
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply(id))
}

func (s *resultsPush) set(reply func(id string) map[string]any) {
	s.mu.Lock()
	s.reply = reply
	s.mu.Unlock()
}

func (s *resultsPush) last() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodes[len(s.nodes)-1]
}

func itemReply(status, reason, mr string) func(id string) map[string]any {
	return func(id string) map[string]any {
		row := map[string]any{"id": id, "status": status, "reason": reason, "merge_request_id": nil}
		if mr != "" {
			row["merge_request_id"] = mr
		}
		acc := 0
		if status == "published" {
			acc = 1
		}
		return map[string]any{"accepted_nodes": acc, "accepted_edges": 0, "conflicts": []any{}, "results": []any{row}}
	}
}

func resultsRig(t *testing.T) (*contextsview.API, *resultsPush) {
	t.Helper()
	push := &resultsPush{reply: itemReply("published", "", "")}
	srv := httptest.NewServer(push)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })
	client := corefleet.NewClientForTesting(srv.URL)
	poller := corefleet.NewCapabilityPoller(client, t.TempDir())
	poller.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier: "enterprise", Enabled: map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true}, FetchedAt: time.Now(),
	})
	syncer := corefleet.NewContextGraphSyncer(client, t.TempDir(), poller).
		WithMeTeams(func(context.Context) ([]corefleet.MeTeam, error) {
			return []corefleet.MeTeam{{ID: "team-root", IsDefault: true}, {ID: "team-backend"}}, nil
		})
	api := contextsview.New(nil).WithSyncer(syncer).
		WithSelfUserID(func() string { return "user-a" }).WithShareLedgerDir(t.TempDir())
	return api, push
}

func TestPublish_ExplicitAudienceOnTheWire(t *testing.T) {
	api, push := resultsRig(t)
	ctx := context.Background()
	cases := []struct {
		layer, team string
		audience    string
		governed    any
		class       string
	}{
		{"team", "team-backend", "team", nil, "team_shared"},
		{"team", "team-root", "org", false, "team_shared"}, // the default team IS the org
		{"org", "", "org", true, "org_shared"},
	}
	for _, c := range cases {
		res, err := api.Context_Publish(ctx, publishReq("notes/"+c.layer+c.team+".md", c.layer, c.team))
		if err != nil {
			t.Fatal(err)
		}
		n := push.last()
		if n["audience"] != c.audience || n["governed"] != c.governed || n["classification"] != c.class {
			t.Fatalf("%s/%s on the wire: %v", c.layer, c.team, n)
		}
		if res.Audience != c.audience || res.Status != "published" {
			t.Fatalf("%s/%s result: %+v", c.layer, c.team, res)
		}
	}
}

func TestPublish_ResultsMapToStatusAndPlainWords(t *testing.T) {
	api, push := resultsRig(t)
	ctx := context.Background()
	for _, c := range []struct{ reason, text string }{
		{"team_not_found", "That team no longer exists"},
		{"previously_rejected", "This exact text was already rejected"},
		{"governed_requires_admin", "Only an org admin can publish this org-wide"},
	} {
		push.set(itemReply("refused", c.reason, ""))
		res, err := api.Context_Publish(ctx, publishReq("notes/"+c.reason+".md", "org", ""))
		if err != nil {
			t.Fatalf("%s: a per-item refusal is an outcome, not an error: %v", c.reason, err)
		}
		if res.Status != "refused" || res.Reason != c.reason || res.ReasonText != c.text {
			t.Fatalf("%s: %+v", c.reason, res)
		}
		if got := shareStates(t, api, "notes/"+c.reason+".md"); len(got) != 0 {
			t.Fatalf("%s: a refused share was recorded: %+v", c.reason, got)
		}
	}
}

func TestPublish_LedgerOnlyOnPublished_PendingTrackedByMergeRequest(t *testing.T) {
	api, push := resultsRig(t)
	ctx := context.Background()
	const path = "notes/policy.md"

	// Shared with a team first: recorded.
	if _, err := api.Context_Publish(ctx, publishReq(path, "team", "team-backend")); err != nil {
		t.Fatal(err)
	}
	if got := shareStates(t, api, path); len(got) != 1 || got[0].TeamID != "team-backend" || got[0].PendingMergeRequestID != "" {
		t.Fatalf("after team share: %+v", got)
	}

	// Org-wide by a member: waiting for review. The ledger's share record
	// stays on the team (nothing was written), the request is tracked.
	push.set(itemReply("pending_review", "", "mr-1"))
	res, err := api.Context_Publish(ctx, publishReq(path, "org", ""))
	if err != nil || res.Status != "pending_review" || res.MergeRequestID != "mr-1" || res.SupersededMergeRequestID != "" {
		t.Fatalf("pending: %+v %v", res, err)
	}
	got := shareStates(t, api, path)
	if len(got) != 1 || got[0].Layer != "team" || got[0].TeamID != "team-backend" || got[0].PendingMergeRequestID != "mr-1" {
		t.Fatalf("after pending: %+v", got)
	}

	// Same content again: the same id, nothing superseded.
	res, _ = api.Context_Publish(ctx, publishReq(path, "org", ""))
	if res.MergeRequestID != "mr-1" || res.SupersededMergeRequestID != "" {
		t.Fatalf("idempotent pending: %+v", res)
	}

	// Changed content: Fleet supersedes mr-1 and answers mr-2.
	push.set(itemReply("pending_review", "", "mr-2"))
	res, _ = api.Context_Publish(ctx, publishReq(path, "org", ""))
	if res.MergeRequestID != "mr-2" || res.SupersededMergeRequestID != "mr-1" {
		t.Fatalf("superseded: %+v", res)
	}
	if got := shareStates(t, api, path); got[0].PendingMergeRequestID != "mr-2" {
		t.Fatalf("pending id not replaced: %+v", got)
	}

	// An admin's publish (or an accepted review) lands: recorded, pending cleared.
	push.set(itemReply("published", "", ""))
	if _, err := api.Context_Publish(ctx, publishReq(path, "org", "")); err != nil {
		t.Fatal(err)
	}
	got = shareStates(t, api, path)
	if len(got) != 1 || got[0].Layer != "org" || got[0].PendingMergeRequestID != "" {
		t.Fatalf("after published: %+v", got)
	}
}

func TestPublish_PreviouslyRejectedClearsPending(t *testing.T) {
	api, push := resultsRig(t)
	ctx := context.Background()
	const path = "notes/again.md"
	if _, err := api.Context_Publish(ctx, publishReq(path, "team", "team-backend")); err != nil {
		t.Fatal(err)
	}
	push.set(itemReply("pending_review", "", "mr-9"))
	if _, err := api.Context_Publish(ctx, publishReq(path, "org", "")); err != nil {
		t.Fatal(err)
	}
	push.set(itemReply("refused", "previously_rejected", ""))
	if _, err := api.Context_Publish(ctx, publishReq(path, "org", "")); err != nil {
		t.Fatal(err)
	}
	// The pending request is forgotten; the team share stands.
	if got := shareStates(t, api, path); len(got) != 1 || got[0].PendingMergeRequestID != "" || got[0].TeamID != "team-backend" {
		t.Fatalf("after previously_rejected: %+v", got)
	}
}
