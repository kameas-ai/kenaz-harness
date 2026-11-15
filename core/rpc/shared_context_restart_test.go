package rpc

// shared_context_restart_test.go — after a restart, teammates' Curated
// entries reach the model's `shared` tool again once the first pull (a full
// pull) has run, and a tombstoned node stays gone. The source is wired to
// the syncer exactly as api.go does (setCurated(ctxSyncer.PulledEntries)).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

type restartPullServer struct {
	mu      sync.Mutex
	queries []string
}

func (s *restartPullServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/api/v1/context/pull" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.queries = append(s.queries, r.URL.RawQuery)
	s.mu.Unlock()
	node := func(id, title, deleted string) map[string]any {
		n := map[string]any{"id": id, "org_id": "o", "team_id": "be-id", "owner_user_id": "u-other", "kind": "guidance",
			"title": title, "body": "Use the blue lane.", "classification": "team_shared", "version": 1,
			"created_at": "2026-11-01T00:00:00Z", "updated_at": "2026-11-01T00:00:00Z"}
		if deleted != "" {
			n["deleted_at"], n["title"], n["body"] = deleted, "", ""
		}
		return n
	}
	since := r.URL.Query().Get("since")
	nodes := []map[string]any{}
	if since == "" { // a full pull: everything, tombstones included
		nodes = append(nodes,
			node("6b2f6f0e-0000-4000-8000-0000000000f1", "Deploy", ""),
			node("6b2f6f0e-0000-4000-8000-0000000000f2", "", "2026-11-02T00:00:00Z"))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes, "edges": []any{}, "cursor": "2026-11-02T00:00:00Z"})
}

func TestSharedContext_CuratedBackAfterRestart(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	srv := &restartPullServer{}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	corefleet.SeedFleetConfigForTesting(hs.URL, corefleet.FleetConfig{
		Issuer: hs.URL, ClientID: "test", APIBaseURL: hs.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })
	dataDir := t.TempDir()
	newSource := func() (*corefleet.ContextGraphSyncer, *sharedContextSource) {
		client := corefleet.NewClientForTesting(hs.URL)
		poller := corefleet.NewCapabilityPoller(client, t.TempDir())
		poller.ForceSetCurrentForTesting(corefleet.Capabilities{
			Tier: "enterprise", Enabled: map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true}, FetchedAt: time.Now(),
		})
		syncer := corefleet.NewContextGraphSyncer(client, dataDir, poller)
		signed := true
		src := signedInSource(&signed)
		src.setCurated(syncer.PulledEntries, func() string { return "u-self" })
		return syncer, src
	}
	ctx := context.Background()
	listIDs := func(src *sharedContextSource) []string {
		items, err := src.ListShared(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		return ids
	}

	s1, src1 := newSource()
	if _, err := s1.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.PullDelta(ctx); err != nil { // a delta: nothing new
		t.Fatal(err)
	}
	if ids := listIDs(src1); len(ids) != 1 {
		t.Fatalf("first run shared = %v", ids)
	}

	// Restart: the first pull is full, and the entry is back.
	s2, src2 := newSource()
	if _, err := s2.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	ids := listIDs(src2)
	if len(ids) != 1 || ids[0] != "entry:6b2f6f0e-0000-4000-8000-0000000000f1" {
		t.Fatalf("after restart shared = %v, want the live entry only", ids)
	}
	if _, ok, _ := src2.GetShared(ctx, "entry:6b2f6f0e-0000-4000-8000-0000000000f2"); ok {
		t.Fatal("a tombstoned entry is readable after restart")
	}
	srv.mu.Lock()
	last := srv.queries[len(srv.queries)-1]
	srv.mu.Unlock()
	if strings.Contains(last, "since=") {
		t.Fatalf("first pull after restart query %q, want a full pull", last)
	}
}
