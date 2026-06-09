package fleet

// context_edges_test.go — WP03: tombstone propagation via pull + two-phase
// node→edge push. Mirrors the tombstone-via-pull test but with edges.
//
// (fleet-context-graph-sync-01NDFSEX17 WP03)

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	contextpack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// TestEdgePush_TwoPhase verifies that PushEntry includes both nodes and edges
// in the same request and that the server receives the correct structure.
func TestEdgePush_TwoPhase(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-edge",
		RefreshToken: "rt-edge",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	teamID := "tid-edge"
	nodeEntry := ContextNodeEntry{
		ID:     "node-from",
		Layer:  contextpack.LayerTeam,
		Kind:   "fact",
		Title:  "Source concept",
		Body:   "body",
		TeamID: &teamID,
		Version: 1,
	}
	edge := ContextEdgeEntry{
		ID:             "edge-1",
		FromNodeID:     "node-from",
		ToNodeID:       "node-to",
		Kind:           "references",
		Classification: ClassTeamShared,
		TeamID:         &teamID,
		Version:        1,
	}

	ctx := context.Background()
	result, err := syncer.PushEntry(ctx, nodeEntry, []ContextEdgeEntry{edge})
	if err != nil {
		t.Fatalf("PushEntry with edge: %v", err)
	}
	if result.AcceptedNodes != 1 {
		t.Errorf("AcceptedNodes=%d, want 1", result.AcceptedNodes)
	}
	if result.AcceptedEdges != 1 {
		t.Errorf("AcceptedEdges=%d, want 1", result.AcceptedEdges)
	}

	// Verify the server received both node and edge.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.pushRequests) == 0 {
		t.Fatal("no push requests received by fake server")
	}
	req := fake.pushRequests[len(fake.pushRequests)-1]
	if len(req.Nodes) != 1 {
		t.Errorf("server nodes=%d, want 1", len(req.Nodes))
	}
	if len(req.Edges) != 1 {
		t.Errorf("server edges=%d, want 1", len(req.Edges))
	}
	if req.Edges[0].FromNodeID != "node-from" {
		t.Errorf("edge.from=%q, want node-from", req.Edges[0].FromNodeID)
	}
	if req.Edges[0].Kind != "references" {
		t.Errorf("edge.kind=%q, want references", req.Edges[0].Kind)
	}
}

// TestEdgePull_RoundTrip verifies that pulled edges are stored and returned
// via PulledEdges().
func TestEdgePull_RoundTrip(t *testing.T) {
	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{ID: "n1", Classification: ClassTeamShared, Kind: "fact", Title: "n1", Body: "b", Version: 1, UpdatedAt: "2026-06-08T10:00:00Z"},
			{ID: "n2", Classification: ClassTeamShared, Kind: "fact", Title: "n2", Body: "b", Version: 1, UpdatedAt: "2026-06-08T10:00:00Z"},
		},
		Edges: []ContextPulledEdge{
			{
				ID:             "e1",
				OrgID:          "org-1",
				FromNodeID:     "n1",
				ToNodeID:       "n2",
				Kind:           "derived_from",
				Classification: ClassTeamShared,
				OwnerUserID:    "u1",
				Version:        1,
				CreatedAt:      "2026-06-08T10:00:00Z",
			},
		},
		Cursor: "2026-06-08T10:00:00Z",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-edge2",
		RefreshToken: "rt-edge2",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}

	edges := syncer.PulledEdges()
	if len(edges) != 1 {
		t.Fatalf("PulledEdges len=%d, want 1", len(edges))
	}
	if edges[0].ID != "e1" {
		t.Errorf("edge.ID=%q, want e1", edges[0].ID)
	}
	if edges[0].Kind != "derived_from" {
		t.Errorf("edge.kind=%q, want derived_from", edges[0].Kind)
	}
	if edges[0].FromNodeID != "n1" || edges[0].ToNodeID != "n2" {
		t.Errorf("edge endpoints: from=%q to=%q", edges[0].FromNodeID, edges[0].ToNodeID)
	}
}

// TestTombstone_ExistingEntryRemoved verifies that a tombstone received in a
// delta pull removes an entry that was present from the previous backfill.
func TestTombstone_ExistingEntryRemoved(t *testing.T) {
	fake := &contextFakeServer{}
	updatedAt := "2026-06-08T10:00:00Z"
	tombstoneAt := "2026-06-08T11:00:00Z"

	// First pull: entry is alive.
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{ID: "n1", Classification: ClassTeamShared, Kind: "fact", Title: "alive", Body: "b", Version: 1, UpdatedAt: updatedAt},
		},
		Cursor: updatedAt,
	})
	// Second pull: same entry is now tombstoned.
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{ID: "n1", Classification: ClassTeamShared, Kind: "fact", Title: "alive", Body: "b", Version: 2, UpdatedAt: tombstoneAt, DeletedAt: &tombstoneAt},
		},
		Cursor: tombstoneAt,
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-tomb2",
		RefreshToken: "rt-tomb2",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()

	// First pull — entry present.
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("first PullDelta: %v", err)
	}
	if len(syncer.PulledEntries()) != 1 {
		t.Fatalf("before tombstone: expected 1 entry, got %d", len(syncer.PulledEntries()))
	}

	// Second pull — tombstone received.
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("second PullDelta: %v", err)
	}
	if len(syncer.PulledEntries()) != 0 {
		t.Errorf("after tombstone: expected 0 entries, got %d", len(syncer.PulledEntries()))
	}
}

// TestEdgeUpsert_Idempotent verifies that re-receiving the same edge does not
// duplicate it in the in-memory cache.
func TestEdgeUpsert_Idempotent(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	edge := ContextEdgeEntry{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Kind: "references"}

	s.mu.Lock()
	s.upsertPulledEdgeLocked(edge)
	s.upsertPulledEdgeLocked(edge) // second upsert should not duplicate
	s.mu.Unlock()

	edges := s.PulledEdges()
	if len(edges) != 1 {
		t.Errorf("PulledEdges len=%d, want 1 (idempotent)", len(edges))
	}
}
