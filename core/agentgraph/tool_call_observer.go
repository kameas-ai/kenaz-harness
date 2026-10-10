package agentgraph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ToolOutcome is the typed result class of one tool call that reached
// dispatch (ml-producer-01MLPRD01 WP02, spec §12 A-5).
//
// The zero value means "not stated": ToolResult.Outcome left empty is
// derived from IsError (and the dispatch error / context) by the kernel,
// so every existing ToolRegistry keeps working unchanged. A registry that
// knows more than IsError can say — the chat adapter knows a permission
// deny from a tool failure — sets it explicitly.
type ToolOutcome string

const (
	// ToolOutcomeOK: the tool ran and did not flag its result as an error.
	ToolOutcomeOK ToolOutcome = "ok"
	// ToolOutcomeError: schema rejection, dispatch failure, a tool that
	// returned IsError, a timeout, or a panic.
	ToolOutcomeError ToolOutcome = "error"
	// ToolOutcomeDenied: refused before the tool ran (Cedar, a
	// pre_tool_use hook, a permission policy, a declined confirmation,
	// the exposure gate).
	ToolOutcomeDenied ToolOutcome = "denied"
	// ToolOutcomeCancelled: the run's context ended while the call was
	// in flight or parked.
	ToolOutcomeCancelled ToolOutcome = "cancelled"
)

// ToolCallRecord is what a ToolCallObserver receives for one call.
//
// Unlike ToolUsageObserver's signature this one DOES carry the raw
// arguments and the result text: the ML producer has to derive a
// minimised path / command prefix / exit code from them (spec §3.2). The
// minimisation is the observer's job; the record itself must never be
// logged or forwarded as-is.
type ToolCallRecord struct {
	// Ctx is the dispatch context (carries runposture, session id, …).
	Ctx       context.Context
	SessionID string
	ToolName  string
	Outcome   ToolOutcome
	// Duration is wall time from the start of this call's dispatch
	// (validation included) to its exit.
	Duration time.Duration
	// RawArgs is the argument JSON as dispatched (post hook rewrite), or
	// the model's own string when the call was rejected before that.
	RawArgs string
	// ResultContent is the tool's own output BEFORE the output cap, so a
	// JSON result (kenaz__bash's exit_code) is still parseable. Empty for
	// calls that never reached the tool.
	ResultContent string
}

// ToolCallObserver is told about EVERY exit of a model-emitted tool call
// in tool_dispatch: schema rejection, Cedar deny, hook deny, cancellation,
// and every completed call. It sits next to ToolUsageObserver (which sees
// completed calls only, as a boolean) and does not replace it.
//
// Implementations must not block: this runs inside the parallel fan-out.
// A panicking observer is recovered and logged; it never fails dispatch.
type ToolCallObserver interface {
	ToolCallCompleted(rec ToolCallRecord)
}

// deriveToolOutcome classifies a call that reached env.Tools.Call. An
// explicit tr.Outcome wins over IsError; a dispatch error is cancelled
// when the run's own context is done (or the error is a cancellation),
// and an error otherwise. A per-call timeout (callCtx's own deadline) is
// an error, not a cancellation: the run is still alive.
func deriveToolOutcome(ctx context.Context, tr ToolResult, callErr error) ToolOutcome {
	if callErr != nil {
		if ctx.Err() != nil || errors.Is(callErr, context.Canceled) {
			return ToolOutcomeCancelled
		}
		return ToolOutcomeError
	}
	if tr.Outcome != "" {
		return tr.Outcome
	}
	if tr.IsError {
		if ctx.Err() != nil {
			return ToolOutcomeCancelled
		}
		return ToolOutcomeError
	}
	return ToolOutcomeOK
}

// reportToolCall is the single ToolCallObserver call site. Nil-safe and
// panic-proof: an observer bug must never surface in the tool fan-out.
func reportToolCall(env *Env, rec ToolCallRecord) {
	if env == nil || env.ToolCalls == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logging.L().Error("agentgraph.tool_call_observer.panic",
				"tool", rec.ToolName, "panic", fmt.Sprintf("%v", r))
		}
	}()
	if rec.Outcome == "" {
		rec.Outcome = ToolOutcomeError
	}
	env.ToolCalls.ToolCallCompleted(rec)
}
