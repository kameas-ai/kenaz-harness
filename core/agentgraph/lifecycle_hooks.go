// lifecycle_hooks.go — LifecycleHookRunner seam and Env wiring for
// hooks-event-surface-expansion-01KZNP3A WP03.
//
// The LifecycleHookRunner interface is the agentgraph-side seam onto
// core/hooks.Runner. Defining the interface here keeps the import
// direction clean: core/agentgraph does not depend on core/hooks;
// the production wiring in core/rpc binds a *hooks.Runner onto the
// seam.
//
// Merge semantics the kernel enforces:
//   - If merged.Blocked, the tool call is denied; env.Tools.Call is
//     not invoked. The tool result carries {is_error: true, content:
//     "hook_denied: <reason>"}.
//   - updated_input rewrites args before dispatch. The kernel emits a
//     pre-existing EventToolCall event with original args, then a new
//     EventHookInputRewrite event recording both the original and the
//     rewritten args so the audit log captures both values.
//   - additional_context from pre_tool_use is appended to the session's
//     system message for the next LLM turn via env.PendingContext.
//   - For post_tool_use, additional_context is similarly enqueued.
//   - Cedar runs AFTER the hook merge; a hook cannot widen a Cedar deny.
package agentgraph

import (
	"context"
	"encoding/json"
)

// LifecycleMergedOutput is the subset of hooks.MergedOutput that the
// kernel acts on. Keeping a local mirror avoids importing core/hooks
// into core/agentgraph.
type LifecycleMergedOutput struct {
	// Blocked is true when at least one hook returned decision="block".
	Blocked bool
	// BlockReason is the first block reason.
	BlockReason string

	// AdditionalContext is the concatenated additional_context from all hooks.
	// The kernel enqueues this as a system message for the next LLM turn.
	AdditionalContext string

	// UpdatedInput replaces the tool-call args when non-nil. The kernel
	// logs both original and rewritten args in the audit event.
	UpdatedInput json.RawMessage

	// UpdatedMCPOutput replaces the MCP tool result when non-nil.
	UpdatedMCPOutput json.RawMessage
}

// LifecycleHookRunner is the seam the kernel calls to fire v2 lifecycle
// hooks (hooks-event-surface-expansion-01KZNP3A). The interface is
// defined here so core/agentgraph has no import dependency on core/hooks.
type LifecycleHookRunner interface {
	// FirePreToolUse fires pre_tool_use hooks and returns a merged result.
	// toolName is the bare tool name; inputJSON is the JSON-encoded args.
	// sessionID / model / kind are for match filtering.
	FirePreToolUse(ctx context.Context, sessionID, toolName string, inputJSON json.RawMessage, model, kind string) (LifecycleMergedOutput, error)

	// FirePostToolUse fires post_tool_use or post_tool_use_failure hooks.
	// isFailure selects the event; errMsg is non-empty on failure.
	FirePostToolUse(ctx context.Context, sessionID, toolName string, inputJSON, outputJSON json.RawMessage, isFailure bool, errMsg, model, kind string) (LifecycleMergedOutput, error)
}

// PendingContextAppender is the optional seam for injecting additional
// system context into the next LLM turn. The kernel threads additional_context
// from pre/post_tool_use hooks through this interface. Nil disables the
// injection (the context is logged but not forwarded to the model).
type PendingContextAppender interface {
	AppendSystemContext(ctx context.Context, sessionID, text string) error
}

// EventHookInputRewrite is the kernel event kind emitted when a hook
// rewrites a tool call's input (pre_tool_use updated_input).
const EventHookInputRewrite = "hook.input_rewrite"

// EventHookDenied is the kernel event kind emitted when a hook blocks a
// tool call (pre_tool_use decision="block").
const EventHookDenied = "hook.denied"
