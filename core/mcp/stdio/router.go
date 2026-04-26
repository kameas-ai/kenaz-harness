package stdio

import (
	"log/slog"
	"sync"
)

// ResponseRouter correlates outbound JSON-RPC request ids with the
// goroutine waiting on each response. One router per
// ServerInstance.
//
// The lifecycle of a single Call is:
//
//  1. Caller asks for a fresh id and Register(id) — receives a
//     buffered channel of capacity 1.
//  2. Caller writes the request envelope on the framer.
//  3. Reader goroutine, on a matching response envelope, calls
//     Deliver(id, env). The send is non-blocking so a late delivery
//     after Cancel doesn't wedge the reader.
//  4. Caller reads from the channel; on success, Cancel(id) is
//     called to free the map entry. On ctx cancellation the caller
//     calls Cancel(id) too — Deliver after Cancel is dropped.
//
// The non-blocking deliver semantics matter: if the caller's
// context fires between Register and Deliver, the map entry is
// removed; the response — which the spec allows even after
// cancellation — is logged at debug and discarded rather than
// blocking the reader.
//
// Deliver holds the router lock for the duration of its
// non-blocking send. That keeps Cancel from racing with a
// concurrent Deliver: closing a channel that someone is sending on
// would panic and take down the reader. The cost — a single
// channel-send under a lock — is irrelevant for the message rates
// MCP traffic produces.
type ResponseRouter struct {
	logger *slog.Logger

	mu      sync.Mutex
	pending map[int64]chan RawMessage
}

// NewResponseRouter constructs an empty router.
func NewResponseRouter(logger *slog.Logger) *ResponseRouter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResponseRouter{
		logger:  logger,
		pending: make(map[int64]chan RawMessage),
	}
}

// Register reserves a slot for id. The returned channel has
// capacity 1 so Deliver never blocks even when the recipient is
// not yet selecting on it. Calling Register with an id already
// registered overwrites the previous channel and closes it — that
// only happens when the caller of CallTool reuses an id, which the
// pool's atomic.Int64 prevents.
func (r *ResponseRouter) Register(id int64) <-chan RawMessage {
	ch := make(chan RawMessage, 1)
	r.mu.Lock()
	if existing, ok := r.pending[id]; ok {
		close(existing)
	}
	r.pending[id] = ch
	r.mu.Unlock()
	return ch
}

// Deliver hands an envelope to the goroutine waiting on id. Late
// deliveries (after Cancel) are dropped through a non-blocking
// select. Returns true when the envelope reached a waiter.
//
// The send is performed under the router lock so that Cancel
// cannot close the channel between our map lookup and the send —
// closing-while-sending would panic.
func (r *ResponseRouter) Deliver(id int64, env RawMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.pending[id]
	if !ok {
		r.logger.Debug("stdio.router.late_delivery", "id", id)
		return false
	}
	select {
	case ch <- env:
		return true
	default:
		// The caller already drained or never pulled; either way we
		// must not block the reader loop.
		r.logger.Debug("stdio.router.dropped", "id", id)
		return false
	}
}

// Cancel removes id from the map and closes its channel so any
// goroutine still blocked on it unblocks with a zero-value receive.
// Callers must use ctx.Err() (not the channel's value) to
// distinguish a real response from a cancellation. Idempotent.
func (r *ResponseRouter) Cancel(id int64) {
	r.mu.Lock()
	ch, ok := r.pending[id]
	if ok {
		delete(r.pending, id)
	}
	r.mu.Unlock()
	if ok {
		close(ch)
	}
}

// CancelAll closes every pending channel. Used by ServerInstance
// shutdown to unblock in-flight callers when the server goes away.
func (r *ResponseRouter) CancelAll() {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[int64]chan RawMessage)
	r.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

// Pending returns the count of registered ids. Test-only helper —
// production code shouldn't depend on this.
func (r *ResponseRouter) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}
