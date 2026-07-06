//go:build serve

// emitter_serve.go provides CGO-free / Wails-free shims for the emitter
// surface used by serve mode (cmd/harness-served). WailsEmitter is replaced
// with a no-op because there is no Wails runtime in the headless VM binary.
// The EventBus (non-Wails path) remains the only active emission sink.

package rpc

import "context"

// Emitter is the interface satisfied by both WailsEmitter (desktop path) and
// the serve-mode no-op.
type Emitter interface {
	Emit(ctx context.Context, topic string, payload any)
}

// WailsEmitter is a no-op in serve mode — there is no Wails runtime.
type WailsEmitter struct{}

// Emit is a no-op in serve mode.
func (WailsEmitter) Emit(_ context.Context, _ string, _ any) {}

// MultiEmitter fans out a single Emit call to all registered sinks in order.
type MultiEmitter struct {
	sinks []Emitter
}

// NewMultiEmitter constructs a MultiEmitter from the given sinks.
func NewMultiEmitter(sinks ...Emitter) *MultiEmitter {
	return &MultiEmitter{sinks: sinks}
}

// Emit calls Emit on every registered sink.
func (m *MultiEmitter) Emit(ctx context.Context, topic string, payload any) {
	for _, s := range m.sinks {
		s.Emit(ctx, topic, payload)
	}
}
