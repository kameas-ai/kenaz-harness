// ConfirmGateway implementation backing WP05's confirm-each modal.
//
// The toolloop's confirm_each policy pauses dispatch and asks this
// gateway whether to allow the call. We surface the request to the
// frontend on the existing `llm:stream-chunk` topic via a new
// StreamConfirmRequest chunk shape — reusing the chat surface's pump
// keeps the wiring narrow (no new event broker patterns per WP
// constraints) and means the modal can subscribe with the same
// useEventStream composable the rest of the chat already uses.
//
// Flow:
//
//	1. Loop calls RequestConfirm(req) and blocks on the response chan.
//	2. Gateway emits {kind:"tool_confirm_request", request_id, …} on
//	   llm:stream-chunk via the StreamSink.
//	3. Frontend modal subscribes, renders, and on a button click calls
//	   the LLM_ResolveConfirm Wails binding.
//	4. Binding routes to ResolveConfirm(request_id, decision) which
//	   flips the pending-map entry and unblocks the goroutine.
//	5. Gateway returns the decision; loop dispatches / blocks.
//
// Cancellation: ctx going down (Stop button) drops the pending entry
// and surfaces ctx.Err(); the toolloop's runConfirmGate translates
// that into a tool_cancelled record.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// ConfirmRequestChunkKind is the StreamEvent.Kind value the gateway
// emits when it surfaces a confirmation request on the chat
// stream-chunk topic. Frontend listens for this kind on the
// llm:stream-chunk subscription and renders the modal.
const ConfirmRequestChunkKind = "tool_confirm_request"

// ConfirmRequestPayload is the typed wire payload shipped on
// llm:stream-chunk when a confirm-each tool call pauses. It mirrors
// toolloop.ConfirmRequest so the frontend doesn't have to reach into
// core/toolloop types.
type ConfirmRequestPayload struct {
	RequestID    string `json:"request_id"`
	SessionID    string `json:"session_id"`
	ParentSubID  string `json:"parent_sub_id"`
	Server       string `json:"server"`
	Tool         string `json:"tool"`
	ToolUseID    string `json:"tool_use_id"`
	ArgsRedacted string `json:"args_redacted,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// confirmEntry is one pending confirmation: a one-shot channel the
// gateway closes (or sends a decision on) when the frontend resolves.
type confirmEntry struct {
	decision chan toolloop.ConfirmDecision
}

// ConfirmGateway is the broker between the toolloop (which blocks on
// RequestConfirm) and the frontend (which calls ResolveConfirm via a
// Wails binding). Safe for concurrent use: every entry has its own
// channel and the pending-map is mutex-guarded.
type ConfirmGateway struct {
	sink StreamSink

	mu      sync.Mutex
	pending map[string]*confirmEntry
}

// NewConfirmGateway constructs a gateway. The sink is the same
// StreamSink the rest of the LLM impl uses — chunks emit on
// `llm:stream-chunk` so the frontend's existing chat-stream
// subscription picks them up.
func NewConfirmGateway(sink StreamSink) *ConfirmGateway {
	if sink == nil {
		sink = nopSink{}
	}
	return &ConfirmGateway{
		sink:    sink,
		pending: make(map[string]*confirmEntry),
	}
}

// RequestConfirm blocks until the frontend resolves the request via
// ResolveConfirm or ctx is cancelled. Returns the user's decision or
// ctx.Err().
func (g *ConfirmGateway) RequestConfirm(ctx context.Context, req toolloop.ConfirmRequest) (toolloop.ConfirmDecision, error) {
	if g == nil {
		return toolloop.ConfirmAllow, nil
	}
	entry := &confirmEntry{decision: make(chan toolloop.ConfirmDecision, 1)}
	g.mu.Lock()
	g.pending[req.RequestID] = entry
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.pending, req.RequestID)
		g.mu.Unlock()
	}()

	// Surface the request on the chat stream-chunk topic. The chunk
	// envelope reuses the existing StreamChunkPayload shape so the
	// frontend's stream-chunk subscription handles it without changes
	// to the broker.
	g.sink.Emit("llm:stream-chunk", StreamChunkPayload{
		SubID:     req.ParentSubID,
		SessionID: req.SessionID,
		Chunk: corellm.StreamEvent{
			Kind: corellm.StreamEventKind(ConfirmRequestChunkKind),
			Text: req.RequestID, // hint for non-typed listeners
		},
	})
	// Also emit a typed-payload sibling on llm:tool-confirm-request so
	// the frontend modal can subscribe to a narrowly-scoped topic
	// without parsing every stream chunk. We keep the stream-chunk
	// emission too so the existing chat pump sees the pause.
	g.sink.Emit("llm:tool-confirm-request", ConfirmRequestPayload{
		RequestID:    req.RequestID,
		SessionID:    req.SessionID,
		ParentSubID:  req.ParentSubID,
		Server:       req.Server,
		Tool:         req.Tool,
		ToolUseID:    req.ToolUseID,
		ArgsRedacted: req.ArgsRedacted,
		Reason:       req.Reason,
	})

	logging.L().Info("toolloop.confirm.gateway_request",
		"session_id", req.SessionID,
		"request_id", req.RequestID,
		"server", req.Server,
		"tool", req.Tool,
	)

	select {
	case d := <-entry.decision:
		return d, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ResolveConfirm flips the pending entry for requestID and unblocks
// the goroutine waiting in RequestConfirm. Returns an error when the
// request id is unknown (e.g. the toolloop already cancelled or the
// frontend is replaying a stale event).
func (g *ConfirmGateway) ResolveConfirm(requestID string, decision toolloop.ConfirmDecision) error {
	if g == nil {
		return errors.New("llm: confirm gateway not wired")
	}
	switch decision {
	case toolloop.ConfirmAllow, toolloop.ConfirmDeny,
		toolloop.ConfirmAlwaysAllow, toolloop.ConfirmAlwaysDeny:
	default:
		return fmt.Errorf("llm: unknown confirm decision %q", decision)
	}
	g.mu.Lock()
	entry, ok := g.pending[requestID]
	g.mu.Unlock()
	if !ok {
		return fmt.Errorf("llm: confirm request %q not pending", requestID)
	}
	select {
	case entry.decision <- decision:
		return nil
	default:
		// Channel buffered with cap=1; a second resolve is a no-op.
		return nil
	}
}
