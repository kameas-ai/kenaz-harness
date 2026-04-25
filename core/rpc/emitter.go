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
func (WailsEmitter) Emit(ctx context.Context, topic string, payload any) {
	runtime.EventsEmit(ctx, topic, payload)
}
