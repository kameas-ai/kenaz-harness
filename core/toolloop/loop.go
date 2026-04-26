// Package toolloop is the orchestrator that closes the LLM ↔ tool ↔ LLM
// loop. When a provider adapter signals FinishReason == "tool_use" with
// one or more accumulated ToolUse calls, the loop dispatches each call
// against the configured MCP pool, threads the result back into the
// conversation as a tool-role message, and re-invokes the registry until
// the model returns a non-tool_use finish.
//
// WP01 scope: single-tool happy path. No permissions, no hooks, no
// concurrency, no cancellation, no per-tool iteration cap beyond the
// raw maxIter guard, no UI streaming feedback. Subsequent WPs layer in
// those concerns; the public Run signature is the stable surface the
// pump integrates against.
package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// DefaultMaxIter is the WP01 ceiling on tool-use rounds per user turn.
// It exists only so a buggy fake / model that never returns end_turn
// cannot hang the loop test forever — the spec's user-facing iteration
// cap (FR-006) lands properly in WP04.
const DefaultMaxIter = 8

// Config bundles the collaborators the loop needs. The constructor
// validates required fields; nil values for optional fields keep the
// loop usable in tests.
type Config struct {
	Registry corellm.Registry
	Pool     MCPPool
	History  SessionHistoryRW
	// Permissions resolves the per-call (server, tool) policy between
	// tool detection and dispatch. nil falls back to a built-in
	// auto_allow resolver so the WP01 chat path stays untouched and
	// callers that don't yet plumb a real resolver still work.
	Permissions PermissionResolver
	// Hooks fires pre/post-tool-use lifecycle hooks around every
	// dispatch. nil falls back to a noop runner so callers that don't
	// yet plumb a real runner still work.
	Hooks HookRunner
	// Audit emits tool_invoked / tool_failed audit events for every
	// dispatch (success, failure, blocked). nil disables emission;
	// production wiring is expected to pass an event-log-backed
	// emitter that runs the redaction pipeline on Append (NFR-003).
	Audit AuditEmitter
	// MaxIter caps the number of tool-use rounds per Run invocation.
	// Zero falls back to DefaultMaxIter.
	MaxIter int
	// MaxConcurrentTools caps how many tool_use calls from a single
	// assistant turn dispatch in parallel (FR-004). Zero falls back to
	// DefaultMaxConcurrentTools (4); a value of 1 reproduces the WP01
	// serial behaviour, which is the safest fallback for pools that are
	// not yet thread-safe.
	MaxConcurrentTools int
	// Progress, when non-nil, receives EmitToolStarted / EmitToolFinished
	// callbacks for every dispatch so the chat surface can render inline
	// tool chips (FR-010). nil silences progress emission; the rpc layer
	// wires a stream-sink-backed implementation in production.
	Progress ProgressEmitter
	// Confirm, when non-nil AND ConfirmEachEnabled is true, is invoked
	// before dispatch when the permission resolver returns
	// PolicyConfirmEach. The gateway surfaces the request to the user
	// and blocks until a decision (allow / deny / always_allow /
	// always_deny) arrives or ctx is cancelled. nil falls back to a
	// noop gateway that always allows (degrades confirm_each to
	// auto_allow).
	Confirm ConfirmGateway
	// ConfirmEachEnabled gates the entire confirm-each modal flow. When
	// false the loop treats PolicyConfirmEach as PolicyAutoAllow and
	// the gateway is never consulted — preserves WP04 behaviour for
	// users who toggle the flag off in settings. Default false at the
	// type-zero value; the rpc layer reads Settings.ConfirmEachEnabled
	// (default true) when wiring.
	ConfirmEachEnabled bool
	// OverrideWriter persists per-session (server, tool) overrides for
	// the always_allow / always_deny decisions. nil falls back to a
	// noop writer; without persistence those decisions degrade to a
	// per-call allow / deny.
	OverrideWriter SessionOverrideWriter
}

// Loop is the orchestrator. Construct via New and invoke Run from the
// rpc pump after the initial stream closes with finish_reason=tool_use.
type Loop struct {
	reg                corellm.Registry
	pool               MCPPool
	history            SessionHistoryRW
	perms              PermissionResolver
	hooks              HookRunner
	audit              AuditEmitter
	progress           ProgressEmitter
	confirm            ConfirmGateway
	confirmEnabled     bool
	overrideW          SessionOverrideWriter
	maxIter            int
	maxParallel        int
}

// New constructs a Loop. Returns an error if the registry or pool is
// missing — those are the two non-optional collaborators. History may
// be nil for tests that only care about the request/response wiring.
func New(cfg Config) (*Loop, error) {
	if cfg.Registry == nil {
		return nil, errors.New("toolloop: registry required")
	}
	if cfg.Pool == nil {
		return nil, errors.New("toolloop: pool required")
	}
	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = DefaultMaxIter
	}
	perms := cfg.Permissions
	if perms == nil {
		perms = allowAllResolver{}
	}
	hooks := cfg.Hooks
	if hooks == nil {
		hooks = noopHookRunner{}
	}
	progress := cfg.Progress
	if progress == nil {
		progress = noopProgressEmitter{}
	}
	maxParallel := cfg.MaxConcurrentTools
	if maxParallel <= 0 {
		maxParallel = DefaultMaxConcurrentTools
	}
	confirm := cfg.Confirm
	if confirm == nil {
		confirm = noopConfirmGateway{}
	}
	overrideW := cfg.OverrideWriter
	if overrideW == nil {
		overrideW = NoopSessionOverrideWriter{}
	}
	return &Loop{
		reg:            cfg.Registry,
		pool:           cfg.Pool,
		history:        cfg.History,
		perms:          perms,
		hooks:          hooks,
		audit:          cfg.Audit,
		progress:       progress,
		confirm:        confirm,
		confirmEnabled: cfg.ConfirmEachEnabled,
		overrideW:      overrideW,
		maxIter:        maxIter,
		maxParallel:    maxParallel,
	}, nil
}

// Run continues the conversation after the initial stream signaled
// tool_use. The caller passes the terminal Response (with accumulated
// ToolCalls) and the original GenerationRequest so the loop can build
// augmented requests. parentSubID is forwarded to logs so a single
// user turn correlates across pump → loop → MCP server.
//
// Behavior:
//   - response.FinishReason != "tool_use" → return nil (no work).
//   - Otherwise: dispatch each ToolUse, append a tool-role message to
//     the session history, re-invoke reg.Stream with the augmented
//     request. Repeat until a non-tool_use finish or maxIter is hit.
//
// The function is synchronous: it returns only after the final
// (non-tool-use) assistant turn closes. The pump's caller is
// responsible for emitting any frontend events; WP01 deliberately
// keeps the loop UI-mute so future WPs can layer in chunk emission
// without restructuring this control flow.
func (l *Loop) Run(
	ctx context.Context,
	sessionID string,
	parentSubID string,
	response *corellm.Response,
	request corellm.GenerationRequest,
) error {
	if response == nil {
		return errors.New("toolloop: nil response")
	}
	if response.FinishReason != "tool_use" {
		return nil
	}

	log := logging.L()
	log.Info("toolloop.run.start",
		"sub_id", parentSubID,
		"session_id", sessionID,
		"tool_calls", len(response.ToolCalls),
	)

	// augmented carries the conversation we hand to the registry on
	// each iteration. We seed it with the original request's messages
	// and append (assistant tool_use turn, tool result turn, …) as we
	// loop.
	augmented := append([]corellm.Message(nil), request.Messages...)
	current := response

	for iter := 0; iter < l.maxIter; iter++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 1. Persist the assistant tool_use turn so reloading the
		//    session shows what the model asked for. The textual
		//    serialization is intentionally simple (JSON of the tool
		//    calls) — once corellm.ContentPart grows native tool_use
		//    parts the storage layer will round-trip them losslessly.
		if err := l.persistAssistantToolUse(ctx, sessionID, current); err != nil {
			return err
		}
		augmented = append(augmented, assistantMessageFromResponse(current))

		// 2. Dispatch every tool. WP04 fans the calls out behind a
		//    semaphore (FR-004); the dispatcher returns ctx.Err() when
		//    the host context cancelled mid-flight, but still surfaces a
		//    full results slice (any unstarted call lands as a synthetic
		//    tool_cancelled). We thread those cancellation results into
		//    history before returning so the audit/post-hook trail stays
		//    consistent with what the conversation actually saw.
		results, dispatchErr := l.dispatchTools(ctx, sessionID, parentSubID, current.ToolCalls)
		if errors.Is(dispatchErr, context.Canceled) || errors.Is(dispatchErr, context.DeadlineExceeded) {
			// Persist what we have using a background context — the
			// host ctx is done but the storage write should still land.
			persistCtx := context.Background()
			for _, r := range results {
				if perr := l.persistToolResult(persistCtx, sessionID, r); perr != nil {
					log.Warn("toolloop.persist_cancelled_result_failed",
						"sub_id", parentSubID,
						"session_id", sessionID,
						"err", perr.Error(),
					)
				}
			}
			log.Info("toolloop.run.cancelled",
				"sub_id", parentSubID,
				"session_id", sessionID,
				"iter", iter,
				"results", len(results),
			)
			return dispatchErr
		}
		if dispatchErr != nil {
			return dispatchErr
		}

		// 3. Persist + thread each tool result.
		for _, r := range results {
			if err := l.persistToolResult(ctx, sessionID, r); err != nil {
				return err
			}
			augmented = append(augmented, toolResultMessage(r))
		}

		// 4. Re-invoke the registry with the augmented history.
		nextReq := request
		nextReq.Messages = augmented
		next, err := l.invokeAndDrain(ctx, nextReq)
		if err != nil {
			return err
		}

		log.Info("toolloop.iteration.complete",
			"sub_id", parentSubID,
			"session_id", sessionID,
			"iter", iter,
			"finish_reason", next.FinishReason,
			"new_tool_calls", len(next.ToolCalls),
		)

		if next.FinishReason != "tool_use" || len(next.ToolCalls) == 0 {
			// Persist the final assistant turn and exit.
			return l.persistAssistantFinal(ctx, sessionID, next)
		}
		current = &next
	}

	// MaxIter reached without convergence. WP01 surfaces this as an
	// error so the pump's existing error path renders it; WP04 will
	// replace this with a synthetic assistant message per FR-006.
	return fmt.Errorf("toolloop: exceeded max iterations (%d) for session %q", l.maxIter, sessionID)
}

// invokeAndDrain opens a registry stream, drains its events into a
// terminal Response, and returns it. Errors from Final propagate.
func (l *Loop) invokeAndDrain(ctx context.Context, req corellm.GenerationRequest) (corellm.Response, error) {
	stream, err := l.reg.Stream(ctx, req)
	if err != nil {
		return corellm.Response{}, fmt.Errorf("toolloop: registry stream: %w", err)
	}
	for range stream.Events() {
		// WP01 does not surface intermediate deltas — the pump owns
		// streaming for the initial turn, and re-invocations are
		// drained synchronously here. WP04 replaces this with a
		// chunk-forwarding pump so the user sees deltas mid-loop.
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		return corellm.Response{}, fmt.Errorf("toolloop: stream final: %w", ferr)
	}
	return resp, nil
}

// resolveServer scans the pool's tool list for a name match. The MCP
// catalog is small (single-digit servers, double-digit tools) so a
// linear scan is fine; if that ever changes we'll cache. Returns
// false when no match is found so the caller can synthesize a
// blocked result.
func resolveServer(tools []Tool, name string) (string, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t.Server, true
		}
	}
	return "", false
}

// assistantMessageFromResponse builds the corellm.Message that
// represents the model's tool_use turn for re-submission. We use a
// JSON serialization of the tool calls as the text content so adapters
// that expect a specific tool_use block shape can parse it; native
// tool_use ContentPart support lands in a later WP.
func assistantMessageFromResponse(resp *corellm.Response) corellm.Message {
	parts := make([]corellm.ContentPart, 0, len(resp.Content)+len(resp.ToolCalls))
	parts = append(parts, resp.Content...)
	for i := range resp.ToolCalls {
		tu := resp.ToolCalls[i]
		parts = append(parts, corellm.ContentPart{Type: "tool_use", ToolUse: &tu})
	}
	return corellm.Message{Role: corellm.RoleAssistant, Content: parts}
}

// toolResultMessage builds the corellm.Message that carries one tool
// result back to the model. The tool_data field carries a structured
// payload so adapter-specific translation (Anthropic tool_result block
// vs OpenAI tool message) can read both the tool_use_id and the body.
//
// Cancelled results carry an extra "cancelled":true flag so adapters
// that want to render "the user stopped the loop" specially can do so;
// providers that don't care still see the standard is_error path.
func toolResultMessage(r toolResult) corellm.Message {
	envelope := map[string]any{
		"tool_use_id": r.ToolUseID,
		"is_error":    r.IsError,
		"output":      r.Output,
	}
	if r.Cancelled {
		envelope["cancelled"] = true
	}
	payload, _ := json.Marshal(envelope)
	return corellm.Message{
		Role: corellm.RoleTool,
		Content: []corellm.ContentPart{{
			Type:     "tool_result",
			Text:     r.Output,
			ToolData: payload,
		}},
	}
}

// persistAssistantToolUse stores the assistant's tool_use turn in the
// session manager so reloading shows the call the model made. We
// serialize as JSON because session.Message.Content is a string column
// and the structured tool_use shape needs to round-trip somehow.
func (l *Loop) persistAssistantToolUse(ctx context.Context, sessionID string, resp *corellm.Response) error {
	if l.history == nil || sessionID == "" {
		return nil
	}
	textContent := concatText(resp.Content)
	calls := make([]map[string]any, 0, len(resp.ToolCalls))
	for _, tu := range resp.ToolCalls {
		calls = append(calls, map[string]any{
			"id":    tu.ID,
			"name":  tu.Name,
			"input": json.RawMessage(tu.Input),
		})
	}
	envelope, _ := json.Marshal(map[string]any{
		"text":       textContent,
		"tool_calls": calls,
	})
	return l.history.AppendMessage(ctx, sessionID, string(corellm.RoleAssistant), string(envelope))
}

// persistToolResult stores one tool's output as a tool-role message in
// the session.
func (l *Loop) persistToolResult(ctx context.Context, sessionID string, r toolResult) error {
	if l.history == nil || sessionID == "" {
		return nil
	}
	envelope, _ := json.Marshal(map[string]any{
		"tool_use_id": r.ToolUseID,
		"server":      r.Server,
		"tool":        r.Tool,
		"is_error":    r.IsError,
		"output":      r.Output,
	})
	return l.history.AppendMessage(ctx, sessionID, string(corellm.RoleTool), string(envelope))
}

// persistAssistantFinal stores the model's terminal (non-tool_use)
// turn. The pump's existing path handles persisting the *initial*
// assistant turn from a single-turn conversation; the loop owns
// everything that happens after the first tool_use.
func (l *Loop) persistAssistantFinal(ctx context.Context, sessionID string, resp corellm.Response) error {
	if l.history == nil || sessionID == "" {
		return nil
	}
	text := concatText(resp.Content)
	if text == "" {
		// Nothing to persist — the model returned only tool calls in
		// the final response (which is degenerate but possible).
		return nil
	}
	return l.history.AppendMessage(ctx, sessionID, string(corellm.RoleAssistant), text)
}

func concatText(parts []corellm.ContentPart) string {
	var out string
	for _, p := range parts {
		if p.Type == "text" || p.Text != "" {
			out += p.Text
		}
	}
	return out
}
