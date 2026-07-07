package fleet

// context_poller_test.go — WP01 poller tests (context-graph-e2e-01NINTG03).
//
// Tests:
//   - Poller fires at least once within the interval.
//   - Poller stops on context cancellation.
//   - Double-start is a no-op (pollOnce guard).
//   - WithAuditEmitter wires an emitter that fires for each path
//     (published, pulled, promoted) via the fake emitter.
//   - Nil emitter is safe (no panic).
//
// NOTE: tests that call stubTokens() must NOT use t.Parallel() because
// the go-keyring mock is not thread-safe (keyring.Set races under -race).
// Tests that don't call stubTokens and don't write to shared state may
// use t.Parallel() safely.
//
// Race-safe fakes follow the mutex+snapshot pattern from CLAUDE.md.

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// ── Poller lifecycle tests ─────────────────────────────────────────────────────

// TestPoller_FiresAtLeastOnce verifies that the background poller calls
// PullDelta at least once within the configured interval.
func TestPoller_FiresAtLeastOnce(t *testing.T) {
	// No t.Parallel(): calls stubTokens which writes to the mock keyring.

	const pollInterval = 20 * time.Millisecond

	var mu sync.Mutex
	pullCount := 0

	fake := &contextFakeServer{}
	// Add a pull response so PullDelta doesn't return 0 nodes.
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "poller-n1", Kind: "fact", Title: "Poller entry",
				Body: "body", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
		Cursor: time.Now().UTC().Format(time.RFC3339Nano),
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-poller-fires",
		RefreshToken: "rt-poller-fires",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	// Wire a library merger that counts calls.
	syncer.SetLibraryMerger(func(entries []ContextNodeEntry) {
		mu.Lock()
		defer mu.Unlock()
		pullCount++
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	syncer.StartPoller(ctx, pollInterval)

	// Wait up to 500ms for at least one poll to fire.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := pullCount
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	final := pullCount
	mu.Unlock()
	if final == 0 {
		t.Error("poller did not fire within 500ms")
	}
}

// TestPoller_StopsOnContextCancel verifies the goroutine exits when the
// context is canceled.
func TestPoller_StopsOnContextCancel(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	const pollInterval = 10 * time.Millisecond

	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "stop-n1", Kind: "fact", Title: "Stop entry",
				Body: "b", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
		Cursor: time.Now().UTC().Format(time.RFC3339Nano),
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-poller-stop",
		RefreshToken: "rt-poller-stop",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx, cancel := context.WithCancel(context.Background())
	syncer.StartPoller(ctx, pollInterval)

	// Let it fire at least once.
	time.Sleep(50 * time.Millisecond)

	// Cancel and let the goroutine wind down.
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Capture pull count before and after a further sleep to confirm no
	// additional pulls happen post-cancel.
	st1 := syncer.Status()
	time.Sleep(30 * time.Millisecond)
	st2 := syncer.Status()

	if st2.PullCount > st1.PullCount+1 {
		// Allow at most 1 in-flight pull after cancel.
		t.Errorf("poller continued after ctx cancel: PullCount %d → %d", st1.PullCount, st2.PullCount)
	}
}

// TestPoller_DoubleStartNoOp verifies that calling StartPoller twice starts
// only one goroutine (the pollOnce guard).
func TestPoller_DoubleStartNoOp(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	const pollInterval = 10 * time.Millisecond

	var mu sync.Mutex
	fireCount := 0

	fake := &contextFakeServer{}
	// Queue enough responses for multiple polls.
	for i := 0; i < 10; i++ {
		fake.addPullResponse(contextPullResponse{
			Nodes: []ContextPulledNode{
				{
					ID: "dup-n1", Kind: "fact", Title: "Dup",
					Body: "b", Classification: ClassTeamShared,
					Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
				},
			},
			Cursor: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-poller-dup",
		RefreshToken: "rt-poller-dup",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)
	// Join the poll goroutine before the test's keyring teardown runs, else
	// pollLoop's LoadTokens races ClearTokens on the shared keyring mock.
	defer syncer.Stop()
	syncer.SetLibraryMerger(func(_ []ContextNodeEntry) {
		mu.Lock()
		defer mu.Unlock()
		fireCount++
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start twice — second call must be a no-op.
	syncer.StartPoller(ctx, pollInterval)
	syncer.StartPoller(ctx, pollInterval)

	// Give both goroutines (if mistakenly spawned) time to fire.
	time.Sleep(100 * time.Millisecond)

	// If two goroutines ran, fireCount would grow 2× per tick.
	// With a single goroutine and 10ms tick over 100ms we expect ~10 ticks max.
	// If we see unreasonably large numbers, the once-guard failed.
	mu.Lock()
	n := fireCount
	mu.Unlock()

	// Two goroutines at 10ms each over 100ms could produce ≥ 18 calls;
	// one goroutine produces ≤ 12. Use 15 as the threshold.
	if n > 15 {
		t.Errorf("double-start guard failed: fireCount=%d (expected ≤15 with a single goroutine)", n)
	}
}

// ── WithAuditEmitter + audit emit tests ────────────────────────────────────────

// TestAuditEmitter_PushEntry verifies that PushEntry emits
// KindFleetContextPublished when an emitter is wired.
func TestAuditEmitter_PushEntry(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-audit-push",
		RefreshToken: "rt-audit-push",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	teamID := "team-audit-push"
	entry := ContextNodeEntry{
		ID:      "audit-push-n1",
		Layer:   contextpack.LayerTeam,
		Kind:    "fact",
		Title:   "Audit push entry",
		Body:    "body",
		TeamID:  &teamID,
		Version: 1,
	}

	ctx := context.Background()
	if _, err := syncer.PushEntry(ctx, entry, nil); err != nil {
		t.Fatalf("PushEntry: %v", err)
	}

	events := em.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Kind != contextaudit.KindFleetContextPublished {
		t.Errorf("Kind=%q, want KindFleetContextPublished", events[0].Kind)
	}
}

// TestAuditEmitter_PullDelta verifies that PullDelta emits
// KindFleetContextPulled when at least one node is returned.
func TestAuditEmitter_PullDelta(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "audit-pull-n1", Kind: "fact", Title: "Audit pull",
				Body: "b", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: "2026-07-05T10:00:00Z",
			},
		},
		Cursor: "2026-07-05T10:00:00Z",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-audit-pull",
		RefreshToken: "rt-audit-pull",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	if n != 1 {
		t.Errorf("PullDelta returned %d, want 1", n)
	}

	events := em.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Kind != contextaudit.KindFleetContextPulled {
		t.Errorf("Kind=%q, want KindFleetContextPulled", events[0].Kind)
	}
}

// TestAuditEmitter_PullDelta_ZeroNodes verifies that PullDelta does NOT emit
// when the pull returns 0 nodes (FR-007: emit only when n > 0).
func TestAuditEmitter_PullDelta_ZeroNodes(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes:  nil,
		Cursor: "",
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-audit-pull-zero",
		RefreshToken: "rt-audit-pull-zero",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	ctx := context.Background()
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}

	events := em.snapshot()
	if len(events) != 0 {
		t.Errorf("got %d events for zero-node pull, want 0", len(events))
	}
}

// TestAuditEmitter_Promote verifies that Promote emits
// KindFleetContextPromoted on success.
func TestAuditEmitter_Promote(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-audit-prom",
		RefreshToken: "rt-audit-prom",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	// Pre-seed cache.
	syncer.mu.Lock()
	syncer.pulled = append(syncer.pulled, ContextNodeEntry{
		ID:             "node-promote",
		Layer:          contextpack.LayerTeam,
		Classification: ClassTeamShared,
		Kind:           "guidance",
		Title:          "Promote me",
		Body:           "body",
		Version:        1,
		SyncStatus:     SyncStatusSynced,
	})
	syncer.mu.Unlock()

	em := &fakeContextAuditEmitter{}
	syncer.WithAuditEmitter(em)

	ctx := context.Background()
	if _, err := syncer.Promote(ctx, "node-promote"); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	events := em.snapshot()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Kind != contextaudit.KindFleetContextPromoted {
		t.Errorf("Kind=%q, want KindFleetContextPromoted", events[0].Kind)
	}
}

// TestAuditEmitter_NilEmitter_NoOp verifies that no panic or error occurs
// when the audit emitter is not wired (FR-009).
func TestAuditEmitter_NilEmitter_NoOp(t *testing.T) {
	// No t.Parallel(): calls stubTokens.

	fake := &contextFakeServer{}
	fake.addPullResponse(contextPullResponse{
		Nodes: []ContextPulledNode{
			{
				ID: "nil-em-n1", Kind: "fact", Title: "Nil emitter",
				Body: "b", Classification: ClassTeamShared,
				Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
		Cursor: time.Now().UTC().Format(time.RFC3339Nano),
	})

	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-audit-nil",
		RefreshToken: "rt-audit-nil",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithTeamCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)
	// Do NOT call WithAuditEmitter — auditEmitter stays nil.

	ctx := context.Background()
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta with nil emitter: %v", err)
	}

	teamID := "t1"
	entry := ContextNodeEntry{
		ID:     "nil-push-n1",
		Layer:  contextpack.LayerTeam,
		Kind:   "fact",
		Title:  "nil push",
		Body:   "b",
		TeamID: &teamID,
	}
	if _, err := syncer.PushEntry(ctx, entry, nil); err != nil {
		t.Fatalf("PushEntry with nil emitter: %v", err)
	}
}
