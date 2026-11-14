package fleet

// context_pull_restart_test.go — the Curated pulled cache is in memory only,
// so each process's first pull is a FULL pull that rebuilds it. Before this,
// the cursor was persisted and a restart left teammates' entries missing
// until they changed on Fleet.

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestContextPull_RestartRebuildsCacheWithFullPull(t *testing.T) {
	const live = "6b2f6f0e-0000-4000-8000-0000000000e1"
	const gone = "6b2f6f0e-0000-4000-8000-0000000000e2"
	liveNode := pulledNode(live, "guidance", "")
	liveNode.Classification, liveNode.TeamID, liveNode.OwnerUserID = ClassTeamShared, "be-id", "user-z"
	liveNode.UpdatedAt = "2026-11-01T00:00:00Z"
	goneNode := pulledNode(gone, "guidance", "")
	goneNode.Classification, goneNode.TeamID, goneNode.OwnerUserID = ClassTeamShared, "be-id", "user-z"
	del := "2026-11-02T00:00:00Z"
	goneNode.DeletedAt = &del
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{liveNode, goneNode}, Cursor: "2026-11-02T00:00:00Z"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	newSyncer := func() *ContextGraphSyncer {
		return NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t))
	}
	ctx := context.Background()

	s1 := newSyncer()
	if _, err := s1.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s1.PulledEntries(); len(got) != 1 || got[0].ID != live {
		t.Fatalf("first run entries = %+v", got)
	}
	// Second poll in the same process is a delta (Fleet has nothing new).
	srv.mu.Lock()
	srv.page = contextPullResponse{}
	srv.mu.Unlock()
	if _, err := s1.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv); !strings.Contains(q, "since=") {
		t.Fatalf("in-process poll query %q, want a delta (since=)", q)
	}

	// Restart. A leftover cursor file from an older build is ignored and removed.
	if err := os.MkdirAll(dataDir+"/fleet", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contextCursorPath(dataDir), []byte("2026-11-02T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.page = contextPullResponse{Nodes: []ContextPulledNode{liveNode, goneNode}, Cursor: "2026-11-02T00:00:00Z"}
	srv.mu.Unlock()
	s2 := newSyncer()
	if _, err := os.Stat(contextCursorPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("legacy cursor file kept: %v", err)
	}
	if got := s2.PulledEntries(); len(got) != 0 {
		t.Fatalf("before the first pull = %+v (nothing cached on disk)", got)
	}
	if _, err := s2.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv); strings.Contains(q, "since=") {
		t.Fatalf("first pull after restart %q, want a full pull (no since)", q)
	}
	got := s2.PulledEntries()
	if len(got) != 1 || got[0].ID != live || got[0].Body == "" {
		t.Fatalf("after restart entries = %+v, want the live entry back and the tombstone gone", got)
	}
}

// The location index is pruned by the full pull: a node this member can no
// longer see (absent from the full pull) loses its row.
func TestContextPull_FullPullPrunesLocationIndex(t *testing.T) {
	const a = "6b2f6f0e-0000-4000-8000-0000000000e3"
	const b = "6b2f6f0e-0000-4000-8000-0000000000e4"
	mk := func(id string) ContextPulledNode {
		n := pulledNode(id, "guidance", "")
		n.Classification, n.TeamID, n.OwnerUserID = ClassTeamShared, "be-id", "user-a"
		return n
	}
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{mk(a), mk(b)}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	newSyncer := func() *ContextGraphSyncer {
		return NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t)).
			WithSelfUserID(func() string { return "user-a" })
	}
	ctx := context.Background()
	if _, err := newSyncer().PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	// Restart: b is no longer visible (moved to a team this member left).
	srv.mu.Lock()
	srv.page = contextPullResponse{Nodes: []ContextPulledNode{mk(a)}, Cursor: "c2"}
	srv.mu.Unlock()
	s := newSyncer()
	if got := s.PulledShareLocations(); len(got) != 2 {
		t.Fatalf("index before the full pull = %+v, want both rows kept", got)
	}
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	got := newSyncer().PulledShareLocations()
	if len(got) != 1 || got[0].NodeID != a {
		t.Fatalf("index after the full pull = %+v, want only %s", got, a)
	}
}

func lastQuery(s *lanePullServer) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		return ""
	}
	return s.queries[len(s.queries)-1]
}
