package contexts_test

// share_states_pulled_test.go — the share dialogs start on the team Fleet
// says an own node is shared with now (team_id on every pulled node),
// including a node shared from another device; the local ledger is the
// fallback and is corrected when Fleet disagrees.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

type pullPushServer struct {
	mu    sync.Mutex
	nodes []map[string]any
}

func (s *pullPushServer) setNodes(n ...map[string]any) { s.mu.Lock(); s.nodes = n; s.mu.Unlock() }

func (s *pullPushServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/pull":
		s.mu.Lock()
		nodes := s.nodes
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes, "edges": []any{}, "cursor": time.Now().UTC().Format(time.RFC3339Nano)})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted_nodes": 1, "accepted_edges": 0, "conflicts": []any{}})
	default:
		http.NotFound(w, r)
	}
}

func curatedNode(id, owner, title, class, team string) map[string]any {
	return map[string]any{
		"id": id, "org_id": "o", "team_id": team, "owner_user_id": owner, "kind": "guidance",
		"title": title, "body": "b", "classification": class, "version": 1,
		"created_at": "2026-11-01T00:00:00Z", "updated_at": "2026-11-01T00:00:00Z",
	}
}

type pulledRig struct {
	srv       *pullPushServer
	url       string
	syncerDir string
	ledgerDir string
	user      string
}

func newPulledRig(t *testing.T) *pulledRig {
	t.Helper()
	srv := &pullPushServer{}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	corefleet.SeedFleetConfigForTesting(hs.URL, corefleet.FleetConfig{
		Issuer: hs.URL, ClientID: "test", APIBaseURL: hs.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })
	return &pulledRig{srv: srv, url: hs.URL, syncerDir: t.TempDir(), ledgerDir: t.TempDir(), user: "user-a"}
}

// open builds a syncer + API over the rig's data dirs (a fresh process).
func (r *pulledRig) open(t *testing.T) (*corefleet.ContextGraphSyncer, *contextsview.API) {
	t.Helper()
	client := corefleet.NewClientForTesting(r.url)
	poller := corefleet.NewCapabilityPoller(client, t.TempDir())
	poller.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier: "enterprise", Enabled: map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true}, FetchedAt: time.Now(),
	})
	syncer := corefleet.NewContextGraphSyncer(client, r.syncerDir, poller)
	api := contextsview.New(nil).WithSyncer(syncer).
		WithSelfUserID(func() string { return r.user }).WithShareLedgerDir(r.ledgerDir)
	return syncer, api
}

func pull(t *testing.T, s *corefleet.ContextGraphSyncer) {
	t.Helper()
	if _, err := s.PullDelta(context.Background()); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
}

const otherDeviceNode = "6b2f6f0e-0000-4000-8000-0000000000b1"

// Shared to Backend from another device: no local ledger entry, yet the
// dialog's state says Backend — and still does after a restart.
func TestShareStates_OwnNodeFromAnotherDevice(t *testing.T) {
	r := newPulledRig(t)
	r.srv.setNodes(curatedNode(otherDeviceNode, "user-a", "notes", "team_shared", "be-id"))
	syncer, api := r.open(t)
	pull(t, syncer)

	want := contextsview.ContextShareStateView{NodeID: "docs/notes.md", Layer: "team", TeamID: "be-id"}
	if got := shareStates(t, api, "docs/notes.md", "docs/other.md"); len(got) != 1 || got[0] != want {
		t.Fatalf("share states = %+v, want [%+v]", got, want)
	}

	// Restart: nothing pulled yet this process, the index answers.
	_, api2 := r.open(t)
	if got := shareStates(t, api2, "docs/notes.md"); len(got) != 1 || got[0] != want {
		t.Fatalf("after restart = %+v, want [%+v]", got, want)
	}

	// Signed in as someone else: not theirs.
	r.user = "user-b"
	if got := shareStates(t, api2, "docs/notes.md"); len(got) != 0 {
		t.Fatalf("other account sees %+v", got)
	}
}

// Fleet and the ledger disagree: Fleet wins, and the ledger is corrected.
func TestShareStates_PulledBeatsLedgerAndUpdatesIt(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	ctx := context.Background()
	if _, err := api.Context_Publish(ctx, publishReq("notes.md", "team", "eng-id")); err != nil {
		t.Fatal(err)
	}
	// Fleet now holds this install's node on Backend (moved elsewhere).
	r.srv.setNodes(curatedNode(syncer.WireNodeID("notes.md"), "user-a", "notes", "team_shared", "be-id"))
	pull(t, syncer)
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("share states = %+v, want be-id from Fleet", got)
	}
	// The ledger alone (same install, pulled index gone) now says Backend too.
	if err := os.Remove(filepath.Join(r.syncerDir, "fleet", "context_pulled_locations.json")); err != nil {
		t.Fatalf("pulled index not persisted: %v", err)
	}
	_, ledgerOnly := r.open(t)
	if got := shareStates(t, ledgerOnly, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("ledger after correction = %+v, want be-id", got)
	}
}

// A teammate's same-titled node, and an ambiguous pair of own nodes, say
// nothing: the ledger (or the enrolled team) decides.
func TestShareStates_IgnoresOthersAndAmbiguous(t *testing.T) {
	r := newPulledRig(t)
	r.srv.setNodes(
		curatedNode("6b2f6f0e-0000-4000-8000-0000000000c1", "user-z", "notes", "team_shared", "be-id"),
		curatedNode("6b2f6f0e-0000-4000-8000-0000000000c2", "user-a", "plan", "team_shared", "be-id"),
		curatedNode("6b2f6f0e-0000-4000-8000-0000000000c3", "user-a", "plan", "team_shared", "eng-id"),
		curatedNode("6b2f6f0e-0000-4000-8000-0000000000c4", "user-a", "wide", "org_shared", "root-id"),
	)
	syncer, api := r.open(t)
	pull(t, syncer)
	got := shareStates(t, api, "notes.md", "plan.md", "wide.md")
	if len(got) != 1 || got[0] != (contextsview.ContextShareStateView{NodeID: "wide.md", Layer: "org"}) {
		t.Fatalf("share states = %+v, want only wide.md as org", got)
	}
}
