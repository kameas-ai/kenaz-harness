package events

import "sync"

// TestEmitter captures every Emit call. Used by tests across the
// bundle mission to assert event ordering and payload shapes.
type TestEmitter struct {
	mu     sync.Mutex
	Events []TestEvent
}

// TestEvent is one captured emission.
type TestEvent struct {
	Kind    string
	Payload any
}

// Emit appends to the captured slice.
func (t *TestEmitter) Emit(kind string, payload any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, TestEvent{Kind: kind, Payload: payload})
}

// Snapshot returns a copy of the captured events. Safe for concurrent
// use with Emit.
func (t *TestEmitter) Snapshot() []TestEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TestEvent, len(t.Events))
	copy(out, t.Events)
	return out
}

// Kinds returns just the kind strings of captured events, in order.
func (t *TestEmitter) Kinds() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.Events))
	for i, e := range t.Events {
		out[i] = e.Kind
	}
	return out
}
