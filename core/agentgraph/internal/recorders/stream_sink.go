package recorders

import (
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// StreamSink is a recording fake that satisfies agentgraph.StreamSink.
// It records every Emit call and the Close call so tests can verify the
// kernel fans events correctly onto the sink.
type StreamSink struct {
	mu     sync.Mutex
	events []agentgraph.StreamEvent
	closed bool
}

// Emit records one streaming event.
func (r *StreamSink) Emit(ev agentgraph.StreamEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// Close records the close signal.
func (r *StreamSink) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}

// Events returns a snapshot of all recorded events.
func (r *StreamSink) Events() []agentgraph.StreamEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]agentgraph.StreamEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Closed reports whether Close was called.
func (r *StreamSink) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// EventCount returns the number of Emit calls recorded so far.
func (r *StreamSink) EventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// RequireClosed asserts that Close was called.
func (r *StreamSink) RequireClosed(t *testing.T) {
	t.Helper()
	if !r.Closed() {
		t.Error("StreamSink.Close: not called")
	}
}

// RequireEvent asserts at least one emitted event satisfies match.
func (r *StreamSink) RequireEvent(t *testing.T, match func(agentgraph.StreamEvent) bool, desc string) {
	t.Helper()
	for _, ev := range r.Events() {
		if match(ev) {
			return
		}
	}
	t.Errorf("StreamSink.Emit: no event found matching %q (total %d events)", desc, r.EventCount())
}

// RequireTextEvent asserts at least one text event with the given text.
func (r *StreamSink) RequireTextEvent(t *testing.T, text string) {
	t.Helper()
	r.RequireEvent(t, func(ev agentgraph.StreamEvent) bool {
		return ev.Kind == agentgraph.StreamEventText && ev.Text == text
	}, "text="+text)
}

// RequireFinishEvent asserts at least one finish event was emitted.
func (r *StreamSink) RequireFinishEvent(t *testing.T) {
	t.Helper()
	r.RequireEvent(t, func(ev agentgraph.StreamEvent) bool {
		return ev.Kind == agentgraph.StreamEventFinish
	}, "kind=finish")
}

// Reset clears the recorded state.
func (r *StreamSink) Reset() {
	r.mu.Lock()
	r.events = nil
	r.closed = false
	r.mu.Unlock()
}
