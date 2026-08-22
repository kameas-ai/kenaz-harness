package rpc

// wf_adapters.go — thin adapters that bridge the production MCP pool and
// LLM registry onto the narrow interfaces the workflow runner expects.
//
// Keeping the adapters in a separate file avoids polluting api.go with
// import-heavy struct bodies while satisfying DIRECTIVE_001: no core/
// package imports anything from the rpc layer.
//
// Adapter shapes:
//
//	wfMCPCallerAdapter       — coremcp.Pool (json.RawMessage args) →
//	                           corewf.MCPCaller (map[string]any args).
//	                           Wraps "unknown server" errors with an
//	                           actionable user message (FR-004).
//
//	wfLLMStreamerAdapter     — corellm.Registry (GenerationRequest/Stream) →
//	                           corewf.LLMStreamer (LLMRequest/LLMStream).
//	                           Translates text-delta events and captures
//	                           tool-use blocks for the bounded loop (FR-003).
//
//	wfToolDiscovererAdapter  — corellm.ToolDiscoverer →
//	                           corewf.ToolDiscoverer.
//	                           Reuses the SAME discoverer chat uses so
//	                           one catalog + one permission filter serve
//	                           both surfaces (FR-002).
//
//	wfToolDispatcherAdapter  — toolloop.MCPPool.Call →
//	                           corewf.ToolDispatcher.
//	                           Dispatches through the same BuiltinPool
//	                           chat's tool loop uses (one Cedar path).
//
//	wfNotifierAdapter        — satisfies corewf.Notifier via the Wails
//	                           runtime notification call.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// ─── MCP adapter ──────────────────────────────────────────────────────────────

// wfMCPCallerAdapter bridges coremcp.Pool onto corewf.MCPCaller.
//
// The production pool's Call signature expects json.RawMessage; the
// workflow runner hands us map[string]any. We marshal the map before
// dispatching and unmarshal the raw JSON result into a plain string.
//
// Actionable errors (FR-004 / WP03):
//   - When the server name is not found in the pool, the caller sees
//     "MCP server <name> is not installed — install it from Tools"
//     rather than the internal "stdio: unknown server" message.
//   - All other errors propagate unchanged.
type wfMCPCallerAdapter struct {
	pool coremcp.Pool
}

func (a *wfMCPCallerAdapter) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	if a.pool == nil {
		return "", fmt.Errorf("MCP server %q is not available — MCP is disabled. Enable it in Settings → Tools", server)
	}
	// Marshal args to json.RawMessage (nil args → null).
	var rawArgs json.RawMessage
	if len(args) > 0 {
		var merr error
		rawArgs, merr = json.Marshal(args)
		if merr != nil {
			return "", fmt.Errorf("mcp_call: marshal args for %s.%s: %w", server, tool, merr)
		}
	}
	raw, err := a.pool.Call(ctx, server, tool, rawArgs)
	if err != nil {
		return "", translateMCPError(server, err)
	}
	// Unwrap the json.RawMessage to a plain string the step output field holds.
	// If the result is a JSON string, unquote it; otherwise return the raw JSON.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	return string(raw), nil
}

// translateMCPError converts low-level pool errors into user-facing messages.
func translateMCPError(server string, err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "unknown server") ||
		strings.Contains(low, "not in pool") ||
		strings.Contains(low, "server not found") {
		return fmt.Errorf("MCP server %q is not installed or not authorized — install it from Tools", server)
	}
	return err
}

// ─── LLM adapter ──────────────────────────────────────────────────────────────

// wfLLMStreamerAdapter bridges corellm.Registry onto corewf.LLMStreamer.
//
// When req.Tools is non-empty the adapter projects the tool specs into
// the GenerationRequest and returns a wfLLMStreamBridge that implements
// the optional corewf.ToolCallStream interface so the bounded loop can
// retrieve tool-use calls after the stream drains (FR-002/FR-003).
//
// When req.History is non-empty (multi-turn tool loop), the adapter
// reconstructs the full conversation message sequence.
//
// Prompt building: the workflow runner supplies a single user_prompt
// string; on the first turn it is wrapped in a user-turn message.
// Subsequent turns (req.Prompt == "") carry only history.
type wfLLMStreamerAdapter struct {
	reg corellm.Registry
}

func (a *wfLLMStreamerAdapter) Stream(ctx context.Context, req corewf.LLMRequest) (corewf.LLMStream, error) {
	if a.reg == nil {
		return nil, fmt.Errorf("workflows: LLM registry not wired")
	}

	// Build the messages slice.
	messages, err := buildMessages(req)
	if err != nil {
		return nil, fmt.Errorf("workflows: build messages: %w", err)
	}

	// Project corewf.ToolSpec → corellm.ToolSpec when tools are enabled.
	var llmTools []corellm.ToolSpec
	for _, t := range req.Tools {
		llmTools = append(llmTools, corellm.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.InputSchema),
		})
	}

	inner, err := a.reg.Stream(ctx, corellm.GenerationRequest{
		ProfileID: req.ProfileID,
		Model:     req.Model,
		Messages:  messages,
		Tools:     llmTools,
	})
	if err != nil {
		return nil, err
	}
	return &wfLLMStreamBridge{inner: inner}, nil
}

// buildMessages constructs the corellm.Message slice from req.Prompt
// and req.History. History carries the prior turns of the tool loop.
func buildMessages(req corewf.LLMRequest) ([]corellm.Message, error) {
	var messages []corellm.Message

	// First turn: user prompt only.
	if len(req.History) == 0 {
		if req.Prompt != "" {
			messages = append(messages, corellm.Message{
				Role:    "user",
				Content: []corellm.ContentBlock{{Type: "text", Text: req.Prompt}},
			})
		}
		return messages, nil
	}

	// Multi-turn: seed with the original user prompt from history iteration 0.
	// The very first HistoryMessage from the model may not include the prompt —
	// we prepend it as the opening user turn.
	if req.Prompt != "" {
		messages = append(messages, corellm.Message{
			Role:    "user",
			Content: []corellm.ContentBlock{{Type: "text", Text: req.Prompt}},
		})
	} else if len(req.History) > 0 {
		// Reconstruct messages from history only (subsequent loop iterations).
		// History has alternating user-seed / assistant / tool-result blocks.
		// The caller sets Prompt="" on the second+ iteration and sends history.
		//
		// Reconstruct the opening user turn by scanning history for the first
		// user-role block (the original prompt is already in history[0] via the
		// first assistant turn's context — but we store user prompt separately).
		// On second+ iterations the history already starts with the assistant's
		// prior turn; skip adding an extra user message.
	}

	for _, h := range req.History {
		switch h.Role {
		case "user":
			messages = append(messages, corellm.Message{
				Role:    "user",
				Content: []corellm.ContentBlock{{Type: "text", Text: h.Text}},
			})
		case "assistant":
			var blocks []corellm.ContentBlock
			if h.Text != "" {
				blocks = append(blocks, corellm.ContentBlock{Type: "text", Text: h.Text})
			}
			for _, tu := range h.ToolUses {
				blocks = append(blocks, corellm.ContentBlock{
					Type: "tool_use",
					ToolUse: &corellm.ToolUse{
						ID:    tu.ID,
						Name:  tu.Name,
						Input: json.RawMessage(tu.Input),
					},
				})
			}
			messages = append(messages, corellm.Message{Role: "assistant", Content: blocks})
		case "tool":
			// Tool results: one block per result, packed into a user-role message
			// (Anthropic's convention: tool results arrive as a user turn).
			var blocks []corellm.ContentBlock
			for _, tr := range h.ToolResults {
				blocks = append(blocks, corellm.ContentBlock{
					Type: "tool_result",
					ToolResult: &corellm.ToolResult{
						ToolUseID: tr.ToolUseID,
						Content:   json.RawMessage(`"` + escapeJSONString(tr.Content) + `"`),
						IsError:   tr.IsError,
					},
				})
			}
			messages = append(messages, corellm.Message{Role: "user", Content: blocks})
		}
	}

	return messages, nil
}

// escapeJSONString escapes a plain string for embedding in a JSON string
// literal. Only handles the common cases (backslash, quote, newline, tab).
func escapeJSONString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// wfLLMStreamBridge narrows corellm.Stream to corewf.LLMStream and
// optionally corewf.ToolCallStream.
//
// It spawns a goroutine that reads corellm.StreamEvent values:
//   - Text events are forwarded to the outbound channel.
//   - Tool events are captured in toolCalls for retrieval via ToolCalls().
//   - All other events are silently dropped.
//
// The bridge implements corewf.ToolCallStream so the bounded tool loop
// can retrieve tool invocations after the Events channel closes (FR-003).
type wfLLMStreamBridge struct {
	inner     corellm.Stream
	once      sync.Once
	ch        chan corewf.LLMStreamEvent
	mu        sync.Mutex
	toolCalls []corewf.ToolUseCall
}

func (b *wfLLMStreamBridge) Events() <-chan corewf.LLMStreamEvent {
	b.once.Do(func() {
		b.ch = make(chan corewf.LLMStreamEvent, 64)
		go func() {
			defer close(b.ch)
			for ev := range b.inner.Events() {
				switch {
				case ev.Err != "":
					b.ch <- corewf.LLMStreamEvent{Err: ev.Err}
					return
				case ev.Kind == corellm.StreamTool && ev.Tool != nil:
					// Capture tool-use call; don't forward to text channel.
					b.mu.Lock()
					b.toolCalls = append(b.toolCalls, corewf.ToolUseCall{
						ID:    ev.Tool.ID,
						Name:  ev.Tool.Name,
						Input: []byte(ev.Tool.Input),
					})
					b.mu.Unlock()
				case ev.Text != "":
					b.ch <- corewf.LLMStreamEvent{Text: ev.Text}
				}
			}
		}()
	})
	return b.ch
}

func (b *wfLLMStreamBridge) Final() (string, error) {
	resp, err := b.inner.Final()
	if err != nil {
		return "", err
	}
	// Capture any tool_use blocks from the final response that weren't
	// surfaced as streaming events (some adapters batch them in Final).
	b.mu.Lock()
	for _, block := range resp.Content {
		if block.Type == "tool_use" && block.ToolUse != nil {
			// Deduplicate by ID to avoid double-counting streaming + final.
			alreadyCaptured := false
			for _, existing := range b.toolCalls {
				if existing.ID == block.ToolUse.ID {
					alreadyCaptured = true
					break
				}
			}
			if !alreadyCaptured {
				b.toolCalls = append(b.toolCalls, corewf.ToolUseCall{
					ID:    block.ToolUse.ID,
					Name:  block.ToolUse.Name,
					Input: []byte(block.ToolUse.Input),
				})
			}
		}
	}
	b.mu.Unlock()

	// Aggregate text content blocks from the response.
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

// ToolCalls implements corewf.ToolCallStream. Must be called after
// the Events channel closes (i.e. after Final() has been called).
func (b *wfLLMStreamBridge) ToolCalls() []corewf.ToolUseCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.toolCalls) == 0 {
		return nil
	}
	out := make([]corewf.ToolUseCall, len(b.toolCalls))
	copy(out, b.toolCalls)
	return out
}

// Verify wfLLMStreamBridge satisfies both interfaces.
var _ corewf.LLMStream = (*wfLLMStreamBridge)(nil)
var _ corewf.ToolCallStream = (*wfLLMStreamBridge)(nil)

// ─── Tool discoverer adapter ───────────────────────────────────────────────────

// wfToolDiscovererAdapter bridges corellm.ToolDiscoverer onto
// corewf.ToolDiscoverer. It reuses the SAME discoverer the chat surface
// uses so model_turn steps share one catalog and one permission filter
// with chat — no new egress bypass (FR-002).
//
// The sessionID forwarded to the underlying discoverer is empty because
// workflow engine steps have no chat session context; the permission
// resolver falls back to the global policy in that case.
type wfToolDiscovererAdapter struct {
	inner corellm.ToolDiscoverer
}

func (a *wfToolDiscovererAdapter) Discover(ctx context.Context) ([]corewf.ToolSpec, error) {
	if a.inner == nil {
		return nil, nil
	}
	// Pass empty sessionID — workflows have no chat session.
	specs, err := a.inner.Tools(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]corewf.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, corewf.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: []byte(s.InputSchema),
		})
	}
	return out, nil
}

// ─── Tool dispatcher adapter ───────────────────────────────────────────────────

// wfToolDispatcherAdapter bridges toolloop.MCPPool onto
// corewf.ToolDispatcher. It dispatches through the same BuiltinPool
// the chat tool loop uses so the same Cedar/permission path applies —
// no new egress bypass (FR-003).
type wfToolDispatcherAdapter struct {
	pool toolloop.MCPPool
}

func (a *wfToolDispatcherAdapter) Dispatch(ctx context.Context, name string, input []byte) (string, bool, error) {
	if a.pool == nil {
		return "", true, fmt.Errorf("tool dispatcher not wired (no MCP pool)")
	}
	// Split the namespaced "server__tool" name back into (server, tool).
	server, tool := splitToolName(name)
	raw, err := a.pool.Call(ctx, server, tool, json.RawMessage(input))
	if err != nil {
		return "", true, err
	}
	// Unwrap JSON string result to plain text.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false, nil
	}
	return string(raw), false, nil
}

// splitToolName splits "server__tool" into ("server", "tool").
// Falls back to ("", name) when no separator is found.
func splitToolName(name string) (server, tool string) {
	const sep = "__"
	idx := strings.Index(name, sep)
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+len(sep):]
}

// ─── Slash-command tool dispatcher adapter ─────────────────────────────────────

// slashToolDispatcherAdapter bridges slashcmd.ToolDispatcher onto the SAME
// pool + permission resolver + confirm-each apparatus a chat tool call
// uses (automation-actually-runs-01PMZ404 UNIT-4).
//
// Owner ruling G-2 (docs/escalation-register-2026-08-19.md Part 9,
// E-004): "namespaced tool names, JSON arguments, and the SAME
// confirm/Cedar path a chat tool call takes." The reason given: one
// permission story for the whole app — a slash command must not become
// a privilege-escalation route that reaches tools without the
// confirmation a chat tool call would trigger.
//
// This is deliberately NOT wfToolDispatcherAdapter's shape (a bare
// pool.Call with no permission gate at all). That adapter serves the
// workflow engine's tool_call step, whose per-run containment is a
// DIFFERENT mission's scope (harness-self-attach-01PMHS01 WP04; see
// spec.md §4 non-goals for automation-actually-runs-01PMZ404) — copying
// it here would leave a slash command MORE permissive than the chat
// surface it is supposed to mirror, which is exactly the hole the
// ruling closes. A slash-invoked tool call is user- or model-initiated
// the same way a chat tool call is, so it gets the same ladder:
// PermissionResolver.Resolve (the SAME merged static+Cedar resolver
// chat uses — see core/rpc/api.go's newLLMStack) short-circuits an
// explicit Deny before anything dispatches, and a confirm_each verdict
// parks on the SAME *toolloop.ConfirmBus chat parks on — never a
// silent auto-allow, and never a locally-reinvented decision that could
// quietly diverge from chat's.
//
// The confirm-each ladder here intentionally omits kernelToolAdapter's
// rung 1 (the autonomy prompt-skip set): that knob is scoped to a
// chat TURN's resolved autonomy posture, which has no meaning for a
// directly-dispatched slash command. Skipping it can only make this
// path prompt in a case chat would have silently skipped — a strictly
// more conservative divergence, never a more permissive one.
//
// gate is the SAME process-singleton Cedar engine (a.cedarGate() in
// core/rpc/api.go) the chat tool path's PolicyGateAdapter.CheckTool
// consults (core/rpc/views/agentgraph/env_deps_policy.go). An
// independent review of PR #307 found that perms above only ever
// evaluates the coarse Action::"use_tool" family (via the merged
// resolver's Cedar-backed session-kind arm) — the finer-grained
// Action::"tool_exec" sibling, which default_policy.cedar:38-46
// explicitly invites a user to forbid per-tool ("Per-tool deny rules
// can be added by the user to lock down individual tools"), was never
// consulted on this path. DispatchTool's cedar.CheckTool call below
// closes that gap; see the comment there for why it is safe to add
// without also duplicating the use_tool leg.
type slashToolDispatcherAdapter struct {
	pool  toolloop.MCPPool
	perms toolloop.PermissionResolver
	gate  cedar.Gate

	// confirm-each collaborators — the SAME instances core/rpc/api.go
	// hands the chat runner's kernelToolAdapter (via chat.ConfirmDeps).
	confirm          *toolloop.ConfirmBus
	confirmEnabled   func() bool
	sessionGrants    *toolloop.SessionGrantCache
	persistGrants    toolloop.PersistentGrantStore
	headless         toolloop.HeadlessConfirmPolicy
	headlessExplicit bool
	auditEmitter     contextaudit.Emitter
	now              func() time.Time
}

// Compile-time witness: slashToolDispatcherAdapter satisfies the
// interface slashcmd.Dispatch.Run dispatches through.
var _ coreslashcmd.ToolDispatcher = (*slashToolDispatcherAdapter)(nil)

func (a *slashToolDispatcherAdapter) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// DispatchTool implements slashcmd.ToolDispatcher. toolName is the
// namespaced "server__tool" form — owner ruling G-2's "namespaced tool
// names" — the same convention wfToolDispatcherAdapter, the workflow
// tool_call step, and the chat kernel tool adapter all use.
// core/slashcmd.Validate now rejects a bare (non-namespaced) tool name
// at save time, so a malformed name reaching here is a defence-in-depth
// check, not the primary gate.
func (a *slashToolDispatcherAdapter) DispatchTool(ctx context.Context, toolName string, args []string) (string, error) {
	if a == nil || a.pool == nil {
		return "", fmt.Errorf("slash tool dispatch: no tool pool wired")
	}
	server, tool := splitToolName(toolName)
	if server == "" {
		return "", fmt.Errorf("slash tool dispatch: %q is not a namespaced tool name (want \"server__tool\")", toolName)
	}

	sessionID := toolloop.SessionIDFromContext(ctx)

	if a.perms != nil {
		v, err := a.perms.Resolve(ctx, sessionID, server, tool)
		if err != nil {
			return "", fmt.Errorf("slash tool dispatch: permission resolve: %w", err)
		}
		switch v.Policy {
		case toolloop.PolicyAutoAllow:
			// Fall through to dispatch.

		case toolloop.PolicyDeny:
			// The floor. Evaluated before any prompt, exactly like
			// kernelToolAdapter.dispatch — a denied call is never
			// surfaced as an approvable row.
			reason := v.Reason
			if reason == "" {
				reason = "denied by permission policy"
			}
			return "", fmt.Errorf("tool %q denied: %s", toolName, reason)

		case toolloop.PolicyConfirmEach:
			approved, denyReason, err := a.resolveConfirmEach(ctx, sessionID, server, tool, v.Reason)
			if err != nil {
				return "", fmt.Errorf("slash tool dispatch: tool confirmation: %w", err)
			}
			if !approved {
				return "", fmt.Errorf("tool %q denied: %s", toolName, denyReason)
			}

		default:
			// An unrecognised or empty policy string is a configuration
			// error and must not read as "allow" — the same rule
			// kernelToolAdapter.dispatch enforces for chat.
			return "", fmt.Errorf("tool %q denied: unrecognised permission policy %q", toolName, v.Policy)
		}
	}

	// The chat tool path's PolicyGateAdapter.CheckTool
	// (core/rpc/views/agentgraph/env_deps_policy.go) evaluates BOTH
	// Action::"use_tool" and the finer-grained Action::"tool_exec"
	// sibling from the SAME dispatch boundary
	// (core/agentgraph/exec_dispatch.go), in that order, propagating
	// the first Deny unwrapped. The perms ladder just above is this
	// path's use_tool-equivalent leg (its Cedar-backed session-kind
	// arm evaluates Action::"use_tool" with the real session context —
	// see cedarSessionKindResolver.Resolve); this call is the missing
	// second leg, evaluated immediately after in the same order.
	// Mirroring PolicyGateAdapter.CheckTool's failure semantics rather
	// than inventing a new one: cedar.CheckTool returns nil for a nil
	// gate (fail-open, matching every unwired test double that
	// constructs this adapter without one) and *cedar.PolicyDeniedError
	// on Deny, which %w preserves so cedar.IsPolicyDenied still
	// discriminates it from any other error this func returns.
	if err := cedar.CheckTool(ctx, a.gate, server, tool); err != nil {
		return "", fmt.Errorf("tool %q denied: %w", toolName, err)
	}

	// Owner ruling G-2's "JSON arguments": the []string tokens the slash
	// command's rendered args template produced are marshalled as a JSON
	// array — this is the []string -> json.RawMessage boundary
	// conversion the ruling calls out as "the part most likely to be got
	// wrong quietly", so it is driven from here (DispatchTool, the
	// model's actual entry point via kenaz__skill -> Dispatch.
	// RunModelInvoked -> Dispatch.Run), not from a helper nothing calls.
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("slash tool dispatch: marshal args: %w", err)
	}

	raw, callErr := a.pool.Call(toolloop.WithSessionID(ctx, sessionID), server, tool, argsJSON)
	if callErr != nil {
		return "", callErr
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	return string(raw), nil
}

// resolveConfirmEach decides a `confirm_each` verdict for a slash-invoked
// tool call. It mirrors kernelToolAdapter.resolveConfirmEach
// (core/rpc/views/agentgraph/chat/kernel_tool_adapter.go) minus the
// autonomy prompt-skip rung (see the type doc), consulting the SAME
// collaborators — the SessionGrantCache, PersistentGrantStore,
// ConfirmBus, and headless policy chat's confirm-each ladder uses — so a
// tool that would prompt in chat also prompts here, and an "always
// allow" grant recorded from either surface is honoured by both.
//
// Returns (approved, denyReason, err). err is non-nil only when the
// confirmation itself could not be resolved (context cancelled while
// parked) — the caller must treat that as "did not dispatch", same as a
// deny.
func (a *slashToolDispatcherAdapter) resolveConfirmEach(
	ctx context.Context,
	sessionID, server, tool, reason string,
) (approved bool, denyReason string, err error) {
	family := toolloop.ClassifyToolFamily(server, tool)

	// Settings.ConfirmEachEnabled() off => the prompt is never offered,
	// same as chat.
	if a.confirmEnabled != nil && !a.confirmEnabled() {
		a.auditConfirm(ctx, contextaudit.ToolConfirmDecisionPayload{
			SessionID: sessionID, Server: server, Tool: tool, Family: family,
			Path: contextaudit.ToolConfirmPathToggleOff, Approved: true,
			Reason: "confirm-each disabled in Settings",
		})
		return true, "", nil
	}

	// Session grant — "allow for this session".
	if a.sessionGrants.Has(sessionID, server, tool) {
		a.auditConfirm(ctx, contextaudit.ToolConfirmDecisionPayload{
			SessionID: sessionID, Server: server, Tool: tool, Family: family,
			Path: contextaudit.ToolConfirmPathSessionGrant, Approved: true,
			Reason: "session grant",
		})
		return true, "", nil
	}

	// Persisted grant — "always allow", consulted live so a revoke from
	// Settings takes effect immediately.
	if a.persistGrants != nil && a.persistGrants.HasGrant(server, tool) {
		a.auditConfirm(ctx, contextaudit.ToolConfirmDecisionPayload{
			SessionID: sessionID, Server: server, Tool: tool, Family: family,
			Path: contextaudit.ToolConfirmPathPersistedGrant, Approved: true,
			Reason: "persisted allow rule",
		})
		return true, "", nil
	}

	// No prompt channel, or the deployment declared itself headless: the
	// SAME default-deny headless policy chat's ladder falls to.
	if a.confirm == nil || !a.confirm.HasChannel() || a.headlessExplicit {
		policy := a.headless
		if policy != toolloop.HeadlessAllow {
			policy = toolloop.HeadlessDeny
		}
		ok := policy == toolloop.HeadlessAllow
		policyReason := "no confirmation channel is attached; headless policy: " + string(policy)
		if a.headlessExplicit {
			policyReason = "deployment declared headless by operator; headless policy: " + string(policy)
		}
		a.auditConfirm(ctx, contextaudit.ToolConfirmDecisionPayload{
			SessionID: sessionID, Server: server, Tool: tool, Family: family,
			Path: contextaudit.ToolConfirmPathHeadlessPolicy, Approved: ok,
			Reason: policyReason,
		})
		if !ok {
			return false, policyReason, nil
		}
		return true, "", nil
	}

	// Prompt: park on the SAME bus a chat tool call parks on.
	req := toolloop.ConfirmRequest{
		SessionID: sessionID,
		CallID:    toolloop.NewConfirmID("confirm"),
		BatchID:   toolloop.ConfirmBatchFromContext(ctx),
		Server:    server,
		Tool:      tool,
		Reason:    reason,
	}
	if req.BatchID == "" {
		req.BatchID = toolloop.NewConfirmID("batch")
	}

	decision, pendErr := a.confirm.Pending(ctx, req)
	if pendErr != nil {
		// Context cancellation or a caller bug — the call must not
		// dispatch, and (matching kernelToolAdapter) nothing was
		// decided, so nothing is audited.
		return false, "", pendErr
	}

	if decision.Approved {
		if decision.RememberSession {
			a.sessionGrants.Grant(sessionID, server, tool)
		}
		if decision.Persist && a.persistGrants != nil {
			_ = a.persistGrants.WriteGrant(server, tool)
		}
	}

	payloadReason := decision.Reason
	if !decision.Approved && payloadReason == "" {
		payloadReason = "not approved by user"
	}
	a.auditConfirm(ctx, contextaudit.ToolConfirmDecisionPayload{
		SessionID: sessionID, CallID: req.CallID, BatchID: req.BatchID,
		Server: server, Tool: tool, Family: family,
		Path: contextaudit.ToolConfirmPathPrompted, Approved: decision.Approved,
		RememberSession: decision.Approved && decision.RememberSession,
		Reason:          payloadReason,
	})
	return decision.Approved, payloadReason, nil
}

// auditConfirm emits one KindToolConfirmDecision record. Never blocks
// the decision: a nil emitter is silence.
func (a *slashToolDispatcherAdapter) auditConfirm(ctx context.Context, p contextaudit.ToolConfirmDecisionPayload) {
	if a.auditEmitter == nil {
		return
	}
	contextaudit.MustEmit(ctx, a.auditEmitter, contextaudit.KindToolConfirmDecision, p, a.clock())
}
