package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

// recordingStarter is a fake ResumeStarter that captures every
// StartResume call so the long-turn-resilience-WP03 tests can pin the
// idempotency contract + the (sessionID, originalMessageID, profileID,
// modelOverride) tuple.
type recordingStarter struct {
	mu        sync.Mutex
	calls     []recordedStartCall
	nextSubID int
	err       error
}

type recordedStartCall struct {
	sessionID         string
	originalMessageID string
	profileID         string
	modelOverride     string
	subID             string
}

func (r *recordingStarter) StartResume(_ context.Context, sessionID, originalMessageID, profileID, modelOverride string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	r.nextSubID++
	subID := "resume-sub-" + itoa(r.nextSubID)
	r.calls = append(r.calls, recordedStartCall{
		sessionID:         sessionID,
		originalMessageID: originalMessageID,
		profileID:         profileID,
		modelOverride:     modelOverride,
		subID:             subID,
	})
	return subID, nil
}

func (r *recordingStarter) snapshot() []recordedStartCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedStartCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// itoa renders a small positive int as a decimal string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// seedPartialMessage builds a session with one user message + one
// assistant message marked as a partial-output drop, returning the
// partial message id so tests can issue ResumeMessage against it.
func seedPartialMessage(t *testing.T, mgr *session.Manager, sessionName string, recoverable bool) (sessionID, partialID string) {
	t.Helper()
	ctx := context.Background()
	rec, err := mgr.Create(ctx, sessionName)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleUser, Content: "say hi"}); err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}
	stored, err := mgr.AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleAssistant, Content: "hello partial"})
	if err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}
	if err := mgr.MarkStreamingFailure(ctx, rec.ID, stored.ID, "transient", recoverable); err != nil {
		t.Fatalf("MarkStreamingFailure: %v", err)
	}
	return rec.ID, stored.ID
}

// TestResumeMessage_HappyPath verifies a recoverable partial row is
// resumable and the resume RPC forwards (sessionID, originalMessageID)
// through to the wired ResumeStarter, returning the new sub id.
func TestResumeMessage_HappyPath(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	starter := &recordingStarter{}
	api := WithResumeStarter(NewManagerAPI(mgr), starter)

	sessionID, partialID := seedPartialMessage(t, mgr, "sess-A", true)

	res, err := api.ResumeMessage(context.Background(), sessionID, partialID)
	if err != nil {
		t.Fatalf("ResumeMessage: %v", err)
	}
	if res.SubscriptionID == "" {
		t.Errorf("SubscriptionID empty")
	}
	if res.OriginalMessageID != partialID {
		t.Errorf("OriginalMessageID = %q, want %q", res.OriginalMessageID, partialID)
	}
	calls := starter.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 StartResume call, got %d", len(calls))
	}
	if calls[0].sessionID != sessionID || calls[0].originalMessageID != partialID {
		t.Errorf("StartResume args = %+v, want session=%q msg=%q", calls[0], sessionID, partialID)
	}
}

// TestResumeMessage_NotRecoverable verifies a partial row whose
// streaming_recoverable is false (tool_use ran before the drop) is
// rejected with ErrResumeNotRecoverable. The starter is NOT invoked.
func TestResumeMessage_NotRecoverable(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	starter := &recordingStarter{}
	api := WithResumeStarter(NewManagerAPI(mgr), starter)

	sessionID, partialID := seedPartialMessage(t, mgr, "sess-B", false)

	_, err := api.ResumeMessage(context.Background(), sessionID, partialID)
	if !errors.Is(err, ErrResumeNotRecoverable) {
		t.Errorf("ResumeMessage err = %v, want ErrResumeNotRecoverable", err)
	}
	if got := len(starter.snapshot()); got != 0 {
		t.Errorf("expected 0 StartResume calls on non-recoverable partial, got %d", got)
	}
}

// TestResumeMessage_NotPartial verifies a healthy assistant row (no
// streaming_failed_at) is rejected with ErrResumeNotPartial. Catches
// the frontend-bug case where a click on a healthy bubble fires
// resume by mistake.
func TestResumeMessage_NotPartial(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	starter := &recordingStarter{}
	api := WithResumeStarter(NewManagerAPI(mgr), starter)

	rec, err := mgr.Create(context.Background(), "sess-C")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := mgr.AppendMessage(context.Background(), rec.ID, session.Message{
		Role: session.RoleAssistant, Content: "healthy",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	_, err = api.ResumeMessage(context.Background(), rec.ID, stored.ID)
	if !errors.Is(err, ErrResumeNotPartial) {
		t.Errorf("ResumeMessage err = %v, want ErrResumeNotPartial", err)
	}
	if got := len(starter.snapshot()); got != 0 {
		t.Errorf("expected 0 StartResume calls on healthy row, got %d", got)
	}
}

// TestResumeMessage_Idempotency verifies issuing resume twice on the
// same partial row does not double-bill: the second call returns
// ErrResumeAlreadyContinued (because a continuation row already exists).
// This is the canonical (b) acceptance test on the WP03 spec.
func TestResumeMessage_Idempotency(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	starter := &recordingStarter{}
	api := WithResumeStarter(NewManagerAPI(mgr), starter)

	sessionID, partialID := seedPartialMessage(t, mgr, "sess-D", true)

	// First call succeeds + opens a stream.
	if _, err := api.ResumeMessage(context.Background(), sessionID, partialID); err != nil {
		t.Fatalf("first ResumeMessage: %v", err)
	}
	// Simulate the chat runner having persisted the continuation row
	// (in production the kernel run's terminal SessionWriteNode does
	// this; the unit test stands in for it directly via
	// AppendContinuation so the idempotency guard fires on the second
	// resume).
	if _, err := mgr.AppendContinuation(context.Background(), sessionID, partialID, session.Message{
		Role: session.RoleAssistant, Content: "continuation text",
	}); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}

	// Second call MUST short-circuit without invoking the starter.
	_, err := api.ResumeMessage(context.Background(), sessionID, partialID)
	if !errors.Is(err, ErrResumeAlreadyContinued) {
		t.Errorf("second ResumeMessage err = %v, want ErrResumeAlreadyContinued", err)
	}
	if got := len(starter.snapshot()); got != 1 {
		t.Errorf("StartResume call count after idempotency guard = %d, want 1", got)
	}
}

// TestResumeMessage_NotConfigured_GracefulDegrade verifies the
// graceful-degrade path on the WP03 spec: when no ResumeStarter is
// wired (chassis boot failure / test path), ResumeMessage returns
// ErrResumeNotConfigured rather than panicking. The frontend falls
// through to the WP00 surface ("Connection lost — partial reply
// preserved.") since no continuation can be opened.
func TestResumeMessage_NotConfigured_GracefulDegrade(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr) // NO WithResumeStarter call

	sessionID, partialID := seedPartialMessage(t, mgr, "sess-E", true)

	_, err := api.ResumeMessage(context.Background(), sessionID, partialID)
	if !errors.Is(err, ErrResumeNotConfigured) {
		t.Errorf("ResumeMessage err = %v, want ErrResumeNotConfigured", err)
	}
}

// TestResumeMessage_ListMessagesSurfacesResumeMetadata verifies that
// ListMessages projects the new resume metadata onto the wire shape so
// the frontend can render the Resume button without a second round-trip.
func TestResumeMessage_ListMessagesSurfacesResumeMetadata(t *testing.T) {
	t.Parallel()
	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	sessionID, partialID := seedPartialMessage(t, mgr, "sess-F", true)

	msgs, err := api.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var partial Message
	for _, m := range msgs {
		if m.ID == partialID {
			partial = m
			break
		}
	}
	if partial.ID == "" {
		t.Fatalf("partial id %q not found in list", partialID)
	}
	if partial.StreamingFailedAt == "" {
		t.Errorf("StreamingFailedAt empty on partial row")
	}
	if partial.StreamingFailureKind != "transient" {
		t.Errorf("StreamingFailureKind = %q, want transient", partial.StreamingFailureKind)
	}
	if !partial.StreamingRecoverable {
		t.Errorf("StreamingRecoverable = false, want true")
	}
}
