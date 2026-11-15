package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// The index holds only the signed-in member's OWN nodes (no teammates'
// owner ids, no titles, no bodies), survives a restart, and a tombstone or
// lane change removes a row.
func TestPulledShareLocations_OwnRowsOnlyPersistedAndPruned(t *testing.T) {
	const own = "6b2f6f0e-0000-4000-8000-0000000000d1"
	const theirs = "6b2f6f0e-0000-4000-8000-0000000000d2"
	n := pulledNode(own, "guidance", "")
	n.Classification, n.TeamID, n.OwnerUserID, n.Title, n.Body = ClassTeamShared, "be-id", "user-a", "Secret plan title", "body text"
	m := pulledNode(theirs, "guidance", "")
	m.Classification, m.TeamID, m.OwnerUserID = ClassTeamShared, "be-id", "user-teammate"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n, m}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	newSyncer := func() *ContextGraphSyncer {
		return NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t)).
			WithSelfUserID(func() string { return "user-a" })
	}
	ctx := context.Background()
	s := newSyncer()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	got := newSyncer().PulledShareLocations()
	if len(got) != 1 || got[0].NodeID != own || got[0].OwnerUserID != "user-a" || got[0].Layer != contextpack.LayerTeam || got[0].TeamID != "be-id" {
		t.Fatalf("after restart = %+v, want only the own row", got)
	}
	raw, err := os.ReadFile(pulledLocationsPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Secret plan", "body text", "user-teammate", theirs} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("index stores %q: %s", leak, raw)
		}
	}
	if fi, _ := os.Stat(pulledLocationsPath(dataDir)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", fi.Mode().Perm())
	}

	srv.mu.Lock()
	srv.page.Nodes = []ContextPulledNode{n}
	srv.page.Nodes[0].Kind, srv.page.Nodes[0].UnitKind, srv.page.Cursor = "doc", "doc", "c2"
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.PulledShareLocations(); len(got) != 0 {
		t.Fatalf("lane change kept %+v", got)
	}

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

// An accepted push records where the node is now; a conflicted one does not.
func TestPulledShareLocations_AcceptedPushUpdatesRow(t *testing.T) {
	conflict := false
	var mu sync.Mutex
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		c := conflict
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if c {
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted_nodes": 1, "accepted_edges": 0,
				"conflicts": []any{map[string]any{"node_id": req.Nodes[0].ID, "server_version": 3, "client_version": 1}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted_nodes": 1, "accepted_edges": 0, "conflicts": []any{}})
	}))
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewContextGraphSyncer(makeTestClient(t, hs.URL), t.TempDir(), makeCapPollerWithTeamCap(t)).
		WithSelfUserID(func() string { return "user-a" })
	push := func(team string) {
		t.Helper()
		if _, err := s.PushEntry(context.Background(), ContextNodeEntry{ID: "notes.md", Layer: contextpack.LayerTeam,
			Kind: "guidance", Title: "notes", Body: "b", Version: 1, TeamID: &team}, nil); err != nil {
			t.Fatal(err)
		}
	}
	push("fe-id")
	got := s.PulledShareLocations()
	if len(got) != 1 || got[0].NodeID != s.WireNodeID("notes.md") || got[0].TeamID != "fe-id" {
		t.Fatalf("after accepted push = %+v", got)
	}
	mu.Lock()
	conflict = true
	mu.Unlock()
	push("be-id")
	if got := s.PulledShareLocations(); len(got) != 1 || got[0].TeamID != "fe-id" {
		t.Fatalf("a conflicted push moved the row: %+v", got)
	}
}

// pagedPullServer serves a full pull in pages: since="" → page 1 (1000
// nodes), since="p1" → page 2 (failing the first time when fail2 is set).
type pagedPullServer struct {
	mu      sync.Mutex
	page1   []ContextPulledNode
	page2   []ContextPulledNode
	fail2   bool
	queries []string
}

func (s *pagedPullServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, r.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Query().Get("since") {
	case "":
		_ = json.NewEncoder(w).Encode(contextPullResponse{Nodes: s.page1, Cursor: "p1"})
	case "p1":
		if s.fail2 { // every attempt fails until the test clears it
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(contextPullResponse{Nodes: s.page2, Cursor: "p2"})
	default:
		_ = json.NewEncoder(w).Encode(contextPullResponse{})
	}
}

// A full pull spans pages: a full page 1 must not prune, an error on page 2
// must not prune, and the short page 2 prunes with every id seen on BOTH
// pages.
func TestContextPull_MultiPageFullPullPrunesOnlyAtTheEnd(t *testing.T) {
	ownNode := func(id string) ContextPulledNode {
		n := pulledNode(id, "guidance", "")
		n.Classification, n.TeamID, n.OwnerUserID = ClassTeamShared, "be-id", "user-a"
		return n
	}
	idOf := func(i int) string { return "6b2f6f0e-0000-4000-8000-" + strings.Repeat("0", 12-len(itoa(i))) + itoa(i) }
	const stale = "6b2f6f0e-0000-4000-8000-ffffffffffff"
	srv := &pagedPullServer{fail2: true}
	for i := 0; i < curatedFullPullPage; i++ {
		srv.page1 = append(srv.page1, ownNode(idOf(i)))
	}
	srv.page2 = []ContextPulledNode{ownNode(idOf(5000))}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	// The last run left a row for a node this member can no longer see.
	if err := os.MkdirAll(dataDir+"/fleet", 0o700); err != nil {
		t.Fatal(err)
	}
	seed, _ := json.Marshal(pulledLocationsFile{Version: pulledLocationsVersion, UserID: "user-a",
		Nodes: map[string]PulledShareLocation{stale: {Layer: contextpack.LayerTeam, TeamID: "be-id", Version: 1}}})
	if err := os.WriteFile(pulledLocationsPath(dataDir), seed, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t)).
		WithSelfUserID(func() string { return "user-a" })
	has := func(id string) bool {
		for _, l := range s.PulledShareLocations() {
			if l.NodeID == id {
				return true
			}
		}
		return false
	}
	ctx := context.Background()

	if _, err := s.PullDelta(ctx); err != nil { // page 1: full page
		t.Fatal(err)
	}
	if !has(stale) {
		t.Fatal("a full page 1 pruned the index before the full pull ended")
	}
	if _, err := s.PullDelta(ctx); err == nil { // page 2 fails
		t.Fatal("page 2: want the injected error")
	}
	if !has(stale) {
		t.Fatal("a failed page 2 pruned the index")
	}
	srv.mu.Lock()
	srv.fail2 = false
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil { // page 2: short, the end
		t.Fatal(err)
	}
	if has(stale) {
		t.Fatal("the end of the full pull kept a row it never saw")
	}
	if !has(idOf(0)) || !has(idOf(999)) || !has(idOf(5000)) {
		t.Fatalf("pruned rows the full pull saw (page 1 or 2): %d rows", len(s.PulledShareLocations()))
	}
	srv.mu.Lock()
	q := append([]string(nil), srv.queries...)
	srv.mu.Unlock()
	if strings.Contains(q[0], "since=") || !strings.Contains(q[len(q)-1], "since=p1") {
		t.Fatalf("queries = %v", q)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// Another account signing in within the process gets a full pull and none
// of the previous account's rows or entries.
func TestContextPull_AccountSwitchRestartsFullPull(t *testing.T) {
	n := pulledNode("6b2f6f0e-0000-4000-8000-0000000000d9", "guidance", "")
	n.Classification, n.TeamID, n.OwnerUserID = ClassTeamShared, "be-id", "user-a"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: "c1"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	self := "user-a"
	s := NewContextGraphSyncer(makeTestClient(t, hs.URL), t.TempDir(), makeCapPollerWithTeamCap(t)).
		WithSelfUserID(func() string { return self })
	ctx := context.Background()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.PulledShareLocations()) != 1 || len(s.PulledEntries()) != 1 {
		t.Fatal("setup")
	}
	self = "user-b"
	srv.mu.Lock()
	srv.page = contextPullResponse{}
	srv.mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv); strings.Contains(q, "since=") {
		t.Fatalf("pull after an account switch %q, want a full pull", q)
	}
	if got := s.PulledShareLocations(); len(got) != 0 {
		t.Fatalf("previous account's rows kept: %+v", got)
	}
	if got := s.PulledEntries(); len(got) != 0 {
		t.Fatalf("previous account's entries kept: %+v", got)
	}
}

// Rows are ordered by Fleet's node version, never by time: an admin move on
// Fleet (higher version) replaces this install's push row even when Fleet's
// updated_at is EARLIER than this device's (fast) clock; an older version
// never replaces it.
func TestPulledShareLocations_VersionOrdersRowsNotClock(t *testing.T) {
	var mu sync.Mutex
	var page contextPullResponse
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted_nodes": 1, "accepted_edges": 0, "conflicts": []any{}})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	dataDir := t.TempDir()
	newSyncer := func() *ContextGraphSyncer {
		return NewContextGraphSyncer(makeTestClient(t, hs.URL), dataDir, makeCapPollerWithTeamCap(t)).
			WithSelfUserID(func() string { return "user-a" })
	}
	s := newSyncer()
	ctx := context.Background()
	fe := "fe-id"
	if _, err := s.PushEntry(ctx, ContextNodeEntry{ID: "notes.md", Layer: contextpack.LayerTeam, Kind: "guidance",
		Title: "notes", Body: "b", Version: 1, TeamID: &fe}, nil); err != nil {
		t.Fatal(err)
	}
	wire := s.WireNodeID("notes.md")
	node := func(team string, version int) ContextPulledNode {
		n := pulledNode(wire, "guidance", "")
		n.Classification, n.TeamID, n.OwnerUserID, n.Version = ClassTeamShared, team, "user-a", version
		// Fleet's clock reads 2 minutes behind this device's.
		n.UpdatedAt = time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
		return n
	}
	teamOf := func(s *ContextGraphSyncer) string {
		for _, l := range s.PulledShareLocations() {
			if l.NodeID == wire {
				return l.TeamID
			}
		}
		return ""
	}
	// An older copy (version 0) never replaces the push row.
	mu.Lock()
	page = contextPullResponse{Nodes: []ContextPulledNode{node("old-id", 0)}, Cursor: "c1"}
	mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := teamOf(s); got != "fe-id" {
		t.Fatalf("older version replaced the push row: %q", got)
	}
	// The admin move: version 2, earlier timestamp. It wins, and still
	// does after a restart's full pull.
	mu.Lock()
	page = contextPullResponse{Nodes: []ContextPulledNode{node("be-id", 2)}, Cursor: "c2"}
	mu.Unlock()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := teamOf(s); got != "be-id" {
		t.Fatalf("after the admin move = %q, want be-id", got)
	}
	s2 := newSyncer()
	if _, err := s2.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if got := teamOf(s2); got != "be-id" {
		t.Fatalf("after restart = %q, want be-id", got)
	}
}
