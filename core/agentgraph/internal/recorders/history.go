package recorders

import (
	"context"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// HistoryCall records one call to HistoryReader.History.
type HistoryCall struct {
	Ctx       context.Context
	SessionID string
	N         int
}

// AppendCall records one call to HistoryWriter.AppendMessage.
type AppendCall struct {
	Ctx       context.Context
	SessionID string
	Role      string
	Content   string
}

// HistoryReader is a recording fake that satisfies agentgraph.HistoryReader.
type HistoryReader struct {
	mu      sync.Mutex
	calls   []HistoryCall
	// Result is returned on every History call.
	Result []agentgraph.Message
	// Err is returned on every History call.
	Err error
}

// History records the call and returns the configured result.
func (r *HistoryReader) History(ctx context.Context, sessionID string, n int) ([]agentgraph.Message, error) {
	r.mu.Lock()
	r.calls = append(r.calls, HistoryCall{Ctx: ctx, SessionID: sessionID, N: n})
	result := r.Result
	err := r.Err
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Calls returns a snapshot of all recorded History calls.
func (r *HistoryReader) Calls() []HistoryCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]HistoryCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of History calls recorded.
func (r *HistoryReader) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledOnce asserts exactly one History call was recorded.
func (r *HistoryReader) RequireCalledOnce(t *testing.T) HistoryCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("HistoryReader.History: want 1 call, got %d", len(calls))
	}
	return calls[0]
}

// RequireCalledWithSession asserts at least one History call used the
// given session ID.
func (r *HistoryReader) RequireCalledWithSession(t *testing.T, sessionID string) {
	t.Helper()
	for _, c := range r.Calls() {
		if c.SessionID == sessionID {
			return
		}
	}
	t.Fatalf("HistoryReader.History: no call with sessionID=%q", sessionID)
}

// RequireNotCalled asserts History was never invoked.
func (r *HistoryReader) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("HistoryReader.History: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *HistoryReader) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// HistoryWriter is a recording fake that satisfies agentgraph.HistoryWriter.
type HistoryWriter struct {
	mu      sync.Mutex
	calls   []AppendCall
	// NextID is returned as the message ID on each AppendMessage call.
	NextID string
	// Err is returned on every AppendMessage call.
	Err error
}

// AppendMessage records the call and returns the configured ID.
func (r *HistoryWriter) AppendMessage(ctx context.Context, sessionID, role, content string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, AppendCall{Ctx: ctx, SessionID: sessionID, Role: role, Content: content})
	nextID := r.NextID
	err := r.Err
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if nextID == "" {
		nextID = "msg-1"
	}
	return nextID, nil
}

// Calls returns a snapshot of all recorded AppendMessage calls.
func (r *HistoryWriter) Calls() []AppendCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AppendCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of AppendMessage calls recorded.
func (r *HistoryWriter) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledOnce asserts exactly one AppendMessage call was recorded.
func (r *HistoryWriter) RequireCalledOnce(t *testing.T) AppendCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("HistoryWriter.AppendMessage: want 1 call, got %d", len(calls))
	}
	return calls[0]
}

// RequireCalledWith asserts at least one AppendMessage call matches the
// given matcher.
func (r *HistoryWriter) RequireCalledWith(t *testing.T, match func(AppendCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("HistoryWriter.AppendMessage: no call found matching %q", desc)
}

// RequireCalledWithRole asserts at least one AppendMessage call used the
// given role.
func (r *HistoryWriter) RequireCalledWithRole(t *testing.T, role string) {
	t.Helper()
	r.RequireCalledWith(t, func(c AppendCall) bool {
		return c.Role == role
	}, "role="+role)
}

// RequireNotCalled asserts AppendMessage was never invoked.
func (r *HistoryWriter) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("HistoryWriter.AppendMessage: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *HistoryWriter) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
