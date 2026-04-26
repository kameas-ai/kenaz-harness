// Hook lifecycle for tool execution. Mirrors Claude Code's stdin/stdout
// hook protocol shape: the orchestrator fires PreToolUse before
// dispatching to MCP and PostToolUse after every dispatch (success or
// error). Pre-hooks may short-circuit (Continue=false) or mutate the
// outgoing args; post-hooks are side-effect only.
//
// The HookRunner interface is defined locally rather than imported from
// core/hooks because the existing core/hooks Runner exposes
// RunPreSend / RunPostSend (chat-pipeline events) — there is no
// pre/post-tool-use surface there yet, and per the WP constraints we
// must not extend core/hooks. The rpc-layer adapter passes a runner
// satisfying this local interface; until a real tool-use hook surface
// lands the wiring uses noopHookRunner.
package toolloop

import (
	"context"
	"encoding/json"
	"sync"
)

// PreToolUseEvent is the payload handed to a pre-tool-use hook before
// the orchestrator dispatches the call to the MCP pool. Field shape
// mirrors Claude Code's pre_tool_use protocol.
type PreToolUseEvent struct {
	SessionID string          `json:"session_id"`
	Tool      string          `json:"tool"`
	Server    string          `json:"server"`
	Args      json.RawMessage `json:"args,omitempty"`
	AttemptNo int             `json:"attempt_no"`
}

// PreToolUseResult is the hook's reply. Continue=false short-circuits
// dispatch and surfaces a synthetic tool_blocked result with Reason.
// A non-nil Args replaces the args used for dispatch, letting hooks
// redact / mutate before the call leaves the harness.
type PreToolUseResult struct {
	Continue bool            `json:"continue"`
	Reason   string          `json:"reason,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
}

// PostToolUseEvent is the payload handed to a post-tool-use hook after
// the dispatch completes (success or error). Args carries the
// post-mutation bytes that actually reached the server. Result is the
// raw MCP response on success; Error is non-empty on failure.
//
// ToolCallID is the per-call id assigned by the model (`call.ID` in
// dispatchOne). The artifacts mission uses it to anchor
// SourceRef.ToolCallID on tool-output captures so the UI can backlink
// from an artifact to the originating tool invocation.
type PostToolUseEvent struct {
	SessionID  string          `json:"session_id"`
	Tool       string          `json:"tool"`
	Server     string          `json:"server"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	LatencyMS  int64           `json:"latency_ms"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// PostToolUseListener is a side-effect-only subscriber notified after
// the primary RunPostToolUse runner returns. The artifacts mission
// (WP02) registers a listener that fans tool-output capture into the
// MediaStore + artifacts table without touching the toolloop's hot
// path. Listeners MUST be safe for concurrent use.
type PostToolUseListener func(event PostToolUseEvent)

// HookRunner is the narrow surface the toolloop calls into for the
// pre/post-tool-use lifecycle. Implementations must be safe for
// concurrent use — the loop may invoke RunPreToolUse / RunPostToolUse
// from multiple goroutines once WP04's concurrent dispatch lands.
//
// RegisterPostListener appends a side-effect-only subscriber that the
// implementation invokes synchronously after the primary post-call
// returns. The contract is fan-out: every registered listener sees
// every event, errors are swallowed at the listener boundary, and
// listener panics MUST be recovered by the implementation so one bad
// subscriber cannot poison the dispatch path.
type HookRunner interface {
	RunPreToolUse(ctx context.Context, event PreToolUseEvent) (PreToolUseResult, error)
	RunPostToolUse(ctx context.Context, event PostToolUseEvent)
	RegisterPostListener(listener PostToolUseListener)
}

// noopHookRunner is the loop's default when Config.Hooks is nil:
// every pre-call returns Continue=true with no mutation, and every
// post-call is dropped. Lets tests and pre-WP03 callers that don't
// yet plumb a real runner stay on a working code path.
//
// The default runner still honors RegisterPostListener so callers can
// wire a listener directly against it without a real hook runner —
// the artifacts mission uses this path when no chat-pipeline hooks
// are configured.
type noopHookRunner struct {
	mu        sync.RWMutex
	listeners []PostToolUseListener
}

func (n *noopHookRunner) RunPreToolUse(_ context.Context, _ PreToolUseEvent) (PreToolUseResult, error) {
	return PreToolUseResult{Continue: true}, nil
}

func (n *noopHookRunner) RunPostToolUse(_ context.Context, ev PostToolUseEvent) {
	n.mu.RLock()
	listeners := append([]PostToolUseListener(nil), n.listeners...)
	n.mu.RUnlock()
	for _, l := range listeners {
		invokePostListener(l, ev)
	}
}

func (n *noopHookRunner) RegisterPostListener(listener PostToolUseListener) {
	if listener == nil {
		return
	}
	n.mu.Lock()
	n.listeners = append(n.listeners, listener)
	n.mu.Unlock()
}

// invokePostListener runs one listener with a panic guard. We never
// want a buggy subscriber to take down the toolloop dispatcher.
func invokePostListener(l PostToolUseListener, ev PostToolUseEvent) {
	defer func() {
		_ = recover()
	}()
	l(ev)
}

// NewNoopHookRunner constructs a HookRunner whose pre/post lifecycle
// methods are inert but whose RegisterPostListener fans the post
// event to every registered listener. The artifacts mission (WP02)
// uses this when no chat-pipeline pre/post hooks are configured: the
// rpc chassis holds the runner so it can register the artifacts sink
// after construction without a second listener registration surface.
func NewNoopHookRunner() HookRunner { return &noopHookRunner{} }
