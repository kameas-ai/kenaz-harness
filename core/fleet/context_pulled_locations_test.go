package fleet

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// The Curated pull keeps where Fleet holds each node (layer + team_id) on
// disk: it survives a restart, a tombstone or a lane change removes it, and
// the file holds no title text or body.
func TestPulledShareLocations_PersistedAndPruned(t *testing.T) {
	const id = "6b2f6f0e-0000-4000-8000-0000000000d1"
	n := pulledNode(id, "guidance", "")
	n.Classification, n.TeamID, n.OwnerUserID, n.Title, n.Body = ClassTeamShared, "be-id", "user-a", "Secret plan title", "body text"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	newSyncer := func() *ContextGraphSyncer {
		return NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t))
	}
	ctx := context.Background()
	s := newSyncer()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	want := PulledShareLocation{NodeID: id, OwnerUserID: "user-a", Layer: contextpack.LayerTeam, TeamID: "be-id",
		TitleKey: CuratedTitleKey("Secret plan title")}
	if got := newSyncer().PulledShareLocations(); len(got) != 1 || got[0] != want {
		t.Fatalf("after restart = %+v, want [%+v]", got, want)
	}
	raw, err := os.ReadFile(pulledLocationsPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Secret plan") || strings.Contains(string(raw), "body text") {
		t.Fatalf("index stores content: %s", raw)
	}
	if fi, _ := os.Stat(pulledLocationsPath(dataDir)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", fi.Mode().Perm())
	}

	// Re-pushed into another lane: dropped.
	srv.mu.Lock()
	srv.page.Nodes[0].Kind, srv.page.Nodes[0].UnitKind, srv.page.Cursor = "doc", "doc", "c2"
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := newSyncer().PulledShareLocations(); len(got) != 0 {
		t.Fatalf("lane change kept %+v", got)
	}

	// Back, then tombstoned: dropped.
	del := "2026-11-02T00:00:00Z"
	srv.mu.Lock()
	srv.page.Nodes[0].Kind, srv.page.Nodes[0].UnitKind, srv.page.Cursor = "guidance", "", "c3"
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.PulledShareLocations(); len(got) != 1 {
		t.Fatalf("re-pulled = %+v", got)
	}
	srv.mu.Lock()
	srv.page.Nodes[0].DeletedAt, srv.page.Cursor = &del, "c4"
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := newSyncer().PulledShareLocations(); len(got) != 0 {
		t.Fatalf("tombstone kept %+v", got)
	}
}
