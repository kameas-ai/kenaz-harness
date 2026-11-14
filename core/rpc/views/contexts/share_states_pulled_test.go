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
	syncer := corefleet.NewContextGraphSyncer(client, r.syncerDir, poller).
		WithSelfUserID(func() string { return r.user })
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

// nodeV sets a node's Fleet version and updated_at.
func nodeV(n map[string]any, version int, updated string) map[string]any {
	n["version"], n["updated_at"] = version, updated
	return n
}

// Fleet moved THIS install's node (same wire id) to Backend from the
// dashboard: no local ledger entry, the state says Backend, after a
// restart too, and never for another account.
func TestShareStates_ExactWireIDNode(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	r.srv.setNodes(curatedNode(syncer.WireNodeID("docs/notes.md"), "user-a", "notes", "team_shared", "be-id"))
	pull(t, syncer)

	want := contextsview.ContextShareStateView{NodeID: "docs/notes.md", Layer: "team", TeamID: "be-id"}
	if got := shareStates(t, api, "docs/notes.md", "docs/other.md"); len(got) != 1 || got[0] != want {
		t.Fatalf("share states = %+v, want [%+v]", got, want)
	}
	_, api2 := r.open(t) // restart, nothing pulled yet
	if got := shareStates(t, api2, "docs/notes.md"); len(got) != 1 || got[0] != want {
		t.Fatalf("after restart = %+v, want [%+v]", got, want)
	}
	r.user = "user-b"
	if got := shareStates(t, api2, "docs/notes.md"); len(got) != 0 {
		t.Fatalf("other account sees %+v", got)
	}
}

// (a) A same-named file shared from ANOTHER device is a different node
// (wire ids are salted per install): a never-shared projB/CLAUDE.md gets no
// team from projA/CLAUDE.md there.
func TestShareStates_SameTitleFromAnotherDeviceSaysNothing(t *testing.T) {
	r := newPulledRig(t)
	r.srv.setNodes(curatedNode(otherDeviceNode, "user-a", "CLAUDE", "team_shared", "be-id"))
	syncer, api := r.open(t)
	pull(t, syncer)
	if got := shareStates(t, api, "projB/CLAUDE.md"); len(got) != 0 {
		t.Fatalf("share states = %+v, want none (a different node)", got)
	}
}

// (b) A fresh publish to Frontend is never rewritten to Backend by another
// device's same-named node.
func TestShareStates_FreshPublishNotRewrittenByTitle(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	if _, err := api.Context_Publish(context.Background(), publishReq("notes.md", "team", "fe-id")); err != nil {
		t.Fatal(err)
	}
	r.srv.setNodes(curatedNode(otherDeviceNode, "user-a", "notes", "team_shared", "be-id"))
	pull(t, syncer)
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("share states = %+v, want fe-id", got)
	}
	r.syncerDirIndexGone(t)
	_, ledgerOnly := r.open(t)
	if got := shareStates(t, ledgerOnly, "notes.md"); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("ledger = %+v, want fe-id (not rewritten)", got)
	}
}

// (c) Right after a publish, a stale pulled row for the same node does not
// override the share that was just made (the accepted push updates the row,
// and an older row never beats a newer ledger record).
func TestShareStates_StaleRowDoesNotBeatFreshPublish(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	wire := syncer.WireNodeID("notes.md")
	r.srv.setNodes(nodeV(curatedNode(wire, "user-a", "notes", "team_shared", "be-id"), 1, "2026-11-01T00:00:00Z"))
	pull(t, syncer)
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("setup = %+v", got)
	}
	if _, err := api.Context_Publish(context.Background(), publishReq("notes.md", "team", "fe-id")); err != nil {
		t.Fatal(err)
	}
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("right after publish = %+v, want fe-id", got)
	}
	// The same stale row arriving again (a re-pull) still loses to the
	// newer ledger record.
	pull(t, syncer)
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("after a stale re-pull = %+v, want fe-id", got)
	}
	// Next launch: the index is rebuilt from a pull whose row for this node
	// is NOT newer (same version) than the ledger record. The ledger wins
	// and is not rewritten.
	r.syncerDirIndexGone(t)
	s2, api2 := r.open(t)
	pull(t, s2)
	if got := shareStates(t, api2, "notes.md"); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("older row vs newer ledger = %+v, want fe-id", got)
	}
}

// A NEWER Fleet row (higher node version: moved on the dashboard after this
// device shared it) wins and corrects the ledger, even though Fleet's
// updated_at is earlier than this device's clock; and still after a
// restart and full pull.
func TestShareStates_NewerRowBeatsLedgerAndUpdatesIt(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	if _, err := api.Context_Publish(context.Background(), publishReq("notes.md", "team", "eng-id")); err != nil {
		t.Fatal(err)
	}
	// Higher version; Fleet's clock behind this device's by 2 minutes.
	earlier := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	r.srv.setNodes(nodeV(curatedNode(syncer.WireNodeID("notes.md"), "user-a", "notes", "team_shared", "be-id"), 2, earlier))
	pull(t, syncer)
	if got := shareStates(t, api, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("share states = %+v, want be-id from Fleet", got)
	}
	s2, api2 := r.open(t) // restart + full pull
	pull(t, s2)
	if got := shareStates(t, api2, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("after restart = %+v, want be-id", got)
	}
	r.syncerDirIndexGone(t)
	_, ledgerOnly := r.open(t)
	if got := shareStates(t, ledgerOnly, "notes.md"); len(got) != 1 || got[0].TeamID != "be-id" {
		t.Fatalf("ledger after correction = %+v, want be-id", got)
	}
}

func (r *pulledRig) syncerDirIndexGone(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.syncerDir, "fleet", "context_pulled_locations.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Teammates' nodes say nothing, even on this install's wire id.
func TestShareStates_IgnoresOthers(t *testing.T) {
	r := newPulledRig(t)
	syncer, api := r.open(t)
	r.srv.setNodes(
		curatedNode(syncer.WireNodeID("notes.md"), "user-z", "notes", "team_shared", "be-id"),
		curatedNode(syncer.WireNodeID("wide.md"), "user-a", "wide", "org_shared", "root-id"),
	)
	pull(t, syncer)
	got := shareStates(t, api, "notes.md", "wide.md")
	if len(got) != 1 || got[0] != (contextsview.ContextShareStateView{NodeID: "wide.md", Layer: "org"}) {
		t.Fatalf("share states = %+v, want only wide.md as org", got)
	}
}
