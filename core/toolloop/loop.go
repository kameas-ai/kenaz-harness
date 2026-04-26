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
	// MaxIter caps the number of tool-use rounds per Run invocation.
	// Zero falls back to DefaultMaxIter.
	MaxIter int
}

// Loop is the orchestrator. Construct via New and invoke Run from the
// rpc pump after the initial stream closes with finish_reason=tool_use.
type Loop struct {
	reg     corellm.Registry
	pool    MCPPool
	history SessionHistoryRW
	perms   PermissionResolver
	maxIter int
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
	return &Loop{
		reg:     cfg.Registry,
		pool:    cfg.Pool,
		history: cfg.History,
		perms:   perms,
		maxIter: maxIter,
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

		// 2. Dispatch every tool sequentially (concurrency is WP04).
		results, err := l.dispatchTools(ctx, sessionID, parentSubID, current.ToolCalls)
		if err != nil {
			return err
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

// dispatchTools resolves each ToolUse against the pool, consults the
// permission gate, and invokes the call when allowed. Sequential —
// concurrency lands in WP04. A pool failure surfaces as a toolResult
// with IsError=true so the model can recover; the function only
// returns an error for unrecoverable problems (pool tools listing,
// resolver internal errors).
func (l *Loop) dispatchTools(ctx context.Context, sessionID, parentSubID string, calls []corellm.ToolUse) ([]toolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	tools, err := l.pool.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("toolloop: pool tools: %w", err)
	}
	log := logging.L()
	out := make([]toolResult, 0, len(calls))
	for _, call := range calls {
		server, ok := resolveServer(tools, call.Name)
		if !ok {
			out = append(out, toolResult{
				ToolUseID: call.ID,
				Tool:      call.Name,
				Output:    fmt.Sprintf("tool %q not registered", call.Name),
				IsError:   true,
			})
			continue
		}
		// Permission gate. WP02 surfaces three policies:
		//   deny         → synthetic blocked result, skip dispatch
		//   confirm_each → treat as auto_allow until WP05
		//   auto_allow   → existing path
		// Resolver internal errors surface as a tool failure (so the
		// model isn't trapped) but are also logged because they're
		// almost always a bug in the wired resolver, not user policy.
		res, permErr := l.perms.Resolve(ctx, sessionID, server, call.Name)
		if permErr != nil {
			log.Warn("toolloop.permission.resolver_failed",
				"sub_id", parentSubID,
				"session_id", sessionID,
				"server", server,
				"tool", call.Name,
				"err", permErr.Error(),
			)
			out = append(out, toolResult{
				ToolUseID: call.ID,
				Server:    server,
				Tool:      call.Name,
				Output:    fmt.Sprintf("permission resolver error: %s", permErr.Error()),
				IsError:   true,
			})
			continue
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
			out = append(out, toolResult{
				ToolUseID: call.ID,
				Server:    server,
				Tool:      call.Name,
				Output:    "Tool blocked: " + reason,
				IsError:   true,
			})
			continue
		case PolicyConfirmEach:
			// TODO(WP05): plumb confirm-each modal — pause the loop,
			// emit tool_confirmation_required, await the user decision
			// via the ConfirmationBus, and either continue or convert
			// to a synthetic deny on a "deny" or timeout outcome.
			fallthrough
		case PolicyAutoAllow:
			// Existing dispatch path.
		default:
			// Defensive: a resolver implementer that returns an unknown
			// policy is treated as a deny — a misconfigured policy
			// should never silently allow a call.
			out = append(out, toolResult{
				ToolUseID: call.ID,
				Server:    server,
				Tool:      call.Name,
				Output:    fmt.Sprintf("Tool blocked: unknown policy %q", res.Policy),
				IsError:   true,
			})
			continue
		}
		raw, callErr := l.pool.Call(ctx, server, call.Name, call.Input)
		if callErr != nil {
			out = append(out, toolResult{
				ToolUseID: call.ID,
				Server:    server,
				Tool:      call.Name,
				Output:    callErr.Error(),
				IsError:   true,
			})
			continue
		}
		out = append(out, toolResult{
			ToolUseID: call.ID,
			Server:    server,
			Tool:      call.Name,
			Output:    string(raw),
		})
	}
	return out, nil
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
func toolResultMessage(r toolResult) corellm.Message {
	payload, _ := json.Marshal(map[string]any{
		"tool_use_id": r.ToolUseID,
		"is_error":    r.IsError,
		"output":      r.Output,
	})
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
