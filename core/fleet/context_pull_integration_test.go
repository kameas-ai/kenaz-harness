package fleet

// context_pull_integration_test.go — httptest-based tests for WP02 pull-delta.
//
// Mirror of config_pull_integration_test.go: a fake fleet HTTP server serves
// GET /api/v1/context/pull and POST /api/v1/context/push; we verify:
//   - Full backfill (cursor=="") fetches all entries (FR-104).
//   - Delta pull (cursor != "") fetches only newer entries.
//   - Precedence: team entries land in team layer; org entries in org layer.
//   - Cursor is advanced and persisted after each pull.
//
// (fleet-context-graph-sync-01NDFSEX17 WP02)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contextpack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// contextFakeServer is a minimal httptest stub for the context endpoints.
type contextFakeServer struct {
	mu sync.Mutex
	// pullResponses is a queue of responses to serve in order.
	pullResponses []contextPullResponse
	pullIdx       int
	// pushRequests records received push payloads.
	pushRequests []contextPushRequest
}

func (s *contextFakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/context/pull"):
		s.mu.Lock()
		var resp contextPullResponse
		if s.pullIdx < len(s.pullResponses) {
			resp = s.pullResponses[s.pullIdx]
			if s.pullIdx < len(s.pullResponses)-1 {
				s.pullIdx++
			}
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)

	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
		var req contextPushRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.pushRequests = append(s.pushRequests, req)
		s.mu.Unlock()
		resp := ContextPushResult{AcceptedNodes: len(req.Nodes), AcceptedEdges: len(req.Edges)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)

	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/promote":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := ContextPromoteResult{
			Node: ContextPulledNode{
				ID:             "node-promote",
				Classification: ClassOrgShared,
				Kind:           "guidance",
				Title:          "Promoted entry",
				Body:           "now org-shared",
				Version:        2,
				UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)

	default:
		http.NotFound(w, r)
	}
}

func (s *contextFakeServer) addPullResponse(resp contextPullResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pullResponses = append(s.pullResponses, resp)
}

func (s *contextFakeServer) pushedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.pushRequests {
		n += len(r.Nodes)
	}
	return n
}

// makeCapPollerWithTeamCap creates a CapabilityPoller with CapSharedTeamGraph
// pre-populated so capability checks pass in tests.
func makeCapPollerWithTeamCap(t *testing.T) *CapabilityPoller {
	t.Helper()
	p := &CapabilityPoller{
		done: make(chan struct{}),
		current: Capabilities{
			Tier: "team",
			Enabled: map[Capability]bool{
				CapSharedTeamGraph: true,
			},
			FetchedAt: time.Now(),
			Source:    "test",
		},
	}
	return p
}

// TestContextPull_Backfill verifies that a full backfill (no cursor) populates
// the in-memory pulled cache for both team and org layers.
func TestContextPull_Backfill(t *testing.T) {
	fake := &contextFakeServer{}
	teamUpdated := time.Now().UTC().Format(time.RFC3339Nano)
	orgUpdated := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "n1", Kind: "guidance", Title: "Team guide",
				Body: "body1", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: teamUpdated,
			},
			{
				ID: "n2", Kind: "fact", Title: "Org fact",
				Body: "body2", Classification: ClassOrgShared,
				Version: 1, UpdatedAt: orgUpdated,
			},
		},
		Cursor: orgUpdated,
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-ctx",
		RefreshToken: "rt-ctx",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)

	dataDir := t.TempDir()
	syncer := NewContextGraphSyncer(client, dataDir, caps)

	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	if n != 2 {
		t.Errorf("PullDelta returned %d nodes, want 2", n)
	}

	entries := syncer.PulledEntries()
	if len(entries) != 2 {
		t.Fatalf("PulledEntries len=%d, want 2", len(entries))
	}

	// Check layer mapping.
	for _, e := range entries {
		switch e.ID {
		case "n1":
			if e.Layer != contextpack.LayerTeam {
				t.Errorf("n1 layer=%q, want team", e.Layer)
			}
		case "n2":
			if e.Layer != contextpack.LayerOrg {
				t.Errorf("n2 layer=%q, want org", e.Layer)
			}
		default:
			t.Errorf("unexpected entry ID %q", e.ID)
		}
	}

	// Cursor should be advanced.
	st := syncer.Status()
	if st.Cursor == "" {
		t.Error("cursor should be set after pull")
	}
	if st.PullCount != 2 {
		t.Errorf("PullCount=%d, want 2", st.PullCount)
	}
}

// TestContextPull_DeltaOnlySinceNewCursor verifies that a second pull uses
// the cursor from the first and fetches only newer entries.
func TestContextPull_DeltaOnlySinceNewCursor(t *testing.T) {
	fake := &contextFakeServer{}
	firstCursor := "2026-06-08T10:00:00.000000000Z"
	secondCursor := "2026-06-08T11:00:00.000000000Z"

	// First pull: 1 node, cursor A.
	fake.addPullResponse(contextPullResponse{
		Nodes:  []ContextPulledNode{{ID: "n1", Kind: "fact", Title: "first", Body: "b", Classification: ClassTeamShared, Version: 1, UpdatedAt: firstCursor}},
		Cursor: firstCursor,
	})
	// Second pull: 1 new node, cursor B.
	fake.addPullResponse(contextPullResponse{
		Nodes:  []ContextPulledNode{{ID: "n2", Kind: "fact", Title: "second", Body: "b2", Classification: ClassTeamShared, Version: 1, UpdatedAt: secondCursor}},
		Cursor: secondCursor,
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-ctx2",
		RefreshToken: "rt-ctx2",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	dataDir := t.TempDir()
	syncer := NewContextGraphSyncer(client, dataDir, caps)

	ctx := context.Background()
	if n, err := syncer.PullDelta(ctx); err != nil || n != 1 {
		t.Fatalf("first pull: n=%d err=%v", n, err)
	}

	if n, err := syncer.PullDelta(ctx); err != nil || n != 1 {
		t.Fatalf("second pull: n=%d err=%v", n, err)
	}

	// All entries should now be present.
	entries := syncer.PulledEntries()
	if len(entries) != 2 {
		t.Fatalf("PulledEntries len=%d, want 2", len(entries))
	}

	st := syncer.Status()
	if st.Cursor != secondCursor {
		t.Errorf("cursor=%q, want %q", st.Cursor, secondCursor)
	}
}

// TestContextPull_TombstonePropagated verifies that a tombstoned entry (FR-103)
// is removed from PulledEntries.
func TestContextPull_TombstonePropagated(t *testing.T) {
	fake := &contextFakeServer{}
	deletedAt := "2026-06-08T12:00:00Z"

	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{ID: "n1", Kind: "fact", Title: "alive", Body: "b", Classification: ClassTeamShared, Version: 1, UpdatedAt: "2026-06-08T10:00:00Z"},
			{ID: "n2", Kind: "fact", Title: "gone", Body: "b", Classification: ClassTeamShared, Version: 2, UpdatedAt: "2026-06-08T12:00:00Z", DeletedAt: &deletedAt},
		},
		Cursor: "2026-06-08T12:00:00Z",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-tomb",
		RefreshToken: "rt-tomb",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	if n != 2 {
		t.Errorf("n=%d, want 2 (both nodes received including tombstone)", n)
	}

	entries := syncer.PulledEntries()
	if len(entries) != 1 {
		t.Fatalf("PulledEntries len=%d, want 1 (tombstone filtered)", len(entries))
	}
	if entries[0].ID != "n1" {
		t.Errorf("surviving entry ID=%q, want n1", entries[0].ID)
	}
}

// TestContextPull_PrecedencePreserved verifies that pulled team/org entries
// carry the correct layer for downstream merge priority checks.
// personal > team > org precedence is enforced at merge time by core/context/merge;
// this test checks that the layer tag is correct.
func TestContextPull_PrecedencePreserved(t *testing.T) {
	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{ID: "t1", Classification: ClassTeamShared, Kind: "fact", Title: "t", Body: "b", Version: 1, UpdatedAt: "2026-06-08T10:00:00Z"},
			{ID: "o1", Classification: ClassOrgShared, Kind: "fact", Title: "o", Body: "b", Version: 1, UpdatedAt: "2026-06-08T10:01:00Z"},
		},
		Cursor: "2026-06-08T10:01:00Z",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-prec",
		RefreshToken: "rt-prec",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}

	layerMap := make(map[string]contextpack.Layer)
	for _, e := range syncer.PulledEntries() {
		layerMap[e.ID] = e.Layer
	}

	if layerMap["t1"] != contextpack.LayerTeam {
		t.Errorf("t1 layer=%q, want team", layerMap["t1"])
	}
	if layerMap["o1"] != contextpack.LayerOrg {
		t.Errorf("o1 layer=%q, want org", layerMap["o1"])
	}

	// Verify team.Precedence() > org.Precedence().
	if contextpack.LayerTeam.Precedence() <= contextpack.LayerOrg.Precedence() {
		t.Errorf("team precedence (%d) should be > org precedence (%d)",
			contextpack.LayerTeam.Precedence(), contextpack.LayerOrg.Precedence())
	}
}

// TestContextPull_OfflineNoError verifies that signed-out state results in
// (0, nil) — local-only fallback (FR-104).
func TestContextPull_OfflineNoError(t *testing.T) {
	// nil client triggers ErrFleetDisabled path in canPull.
	syncer := NewContextGraphSyncer(nil, t.TempDir(), nil)
	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Errorf("offline PullDelta: want nil error, got %v", err)
	}
	if n != 0 {
		t.Errorf("offline PullDelta: want 0 nodes, got %d", n)
	}
}

// TestContextPush_TeamEntry verifies that a team entry is accepted by the
// stub server via PushEntry.
func TestContextPush_TeamEntry(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-push",
		RefreshToken: "rt-push",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	teamID := "team-uuid-1234"
	entry := ContextNodeEntry{
		ID:     "entry-1",
		Layer:  contextpack.LayerTeam,
		Kind:   "guidance",
		Title:  "House Go style",
		Body:   "Use table-driven tests.",
		TeamID: &teamID,
		Version: 1,
	}

	ctx := context.Background()
	result, err := syncer.PushEntry(ctx, entry, nil)
	if err != nil {
		t.Fatalf("PushEntry: %v", err)
	}
	if result.AcceptedNodes != 1 {
		t.Errorf("AcceptedNodes=%d, want 1", result.AcceptedNodes)
	}
	if fake.pushedCount() != 1 {
		t.Errorf("server saw %d pushed nodes, want 1", fake.pushedCount())
	}
}

// TestContextPush_IdempotentRepush verifies that re-pushing the same version
// is accepted by the stub (real server returns conflicts; stub returns accepted).
func TestContextPush_IdempotentRepush(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-idem",
		RefreshToken: "rt-idem",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	teamID := "tid-1"
	entry := ContextNodeEntry{ID: "e1", Layer: contextpack.LayerTeam, Kind: "fact", Title: "T", Body: "B", TeamID: &teamID, Version: 3}

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		r, err := syncer.PushEntry(ctx, entry, nil)
		if err != nil {
			t.Fatalf("push #%d: %v", i+1, err)
		}
		if r.AcceptedNodes != 1 {
			t.Errorf("push #%d: AcceptedNodes=%d", i+1, r.AcceptedNodes)
		}
	}
}
