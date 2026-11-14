package fleet

// unit_lane_change_policy_test.go — a known pulled node the Unit lane later
// skips (re-pushed with a Curated kind, or now invalid for the local store)
// must stop eager-loading: its sidecar server_load_policy is cleared. Real
// sqlite (CLAUDE.md blind spot #2).

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUnitPull_LaneSkipClearsStaleAlways(t *testing.T) {
	cases := map[string]func(n *ContextPulledNode){
		// Re-pushed with a Curated kind: another lane's node now.
		"curated kind": func(n *ContextPulledNode) { n.Kind, n.UnitKind = "guidance", "doc" },
		// Still a unit-lane kind, but its classification no longer maps
		// to a local one (skipped, not applied).
		"unmapped classification": func(n *ContextPulledNode) { n.Classification = "not-a-classification" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			const id = "6b2f6f0e-0000-4000-8000-0000000000a1"
			n := pulledNode(id, "doc", "doc")
			n.LoadPolicy = "always"
			srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: "c1"}}
			hs := httptest.NewServer(srv)
			defer hs.Close()
			stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
			_, m := openUnitSyncSQLite(t)
			s := NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
			ctx := context.Background()

			if _, err := s.PullDown(ctx); err != nil {
				t.Fatal(err)
			}
			if got := sidecarPolicy(t, m, id); got != "always" {
				t.Fatalf("first sight: ServerLoadPolicy = %q, want always", got)
			}

			srv.mu.Lock()
			next := srv.page.Nodes[0]
			next.Version = 2
			change(&next)
			srv.page.Nodes[0], srv.page.Cursor = next, "c2"
			srv.mu.Unlock()
			if _, err := s.PullDown(ctx); err != nil {
				t.Fatal(err)
			}
			if got := sidecarPolicy(t, m, id); got != "" {
				t.Fatalf("after lane skip: ServerLoadPolicy = %q, want cleared", got)
			}
		})
	}
}

// A node the lane has never seen is still just skipped (no sidecar made).
func TestUnitPull_LaneSkipUnknownNodeNoSidecar(t *testing.T) {
	const id = "6b2f6f0e-0000-4000-8000-0000000000a2"
	n := pulledNode(id, "guidance", "doc")
	n.LoadPolicy = "always"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	_, m := openUnitSyncSQLite(t)
	s := NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.PullDown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetSyncStateByNodeID(context.Background(), id); err == nil {
		t.Fatal("a skipped, never-seen node got a sidecar")
	}
}
