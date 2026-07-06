package fleet

// context_e2e_integration_test.go — WP04 end-to-end integration tests
// (context-graph-e2e-01NINTG03 WP04).
//
// Scenarios exercised against the contextFakeServer stub:
//   1. Push → pull → PulledEntries: publish a team node, pull it back, verify
//      it appears in PulledEntries().
//   2. Tombstone propagation: pull a node with DeletedAt; verify it is absent
//      from PulledEntries().
//   3. Promote path + audit verify: promote a team entry to org, verify
//      KindFleetContextPromoted is emitted via the fake emitter.
//   4. Full round-trip with audit: push emits KindFleetContextPublished,
//      pull emits KindFleetContextPulled, promote emits KindFleetContextPromoted.
//
// The contextFakeServer is defined in context_pull_integration_test.go.
// The fakeContextAuditEmitter is defined in context_promote_test.go.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// TestContextE2E_PushThenPullThenPulledEntries exercises the core publish →
// fleet ingest → pull → PulledEntries path.
func TestContextE2E_PushThenPullThenPulledEntries(t *testing.T) {
	fake := &contextFakeServer{}

	// The fake server will accept a push; then serve the same node on pull.
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID:             "e2e-n1",
				Kind:           "fact",
				Title:          "E2E team entry",
				Body:           "pushed then pulled",
				Classification: ClassTeamShared,
				Version:        1,
				UpdatedAt:      updatedAt,
			},
		},
		Cursor: updatedAt,
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-e2e-push-pull",
		RefreshToken: "rt-e2e-push-pull",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()

	// Step 1: push the entry.
	teamID := "team-e2e-1"
	entry := ContextNodeEntry{
		ID:     "e2e-n1",
		Layer:  contextpack.LayerTeam,
		Kind:   "fact",
		Title:  "E2E team entry",
		Body:   "pushed then pulled",
		TeamID: &teamID,
		Version: 1,
	}
	result, err := syncer.PushEntry(ctx, entry, nil)
	if err != nil {
		t.Fatalf("PushEntry: %v", err)
	}
	if result.AcceptedNodes != 1 {
		t.Errorf("AcceptedNodes=%d, want 1", result.AcceptedNodes)
	}

	// Step 2: pull the delta.
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	if n != 1 {
		t.Errorf("PullDelta returned %d nodes, want 1", n)
	}

	// Step 3: verify the entry is in PulledEntries().
	entries := syncer.PulledEntries()
	if len(entries) != 1 {
		t.Fatalf("PulledEntries len=%d, want 1", len(entries))
	}
	if entries[0].ID != "e2e-n1" {
		t.Errorf("PulledEntries[0].ID=%q, want e2e-n1", entries[0].ID)
	}
	if entries[0].Layer != contextpack.LayerTeam {
		t.Errorf("layer=%q, want team", entries[0].Layer)
	}
}

// TestContextE2E_TombstonePropagation verifies that a node pulled with
// DeletedAt non-nil is absent from PulledEntries() (FR-103).
func TestContextE2E_TombstonePropagation(t *testing.T) {
	fake := &contextFakeServer{}
	deletedAt := "2026-07-05T12:00:00Z"

	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "tombstone-alive", Kind: "fact", Title: "alive",
				Body: "b", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: "2026-07-05T10:00:00Z",
			},
			{
				ID: "tombstone-dead", Kind: "fact", Title: "gone",
				Body: "b", Classification: ClassTeamShared,
				Version: 2, UpdatedAt: "2026-07-05T12:00:00Z",
				DeletedAt: &deletedAt,
			},
		},
		Cursor: "2026-07-05T12:00:00Z",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-e2e-tomb",
		RefreshToken: "rt-e2e-tomb",
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
		t.Errorf("PullDelta returned %d, want 2 (including tombstone)", n)
	}

	entries := syncer.PulledEntries()
	if len(entries) != 1 {
		t.Fatalf("PulledEntries len=%d, want 1 (tombstone excluded)", len(entries))
	}
	if entries[0].ID != "tombstone-alive" {
		t.Errorf("surviving entry ID=%q, want tombstone-alive", entries[0].ID)
	}
}

// TestContextE2E_PromoteWithAudit exercises the promote path and verifies
// that KindFleetContextPromoted is emitted via the fake audit emitter.
func TestContextE2E_PromoteWithAudit(t *testing.T) {
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-e2e-prom",
		RefreshToken: "rt-e2e-prom",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	// Pre-seed a team entry in the local cache.
	syncer.mu.Lock()
	syncer.pulled = append(syncer.pulled, ContextNodeEntry{
		ID:             "node-e2e-promote",
		Layer:          contextpack.LayerTeam,
		Classification: ClassTeamShared,
		Kind:           "guidance",
		Title:          "E2E promote",
		Body:           "team body",
		Version:        1,
		SyncStatus:     SyncStatusSynced,
	})
	syncer.mu.Unlock()

	ctx := context.Background()
	result, err := syncer.Promote(ctx, "node-e2e-promote")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result.Node.Classification != ClassOrgShared {
		t.Errorf("promoted classification=%q, want org_shared", result.Node.Classification)
	}

	// Verify audit emit.
	events := em.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(events))
	}
	if events[0].Kind != contextaudit.KindFleetContextPromoted {
		t.Errorf("Kind=%q, want KindFleetContextPromoted", events[0].Kind)
	}

	// Verify local cache is updated to org layer.
	entries := syncer.PulledEntries()
	for _, e := range entries {
		if e.ID == "node-e2e-promote" {
			if e.Layer != contextpack.LayerOrg {
				t.Errorf("local cache layer=%q, want org after promote", e.Layer)
			}
			if e.Classification != ClassOrgShared {
				t.Errorf("local cache classification=%q, want org_shared", e.Classification)
			}
		}
	}
}

// TestContextE2E_FullRoundTripAudit verifies all four audit kinds fire
// across the complete push → pull → promote path.
func TestContextE2E_FullRoundTripAudit(t *testing.T) {
	fake := &contextFakeServer{}

	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID:             "e2e-full-n1",
				Kind:           "fact",
				Title:          "Full round-trip",
				Body:           "body",
				Classification: ClassTeamShared,
				Version:        1,
				UpdatedAt:      updatedAt,
			},
		},
		Cursor: updatedAt,
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-e2e-full",
		RefreshToken: "rt-e2e-full",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	ctx := context.Background()

	// Push.
	teamID := "team-e2e-full"
	entry := ContextNodeEntry{
		ID:     "e2e-full-n1",
		Layer:  contextpack.LayerTeam,
		Kind:   "fact",
		Title:  "Full round-trip",
		Body:   "body",
		TeamID: &teamID,
		Version: 1,
	}
	if _, err := syncer.PushEntry(ctx, entry, nil); err != nil {
		t.Fatalf("PushEntry: %v", err)
	}

	// Pull.
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}

	// Promote.
	if _, err := syncer.Promote(ctx, "e2e-full-n1"); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Verify three audit events: published, pulled, promoted.
	events := em.snapshot()
	if len(events) != 3 {
		t.Fatalf("got %d audit events, want 3 (published+pulled+promoted)", len(events))
	}

	kinds := make(map[contextaudit.Kind]int, 3)
	for _, ev := range events {
		kinds[ev.Kind]++
	}
	if kinds[contextaudit.KindFleetContextPublished] != 1 {
		t.Errorf("KindFleetContextPublished count=%d, want 1", kinds[contextaudit.KindFleetContextPublished])
	}
	if kinds[contextaudit.KindFleetContextPulled] != 1 {
		t.Errorf("KindFleetContextPulled count=%d, want 1", kinds[contextaudit.KindFleetContextPulled])
	}
	if kinds[contextaudit.KindFleetContextPromoted] != 1 {
		t.Errorf("KindFleetContextPromoted count=%d, want 1", kinds[contextaudit.KindFleetContextPromoted])
	}
}
