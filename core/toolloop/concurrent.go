// Concurrent dispatch for the toolloop (WP04).
//
// The model can emit several tool_use calls inside a single assistant
// turn. WP01–WP03 dispatched them serially; WP04 fans them out behind a
// counting semaphore (default cap 4) so independent calls (e.g. read two
// files in parallel) don't queue. Results are merged back into the
// declared order so the augmented conversation looks identical to the
// serial path — the model and the persisted session history never see
// the concurrency.
//
// Cancellation: the host context is the single source of truth. Pool
// implementations honor ctx.Done() inside Call (FR-008 / NFR-004); any
// in-flight call therefore unwinds when the user clicks Stop. Calls that
// haven't begun dispatch (still queued behind the semaphore) skip
// straight to a synthetic tool_cancelled result so the conversation
// thread stays consistent and the post_tool_use hook still fires for
// every declared call.
//
// Streaming: the loop emits ProgressEmitter events when each call starts
// and finishes, so the chat surface can render inline tool chips. The
// stdio mission's WP03 will land progress notifications onto the broker;
// until then the rpc layer wires a noop emitter and the tests inject a
// recording one.
package toolloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// DefaultMaxConcurrentTools is the WP04 fan-out cap when Config leaves
// MaxConcurrentTools unset. Matches the spec's FR-004 default of 4 and
// is small enough that a misbehaving tool can't starve the whole pool.
const DefaultMaxConcurrentTools = 4

// ProgressEmitter is the optional surface the loop calls to surface
// per-tool start/finish events to the chat. It is intentionally narrow:
// the loop owns the payload shape, callers translate to whatever wire
// envelope they ship (the rpc layer wraps it in a stream-chunk topic).
//
// Implementations MUST be safe for concurrent use — the loop fans out
// dispatches behind a semaphore and emits from each goroutine.
type ProgressEmitter interface {
	// EmitToolStarted fires once dispatch begins for a single call (after
	// the permission gate and pre-hook clear). Skipped when the call is
	// blocked, denied, or pre-empted by a context cancellation.
	EmitToolStarted(ctx context.Context, ev ToolProgressEvent)
	// EmitToolFinished fires once the call returns — success, error, or
	// cancellation. LatencyMS is wall time from EmitToolStarted to the
	// terminal outcome.
	EmitToolFinished(ctx context.Context, ev ToolProgressEvent)
}

// ToolProgressEvent is the payload for ProgressEmitter callbacks.
//
// Status values:
//   - "started"   — fired by EmitToolStarted
//   - "ok"        — successful dispatch
//   - "error"     — pool returned a non-cancel error
//   - "cancelled" — host ctx cancelled mid-flight or before dispatch
//   - "blocked"   — permission gate / pre-hook short-circuited the call
type ToolProgressEvent struct {
	SessionID   string `json:"session_id"`
	ParentSubID string `json:"parent_sub_id"`
	ToolUseID   string `json:"tool_use_id"`
	Server      string `json:"server"`
	Tool        string `json:"tool"`
	Status      string `json:"status"`
	LatencyMS   int64  `json:"latency_ms,omitempty"`
	Message     string `json:"message,omitempty"`
}

// noopProgressEmitter is the default when Config.Progress is nil.
type noopProgressEmitter struct{}

func (noopProgressEmitter) EmitToolStarted(context.Context, ToolProgressEvent)  {}
func (noopProgressEmitter) EmitToolFinished(context.Context, ToolProgressEvent) {}

// dispatchTools fans the supplied calls out across a counting semaphore
// and merges results back into the declared (input) order. A nil call
// slice and a per-call concurrency cap of 1 both collapse to the
// pre-WP04 serial behaviour so existing tests continue to pass.
//
// Errors:
//   - The function returns an error only for unrecoverable issues
//     (pool.Tools listing failure). Per-call failures land in the
//     returned []toolResult with IsError=true.
//   - When ctx is cancelled mid-flight, every call still produces an
//     entry: in-flight calls surface the underlying ctx error as a
//     synthetic tool_cancelled result, and queued calls (that never
//     acquired the semaphore) produce a synthetic tool_cancelled
//     result without touching the pool. The final return value is
//     ctx.Err() so the caller can distinguish "loop was cancelled"
//     from "loop completed normally".
func (l *Loop) dispatchTools(ctx context.Context, sessionID, parentSubID string, calls []corellm.ToolUse) ([]toolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	tools, err := l.pool.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("toolloop: pool tools: %w", err)
	}

	results := make([]toolResult, len(calls))

	// Resolve server names sequentially up-front. The catalog is small
	// and Tools() is already a network call — we don't want to repeat
	// it inside every goroutine.
	//
	// The model emits the namespaced name we exported via the tool
	// discoverer ("<server>__<tool>"); split it back into a (server,
	// tool) pair so pool.Call, audit events, and progress events all
	// see the un-namespaced tool name. Calls without the separator
	// keep the WP01 path: scan the catalog for a unique name match.
	// dispatched[i].Name is the un-namespaced tool id; dispatched[i].ID
	// stays equal to the model's original call id for tool_result
	// correlation.
	servers := make([]string, len(calls))
	resolved := make([]bool, len(calls))
	dispatched := make([]corellm.ToolUse, len(calls))
	for i, call := range calls {
		dispatched[i] = call
		if server, tool, ok := splitNamespacedToolName(call.Name); ok {
			servers[i] = server
			dispatched[i].Name = tool
			// Verify the (server, tool) pair is actually in the
			// catalog so a denied / removed tool surfaces as
			// "not registered" instead of a pool error.
			resolved[i] = catalogContains(tools, server, tool)
		} else {
			s, ok := resolveServer(tools, call.Name)
			servers[i] = s
			resolved[i] = ok
		}
	}

	// Single-call fast path: skip the goroutine + semaphore overhead and
	// run inline. Behaviourally identical to the multi-call path; only
	// exists so the most common dispatch shape (one tool per turn) does
	// not pay the parallelism tax.
	if len(calls) == 1 {
		results[0] = l.dispatchOne(ctx, sessionID, parentSubID, servers[0], resolved[0], dispatched[0])
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		return results, nil
	}

	cap := l.maxParallel
	if cap <= 0 {
		cap = DefaultMaxConcurrentTools
	}
	if cap > len(calls) {
		cap = len(calls)
	}
	sem := make(chan struct{}, cap)

	var wg sync.WaitGroup
	wg.Add(len(calls))
	for i := range calls {
		i := i
		go func() {
			defer wg.Done()
			// Acquire the semaphore; honour ctx so a cancellation while
			// queued doesn't wait for the entire batch to drain.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = l.synthCancelled(ctx, sessionID, parentSubID, servers[i], dispatched[i])
				return
			}
			defer func() { <-sem }()

			// Re-check ctx after acquiring — between queue entry and
			// goroutine wake the host ctx may have been cancelled.
			if ctx.Err() != nil {
				results[i] = l.synthCancelled(ctx, sessionID, parentSubID, servers[i], dispatched[i])
				return
			}
			results[i] = l.dispatchOne(ctx, sessionID, parentSubID, servers[i], resolved[i], dispatched[i])
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return results, ctx.Err()
	}
	return results, nil
}

// synthCancelled builds the tool_cancelled record for a call that never
// reached pool.Call because the host ctx was cancelled before / during
// queueing. The post_tool_use hook still fires (with Error="context
// canceled") so audit/memory pipelines see a complete record per FR-008.
func (l *Loop) synthCancelled(ctx context.Context, sessionID, parentSubID, server string, call corellm.ToolUse) toolResult {
	cancelMsg := "context canceled"
	if ce := ctx.Err(); ce != nil {
		cancelMsg = ce.Error()
	}
	emitToolFailed(context.Background(), l.audit, sessionID, parentSubID, server, call.Name, call.Input, cancelMsg, 0)
	// post_tool_use fires with the cancellation error so audit / memory
	// pipelines see the dropped call. We use a fresh Background ctx here
	// because the host ctx is already done — the hook should still get
	// to run synchronously.
	l.hooks.RunPostToolUse(context.Background(), PostToolUseEvent{
		SessionID:  sessionID,
		Tool:       call.Name,
		Server:     server,
		Args:       call.Input,
		Error:      cancelMsg,
		LatencyMS:  0,
		ToolCallID: call.ID,
	})
	l.progress.EmitToolFinished(context.Background(), ToolProgressEvent{
		SessionID:   sessionID,
		ParentSubID: parentSubID,
		ToolUseID:   call.ID,
		Server:      server,
		Tool:        call.Name,
		Status:      "cancelled",
		Message:     cancelMsg,
	})
	return toolResult{
		ToolUseID: call.ID,
		Server:    server,
		Tool:      call.Name,
		Output:    "Tool cancelled: " + cancelMsg,
		IsError:   true,
		Cancelled: true,
	}
}

// dispatchOne runs the full per-call lifecycle: server resolution check,
// permission gate, pre-hook, pool.Call, post-hook, audit, progress
// emission. Designed to be called from a goroutine; all collaborators
// (hooks runner, audit emitter, permission resolver, pool) are required
// to be safe for concurrent use.
func (l *Loop) dispatchOne(
	ctx context.Context,
	sessionID, parentSubID, server string,
	resolved bool,
	call corellm.ToolUse,
) toolResult {
	log := logging.L()

	if !resolved {
		// Unknown tool name — synthesize a blocked result without
		// touching the pool, hooks, or progress (no dispatch ever
		// began for this call).
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, "", call.Name, call.Input,
			fmt.Sprintf("tool %q not registered", call.Name), 0)
		return toolResult{
			ToolUseID: call.ID,
			Tool:      call.Name,
			Output:    fmt.Sprintf("tool %q not registered", call.Name),
			IsError:   true,
		}
	}

	// Permission gate.
	res, permErr := l.perms.Resolve(ctx, sessionID, server, call.Name)
	if permErr != nil {
		log.Warn("toolloop.permission.resolver_failed",
			"sub_id", parentSubID,
			"session_id", sessionID,
			"server", server,
			"tool", call.Name,
			"err", permErr.Error(),
		)
		reason := fmt.Sprintf("permission resolver error: %s", permErr.Error())
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input, reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    reason,
			IsError:   true,
		}
	}
	log.Info("toolloop.permission.resolved",
		"sub_id", parentSubID,
		"session_id", sessionID,
		"server", server,
		"tool", call.Name,
		"policy", string(res.Policy),
	)
	switch res.Policy {
	case PolicyDeny:
		reason := res.Reason
		if reason == "" {
			reason = "tool not authorised for this session"
		}
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input,
			"Tool blocked: "+reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}
	case PolicyConfirmEach:
		if l.confirmEnabled && l.confirm != nil {
			confirmRes, ok := l.runConfirmGate(ctx, sessionID, parentSubID, server, call, res.Reason)
			if !ok {
				return confirmRes
			}
		}
		// confirm flag off OR confirm.allow — fall through to dispatch.
	case PolicyAutoAllow:
		// Existing dispatch path.
	default:
		reason := fmt.Sprintf("Tool blocked: unknown policy %q", res.Policy)
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input, reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    reason,
			IsError:   true,
		}
	}

	// Pre-tool-use hook.
	dispatchArgs := call.Input
	started := time.Now()
	preEv := PreToolUseEvent{
		SessionID: sessionID,
		Tool:      call.Name,
		Server:    server,
		Args:      dispatchArgs,
		AttemptNo: 1,
	}
	preRes, preErr := l.hooks.RunPreToolUse(ctx, preEv)
	if preErr != nil {
		log.Warn("toolloop.hook.pretool_failed",
			"sub_id", parentSubID,
			"session_id", sessionID,
			"server", server,
			"tool", call.Name,
			"err", preErr.Error(),
		)
		reason := "pre-hook error: " + preErr.Error()
		latencyMS := time.Since(started).Milliseconds()
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, dispatchArgs, reason, latencyMS)
		l.hooks.RunPostToolUse(ctx, PostToolUseEvent{
			SessionID:  sessionID,
			Tool:       call.Name,
			Server:     server,
			Args:       dispatchArgs,
			Error:      reason,
			LatencyMS:  latencyMS,
			ToolCallID: call.ID,
		})
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}
	}
	if !preRes.Continue {
		reason := preRes.Reason
		if reason == "" {
			reason = "blocked by pre-hook"
		}
		latencyMS := time.Since(started).Milliseconds()
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, dispatchArgs,
			"Tool blocked: "+reason, latencyMS)
		l.hooks.RunPostToolUse(ctx, PostToolUseEvent{
			SessionID:  sessionID,
			Tool:       call.Name,
			Server:     server,
			Args:       dispatchArgs,
			Error:      "Tool blocked: " + reason,
			LatencyMS:  latencyMS,
			ToolCallID: call.ID,
		})
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}
	}
	if preRes.Args != nil {
		dispatchArgs = preRes.Args
	}

	// Tool dispatch is starting in earnest — surface to the chat.
	l.progress.EmitToolStarted(ctx, ToolProgressEvent{
		SessionID:   sessionID,
		ParentSubID: parentSubID,
		ToolUseID:   call.ID,
		Server:      server,
		Tool:        call.Name,
		Status:      "started",
	})

	raw, callErr := l.pool.Call(ctx, server, call.Name, dispatchArgs)
	latencyMS := time.Since(started).Milliseconds()
	if callErr != nil {
		// Distinguish cancellation from a normal tool failure. The MCP
		// pool implementation honors ctx.Done() — when the host context
		// is canceled, callErr surfaces as context.Canceled (or
		// DeadlineExceeded). The model receives a synthetic
		// tool_cancelled result so it can see the user pulled the cord.
		isCancel := errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) || ctx.Err() != nil
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, dispatchArgs,
			callErr.Error(), latencyMS)
		// Use a fresh Background context for the post-hook when the
		// host ctx is already done; the hook still needs to run.
		hookCtx := ctx
		if isCancel {
			hookCtx = context.Background()
		}
		l.hooks.RunPostToolUse(hookCtx, PostToolUseEvent{
			SessionID:  sessionID,
			Tool:       call.Name,
			Server:     server,
			Args:       dispatchArgs,
			Error:      callErr.Error(),
			LatencyMS:  latencyMS,
			ToolCallID: call.ID,
		})
		status := "error"
		if isCancel {
			status = "cancelled"
		}
		l.progress.EmitToolFinished(hookCtx, ToolProgressEvent{
			SessionID:   sessionID,
			ParentSubID: parentSubID,
			ToolUseID:   call.ID,
			Server:      server,
			Tool:        call.Name,
			Status:      status,
			LatencyMS:   latencyMS,
			Message:     callErr.Error(),
		})
		out := toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    callErr.Error(),
			IsError:   true,
			Cancelled: isCancel,
		}
		if isCancel {
			out.Output = "Tool cancelled: " + callErr.Error()
		}
		return out
	}
	emitToolInvoked(ctx, l.audit, sessionID, parentSubID, server, call.Name, dispatchArgs, latencyMS)
	l.hooks.RunPostToolUse(ctx, PostToolUseEvent{
		SessionID:  sessionID,
		Tool:       call.Name,
		Server:     server,
		Args:       dispatchArgs,
		Result:     raw,
		LatencyMS:  latencyMS,
		ToolCallID: call.ID,
	})
	l.progress.EmitToolFinished(ctx, ToolProgressEvent{
		SessionID:   sessionID,
		ParentSubID: parentSubID,
		ToolUseID:   call.ID,
		Server:      server,
		Tool:        call.Name,
		Status:      "ok",
		LatencyMS:   latencyMS,
	})
	return toolResult{
		ToolUseID: call.ID,
		Server:    server,
		Tool:      call.Name,
		Output:    string(raw),
	}
}

// runConfirmGate handles the confirm_each modal flow. Returns
// (toolResult, false) when the loop should short-circuit dispatch
// (deny / always_deny / ctx-cancel / gateway error), or (zero, true)
// when dispatch should proceed (allow / always_allow). always_allow /
// always_deny additionally write a session-scope override before
// returning so subsequent calls in the same session skip the modal.
//
// Cancellation: ctx going down during the wait surfaces as a
// tool_cancelled synthetic record (matches the queued-cancel path in
// dispatchTools so the conversation thread stays consistent).
//
// Gateway errors are treated as a block-with-reason — the call doesn't
// silently succeed if the modal plumbing breaks; surfacing the error
// to the model lets it apologize / retry through a different tool.
func (l *Loop) runConfirmGate(
	ctx context.Context,
	sessionID, parentSubID, server string,
	call corellm.ToolUse,
	resolverReason string,
) (toolResult, bool) {
	log := logging.L()

	req := ConfirmRequest{
		RequestID:    newConfirmRequestID(),
		SessionID:    sessionID,
		ParentSubID:  parentSubID,
		Server:       server,
		Tool:         call.Name,
		ToolUseID:    call.ID,
		Args:         call.Input,
		ArgsRedacted: string(call.Input),
		Reason:       resolverReason,
	}
	log.Info("toolloop.confirm.requested",
		"sub_id", parentSubID,
		"session_id", sessionID,
		"server", server,
		"tool", call.Name,
		"request_id", req.RequestID,
	)
	decision, err := l.confirm.RequestConfirm(ctx, req)
	if err != nil {
		// Distinguish ctx cancellation from a genuine gateway fault.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			cancelMsg := err.Error()
			emitToolFailed(context.Background(), l.audit, sessionID, parentSubID, server, call.Name, call.Input, cancelMsg, 0)
			l.hooks.RunPostToolUse(context.Background(), PostToolUseEvent{
				SessionID:  sessionID,
				Tool:       call.Name,
				Server:     server,
				Args:       call.Input,
				Error:      cancelMsg,
				LatencyMS:  0,
				ToolCallID: call.ID,
			})
			l.progress.EmitToolFinished(context.Background(), ToolProgressEvent{
				SessionID:   sessionID,
				ParentSubID: parentSubID,
				ToolUseID:   call.ID,
				Server:      server,
				Tool:        call.Name,
				Status:      "cancelled",
				Message:     cancelMsg,
			})
			return toolResult{
				ToolUseID: call.ID,
				Server:    server,
				Tool:      call.Name,
				Output:    "Tool cancelled: " + cancelMsg,
				IsError:   true,
				Cancelled: true,
			}, false
		}
		log.Warn("toolloop.confirm.gateway_failed",
			"sub_id", parentSubID, "session_id", sessionID,
			"server", server, "tool", call.Name, "err", err.Error())
		reason := "confirm gateway error: " + err.Error()
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input, reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}, false
	}
	log.Info("toolloop.confirm.decided",
		"sub_id", parentSubID,
		"session_id", sessionID,
		"server", server,
		"tool", call.Name,
		"request_id", req.RequestID,
		"decision", string(decision),
	)
	switch decision {
	case ConfirmAllow:
		return toolResult{}, true
	case ConfirmAlwaysAllow:
		l.persistOverride(ctx, sessionID, server, call.Name, PolicyAutoAllow, "user: always allow this session")
		return toolResult{}, true
	case ConfirmDeny:
		reason := "blocked by user"
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input,
			"Tool blocked: "+reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}, false
	case ConfirmAlwaysDeny:
		l.persistOverride(ctx, sessionID, server, call.Name, PolicyDeny, "user: always deny this session")
		reason := "blocked by user (always deny)"
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input,
			"Tool blocked: "+reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}, false
	default:
		// Defensive — gateways returning an unknown decision are
		// treated as deny so a misbehaving frontend can't silently
		// allow tool calls.
		reason := fmt.Sprintf("unknown confirm decision %q", decision)
		emitToolFailed(ctx, l.audit, sessionID, parentSubID, server, call.Name, call.Input, "Tool blocked: "+reason, 0)
		return toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    "Tool blocked: " + reason,
			IsError:   true,
		}, false
	}
}

// persistOverride writes an exact (server, tool) override for the
// session at the supplied policy. Failure is non-fatal: we log a warn
// and continue — the user's per-call decision is still honored, only
// the "always" persistence drops. This keeps the modal flow robust to
// transient storage hiccups.
func (l *Loop) persistOverride(ctx context.Context, sessionID, server, tool string, policy ToolPolicy, reason string) {
	if l.overrideW == nil {
		return
	}
	if err := l.overrideW.SetMCPOverride(ctx, sessionID, MCPOverride{
		Server: server,
		Tool:   tool,
		Policy: policy,
		Reason: reason,
	}); err != nil {
		logging.L().Warn("toolloop.confirm.override_write_failed",
			"session_id", sessionID,
			"server", server,
			"tool", tool,
			"policy", string(policy),
			"err", err.Error(),
		)
	}
}

// newConfirmRequestID returns a 16-byte hex-encoded random id. We
// don't need ULID ordering here — the id is purely a correlation key
// across the gateway / frontend / response edge.
func newConfirmRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is process-fatal everywhere else in
		// the codebase; return a degenerate id rather than panic so
		// the loop still surfaces a tool_blocked instead of crashing
		// the chat surface.
		return "confirm-rand-failed"
	}
	return "confirm-" + hex.EncodeToString(b[:])
}
