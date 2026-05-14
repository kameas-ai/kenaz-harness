package recorders

import (
	"context"
	"sync"
	"testing"
)

// SpanCall records one call to TraceSink.Span.
type SpanCall struct {
	Ctx   context.Context
	Name  string
	Attrs map[string]any
}

// TraceSink is a recording fake that satisfies agentgraph.TraceSink.
// Span calls are recorded; the returned context + end function are no-ops.
type TraceSink struct {
	mu    sync.Mutex
	calls []SpanCall
}

// Span records the call and returns a pass-through context + no-op end.
func (r *TraceSink) Span(ctx context.Context, name string, attrs map[string]any) (context.Context, func(error)) {
	r.mu.Lock()
	r.calls = append(r.calls, SpanCall{Ctx: ctx, Name: name, Attrs: attrs})
	r.mu.Unlock()
	return ctx, func(error) {}
}

// Calls returns a snapshot of all recorded Span calls.
func (r *TraceSink) Calls() []SpanCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SpanCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Span calls recorded.
func (r *TraceSink) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireSpanCalled asserts at least one Span call used the given name.
func (r *TraceSink) RequireSpanCalled(t *testing.T, name string) {
	t.Helper()
	for _, c := range r.Calls() {
		if c.Name == name {
			return
		}
	}
	t.Fatalf("TraceSink.Span: no span with name=%q found", name)
}

// RequireCalledWith asserts at least one Span call matches the given matcher.
func (r *TraceSink) RequireCalledWith(t *testing.T, match func(SpanCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("TraceSink.Span: no call found matching %q", desc)
}

// RequireNotCalled asserts Span was never invoked.
func (r *TraceSink) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("TraceSink.Span: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *TraceSink) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
