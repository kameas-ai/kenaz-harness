package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// TestMessageStore_RoundTripsCompactionShape pins the value-shape
// translation between core/session.Message and compaction.SessionMessage at
// the wiring boundary. A regression where role / content / sequence
// drift would silently break the engine's boundary-snap logic.
func TestMessageStore_RoundTripsCompactionShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := session.NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, session.Record{
		ID: "s1", Name: "x",
		CreatedAt: now, UpdatedAt: now, LastActiveAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: "m1", SessionID: "s1", Role: session.RoleUser, Content: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: "m2", SessionID: "s1", Role: session.RoleAssistant, Content: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	a := NewMessageStore(store)
	got, err := a.ListActiveMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hi" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hello" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

// TestMessageStore_ApplyCompaction_Persists asserts the wiring adapter
// forwards an ApplyCompaction call onto the session store and the
// post-compaction state matches the engine's expectations.
func TestMessageStore_ApplyCompaction_Persists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := session.NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, session.Record{
		ID: "s1", Name: "x",
		CreatedAt: now, UpdatedAt: now, LastActiveAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{ID: "m1", SessionID: "s1", Role: session.RoleUser, Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{ID: "m2", SessionID: "s1", Role: session.RoleAssistant, Content: "b"}); err != nil {
		t.Fatal(err)
	}

	a := NewMessageStore(store)
	at := now.Add(time.Hour)
	summary := compaction.SessionMessage{
		ID: "sum-1", Role: "system", Content: "[Earlier conversation summary: ab]",
		Sequence: 0, CreatedAt: at,
	}
	if err := a.ApplyCompaction(ctx, "s1", summary, []string{"m1", "m2"}, at); err != nil {
		t.Fatalf("ApplyCompaction: %v", err)
	}
	active, err := store.ListMessagesActive(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Errorf("len(active) = %d, want 1 (just the summary)", len(active))
	}
	if len(active) > 0 && active[0].ID != "sum-1" {
		t.Errorf("active[0].ID = %q, want sum-1", active[0].ID)
	}
}

// TestSweepStore_ForwardsToStore pins the SweepStore adapter onto the
// session.Store DeleteArchivedBefore method.
func TestSweepStore_ForwardsToStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := session.NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, session.Record{
		ID: "s1", Name: "x",
		CreatedAt: now, UpdatedAt: now, LastActiveAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{ID: "m1", SessionID: "s1", Role: session.RoleUser, Content: "a"}); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-100 * 24 * time.Hour)
	if err := store.ApplyCompaction(ctx, "s1", session.Message{
		ID: "sum-1", Role: session.RoleSystem, Sequence: 0, Content: "[s]", CreatedAt: old,
	}, []string{"m1"}, old); err != nil {
		t.Fatal(err)
	}

	a := NewSweepStore(store)
	deleted, _, _, err := a.DeleteArchivedBefore(ctx, now.Add(-7*24*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
}

// TestCapabilityLookup_ExactMatch pins the curated builtin table.
func TestCapabilityLookup_ExactMatch(t *testing.T) {
	t.Parallel()
	c := NewCapabilityLookup()
	max, ok := c.MaxContextTokens(compaction.ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"})
	if !ok {
		t.Fatalf("expected ok=true for known model")
	}
	if max != 200_000 {
		t.Errorf("max = %d, want 200000", max)
	}
}

// TestCapabilityLookup_PrefixMatch asserts a fresh model in a known
// family inherits the family head's budget.
func TestCapabilityLookup_PrefixMatch(t *testing.T) {
	t.Parallel()
	c := NewCapabilityLookup()
	c.SetTable("anthropic", "claude-sonnet-", 200_000)
	max, ok := c.MaxContextTokens(compaction.ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-9-9-future"})
	if !ok || max != 200_000 {
		t.Errorf("got max=%d ok=%v, want 200000 + true", max, ok)
	}
}

// TestCapabilityLookup_UnknownModelReturnsFalse asserts the engine's
// "skip pre-flight check" branch is reachable when nothing matches.
func TestCapabilityLookup_UnknownModelReturnsFalse(t *testing.T) {
	t.Parallel()
	c := NewCapabilityLookup()
	_, ok := c.MaxContextTokens(compaction.ProviderProfileRef{ProviderID: "voidcorp", ModelID: "unknown-1"})
	if ok {
		t.Error("expected ok=false for unknown provider+model")
	}
}

// TestAuditEmitter_RingBuffersEvents asserts the in-memory ring keeps
// the most-recent N events when its capacity is reached.
func TestAuditEmitter_RingBuffersEvents(t *testing.T) {
	t.Parallel()
	em := NewAuditEmitter().WithRingCapacity(3)
	for i := 0; i < 5; i++ {
		em.Emit(context.Background(), "test.kind", map[string]int{"i": i})
	}
	got := em.Recent(0)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Last three events should be i=2, i=3, i=4 (oldest-first).
	if string(got[0].Payload) != `{"i":2}` {
		t.Errorf("got[0] = %s", string(got[0].Payload))
	}
	if string(got[2].Payload) != `{"i":4}` {
		t.Errorf("got[2] = %s", string(got[2].Payload))
	}
}
