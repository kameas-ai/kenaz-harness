package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// StreamCloseReason mirrors plan §4.2: ctx-cancelled | stop-called | backend-error.
type StreamCloseReason string

const (
	StreamClosedCtxCancelled StreamCloseReason = "ctx-cancelled"
	StreamClosedStopCalled   StreamCloseReason = "stop-called"
	StreamClosedBackendError StreamCloseReason = "backend-error"
)

// Permission-pending push topics (mission cedar-credential-policy-01KQ8TDE,
// WP02). One topic per resource family. The frontend subscribes to the
// relevant family topic(s) to render the interactive permission modal.
// Payloads are typed permission-request structs emitted by the gate
// hooks when a resource evaluation returns NotApplicable and the
// prompt registry enqueues the decision.
const (
	TopicBashPermissionPending       = "bash:permission-pending"
	TopicCredPermissionPending       = "cred:permission-pending"
	TopicFSPermissionPending         = "fs:permission-pending"
	TopicToolPermissionPending       = "tool:permission-pending"
)

// StreamClosedPayload is the typed payload emitted on `<view>:stream-closed`.
type StreamClosedPayload struct {
	ID      string            `json:"id"`
	Reason  StreamCloseReason `json:"reason"`
	Message string            `json:"message,omitempty"`
}

// StreamBroker owns subscription lifecycle, fan-out, and stream-closed
// payloads (plan §4.2 / WP11). Subscribe takes a typed source channel
// and emits one Wails event per item until the channel closes; close
// triggers a `<view>:stream-closed` payload with the appropriate reason.
//
// THIS IS ONE OF ONLY TWO FILES ALLOWED TO CALL runtime.EventsEmit
// (via the injected Emitter). Direct runtime.EventsEmit usage outside
// emitter.go and stream_broker.go is forbidden by privacy CI invariant
// (scripts/ci/check-emitter-isolation.sh).
type StreamBroker struct {
	emitter Emitter
	subs    sync.Map // map[string]*subscription
	nextID  atomic.Uint64

	// ctx is the Wails OnStartup-supplied context. runtime.EventsEmit
	// REQUIRES this exact context to dispatch — context.Background()
	// fails with "An invalid context was passed". main.go wires this
	// via API.SetContext → broker.SetContext on app startup.
	ctxMu sync.RWMutex
	ctx   context.Context
}

// SetContext threads the Wails-supplied OnStartup context through to
// the broker so direct .Emit (the LLM stream-bridge path) dispatches
// to the live runtime instead of context.Background — the latter
// crashes Wails' EventsEmit with "An invalid context was passed".
func (b *StreamBroker) SetContext(ctx context.Context) {
	b.ctxMu.Lock()
	b.ctx = ctx
	b.ctxMu.Unlock()
}

// EmitCtx returns the configured ctx, falling back to background if
// SetContext hasn't been called yet (test path / pre-startup).
func (b *StreamBroker) EmitCtx() context.Context {
	b.ctxMu.RLock()
	defer b.ctxMu.RUnlock()
	if b.ctx == nil {
		return context.Background()
	}
	return b.ctx
}

type subscription struct {
	id        string
	view      string
	closed    atomic.Bool
	stopFunc  func()
	closeOnce sync.Once
}

// NewStreamBroker constructs a broker with the given Emitter. Inject
// WailsEmitter{} in production; tests inject a recording fake.
func NewStreamBroker(emitter Emitter) *StreamBroker {
	if emitter == nil {
		emitter = WailsEmitter{}
	}
	return &StreamBroker{emitter: emitter}
}

// Subscribe wires a typed source channel to a Wails topic. Returns a
// subscription id stable for the lifetime of the subscription.
func (b *StreamBroker) Subscribe(
	ctx context.Context,
	view, eventKind string,
	source <-chan any,
) (string, error) {
	if view == "" {
		return "", errors.New("stream broker: view required")
	}
	if eventKind == "" {
		eventKind = "event"
	}
	if source == nil {
		return "", errors.New("stream broker: nil source channel")
	}

	id := fmt.Sprintf("%s-%d", view, b.nextID.Add(1))
	topic := fmt.Sprintf("%s:%s", view, eventKind)
	closedTopic := fmt.Sprintf("%s:stream-closed", view)

	sub := &subscription{id: id, view: view}
	subCtx, cancel := context.WithCancel(ctx)
	sub.stopFunc = cancel
	b.subs.Store(id, sub)

	go func() {
		defer b.subs.Delete(id)

		var reason StreamCloseReason = StreamClosedCtxCancelled
		var msg string

		for {
			select {
			case <-subCtx.Done():
				if errors.Is(subCtx.Err(), context.Canceled) {
					if sub.closed.Load() {
						reason = StreamClosedStopCalled
					} else {
						reason = StreamClosedCtxCancelled
					}
				}
				goto closed
			case ev, ok := <-source:
				if !ok {
					reason = StreamClosedBackendError
					msg = "source channel closed unexpectedly"
					goto closed
				}
				b.emitter.Emit(ctx, topic, ev)
			}
		}

	closed:
		sub.closeOnce.Do(func() {
			b.emitter.Emit(ctx, closedTopic, StreamClosedPayload{
				ID:      id,
				Reason:  reason,
				Message: msg,
			})
		})
	}()

	return id, nil
}

// Unsubscribe stops the subscription. The pump emits stream-closed
// with reason=stop-called.
func (b *StreamBroker) Unsubscribe(id string) error {
	v, ok := b.subs.Load(id)
	if !ok {
		return fmt.Errorf("stream broker: subscription %q not found", id)
	}
	sub := v.(*subscription)
	sub.closed.Store(true)
	if sub.stopFunc != nil {
		sub.stopFunc()
	}
	return nil
}
