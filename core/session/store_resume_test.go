package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

// helper: make a ready memStore with one session and a couple of msgs.
func newSeededStore(t *testing.T, sessionID string) Store {
	t.Helper()
	s := NewMemoryStore()
	if err := s.Create(context.Background(), Record{
		ID:           sessionID,
		Name:         "test",
		CreatedAt:    time.Unix(0, 1000).UTC(),
		UpdatedAt:    time.Unix(0, 1000).UTC(),
		LastActiveAt: time.Unix(0, 1000).UTC(),
		ContextKind:  ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

// TestStore_MarkStreamingFailure_RoundTrip verifies the resume
// metadata round-trips via MarkStreamingFailure → ListMessages.
func TestStore_MarkStreamingFailure_RoundTrip(t *testing.T) {
	t.Parallel()
	store := newSeededStore(t, "sess-1")
	ctx := context.Background()

	stored, err := store.AppendMessage(ctx, Message{
		ID:        "msg-1",
		SessionID: "sess-1",
		Role:      RoleAssistant,
		Content:   "partial",
		CreatedAt: time.Unix(0, 2000).UTC(),
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	failedAt := time.Unix(0, 5000).UTC()
	if err := store.MarkStreamingFailure(ctx, "sess-1", stored.ID, failedAt, "transient", true); err != nil {
		t.Fatalf("MarkStreamingFailure: %v", err)
	}

	msgs, err := store.ListMessages(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.StreamingFailedAt == nil || !m.StreamingFailedAt.Equal(failedAt) {
		t.Errorf("StreamingFailedAt = %v, want %v", m.StreamingFailedAt, failedAt)
	}
	if m.StreamingFailureKind != "transient" {
		t.Errorf("StreamingFailureKind = %q", m.StreamingFailureKind)
	}
	if !m.StreamingRecoverable {
		t.Errorf("StreamingRecoverable = false, want true")
	}
}

// TestStore_MarkStreamingFailure_Idempotent verifies marking the same
// row twice with different classifications overwrites cleanly.
func TestStore_MarkStreamingFailure_Idempotent(t *testing.T) {
	t.Parallel()
	store := newSeededStore(t, "sess-1")
	ctx := context.Background()
	if _, err := store.AppendMessage(ctx, Message{
		ID: "msg-1", SessionID: "sess-1", Role: RoleAssistant, Content: "partial",
		CreatedAt: time.Unix(0, 2000).UTC(),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := store.MarkStreamingFailure(ctx, "sess-1", "msg-1", time.Unix(0, 3000).UTC(), "transient", true); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if err := store.MarkStreamingFailure(ctx, "sess-1", "msg-1", time.Unix(0, 4000).UTC(), "auth", false); err != nil {
		t.Fatalf("second mark: %v", err)
	}
	got, err := store.GetMessage(ctx, "sess-1", "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.StreamingFailureKind != "auth" {
		t.Errorf("StreamingFailureKind = %q, want auth (overwritten)", got.StreamingFailureKind)
	}
	if got.StreamingRecoverable {
		t.Errorf("StreamingRecoverable = true, want false (overwritten)")
	}
}

// TestStore_MarkStreamingFailure_UnknownMessage verifies the error
// surface for an unknown message id.
func TestStore_MarkStreamingFailure_UnknownMessage(t *testing.T) {
	t.Parallel()
	store := newSeededStore(t, "sess-1")
	err := store.MarkStreamingFailure(context.Background(), "sess-1", "nope", time.Now(), "transient", true)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

// TestStore_AppendContinuation_StampsContinuationOf verifies the new
// continuation row carries continuation_of pointing at the partial id.
func TestStore_AppendContinuation_StampsContinuationOf(t *testing.T) {
	t.Parallel()
	store := newSeededStore(t, "sess-1")
	ctx := context.Background()
	original, err := store.AppendMessage(ctx, Message{
		ID: "msg-1", SessionID: "sess-1", Role: RoleAssistant, Content: "partial",
		CreatedAt: time.Unix(0, 2000).UTC(),
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	cont, err := store.AppendContinuation(ctx, original.ID, Message{
		ID: "msg-2", SessionID: "sess-1", Role: RoleAssistant, Content: " world",
		CreatedAt: time.Unix(0, 6000).UTC(),
	})
	if err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if cont.ContinuationOf != original.ID {
		t.Errorf("ContinuationOf = %q, want %q", cont.ContinuationOf, original.ID)
	}
	if cont.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", cont.Sequence)
	}
	// Round-trip via ListMessages.
	msgs, err := store.ListMessages(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[1].ContinuationOf != original.ID {
		t.Errorf("listed[1].ContinuationOf = %q, want %q", msgs[1].ContinuationOf, original.ID)
	}
}

// TestStore_AppendContinuation_RejectsEmptyOriginalID verifies the
// programmer-error guard.
func TestStore_AppendContinuation_RejectsEmptyOriginalID(t *testing.T) {
	t.Parallel()
	store := newSeededStore(t, "sess-1")
	_, err := store.AppendContinuation(context.Background(), "", Message{
		ID: "msg-x", SessionID: "sess-1", Role: RoleAssistant, Content: "x",
	})
	if err == nil {
		t.Error("expected error on empty originalID")
	}
}
