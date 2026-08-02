//go:build !serve

package rpc

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Emitter is the only authorised caller of runtime.EventsEmit
// (plan §4.2). scripts/ci/check-emitter-isolation.sh greps for any
// other caller and fails the build if it finds one.
type Emitter interface {
	Emit(ctx context.Context, topic string, payload any)
}

// WailsEmitter wraps runtime.EventsEmit. The Wails ctx must be the one
// passed to the Wails OnStartup hook for runtime to dispatch correctly.
type WailsEmitter struct{}

// Emit dispatches a payload on the given topic. THIS IS ONE OF ONLY
// TWO FILES ALLOWED TO CALL runtime.EventsEmit (the other is
// stream_broker.go).
//
// The nil-runtime guard is load-bearing, not defensive noise: Wails'
// runtime.EventsEmit calls log.Fatalf — i.e. os.Exit — when the context
// it is handed does not carry the Wails event dispatcher. Any emit that
// happens before OnStartup wires that context, or from a build that
// links the desktop emitter without running a Wails app (served mode
// compiled without the `serve` tag; any test that drives a real core),
// would therefore take the entire process down. Dropping one desktop
// event is a strictly better outcome, and it is not silent data loss:
// the MultiEmitter's EventBus sink still delivers, which is the sink
// served mode actually reads.
func (WailsEmitter) Emit(ctx context.Context, topic string, payload any) {
	if ctx == nil || ctx.Value("events") == nil { //nolint:staticcheck // Wails' own key type
		return
	}
	runtime.EventsEmit(ctx, topic, payload)
}

// MultiEmitter fans out a single Emit call to all registered sinks in
// order.  The desktop Wails path and the in-process EventBus path are
// both wired here in served mode so neither path needs to know about the
// other.  The Wails privacy CI invariant (only emitter.go and
// stream_broker.go call runtime.EventsEmit) is preserved because every
// sink that reaches runtime.EventsEmit does so through WailsEmitter.
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
