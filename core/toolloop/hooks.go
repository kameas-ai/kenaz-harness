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
type PostToolUseEvent struct {
	SessionID string          `json:"session_id"`
	Tool      string          `json:"tool"`
	Server    string          `json:"server"`
	Args      json.RawMessage `json:"args,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
}

// HookRunner is the narrow surface the toolloop calls into for the
// pre/post-tool-use lifecycle. Implementations must be safe for
// concurrent use — the loop may invoke RunPreToolUse / RunPostToolUse
// from multiple goroutines once WP04's concurrent dispatch lands.
type HookRunner interface {
	RunPreToolUse(ctx context.Context, event PreToolUseEvent) (PreToolUseResult, error)
	RunPostToolUse(ctx context.Context, event PostToolUseEvent)
}

// noopHookRunner is the loop's default when Config.Hooks is nil:
// every pre-call returns Continue=true with no mutation, and every
// post-call is dropped. Lets tests and pre-WP03 callers that don't
// yet plumb a real runner stay on a working code path.
type noopHookRunner struct{}

func (noopHookRunner) RunPreToolUse(_ context.Context, _ PreToolUseEvent) (PreToolUseResult, error) {
	return PreToolUseResult{Continue: true}, nil
}

func (noopHookRunner) RunPostToolUse(_ context.Context, _ PostToolUseEvent) {}
