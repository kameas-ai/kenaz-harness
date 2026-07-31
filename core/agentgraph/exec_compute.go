package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// This file holds the compute-primitive executors (FR-029 .. FR-039):
// Model, Tool, Transform, Activity, Reflect, Review, Planner, Ask,
// Escalate, Compact.
//
// All executors follow the same shape: pull the typed *Attrs from the
// node, perform the side effect via the Env seam, emit an EventBatch
// with kind-specific events, return outputs on declared ports.

// ---- ModelNode (was LLMNode) ----

// modelExecutor implements ExecModel (FR-030). The on-the-wire kind is
// `model`; legacy graphs naming `llm` are alias-resolved at load time.
type modelExecutor struct{}

func (modelExecutor) Kind() NodeKind { return NodeKindModel }

func (modelExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ModelAttrs)
	if !ok {
		return res, fmt.Errorf("model: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// Build the message slice from upstream inputs. The default port
	// for an LLMNode is "messages"; we accept either a []Message slice
	// or a []llm.Message payload. Anything else falls back to a
	// blank history.
	// Compose the graph-level base system prompt (if any) with this
	// node's own role prompt. When the graph sets no base, composePrompt
	// yields just the node role — no behaviour change for graphs that
	// don't ground their model calls.
	systemPrompt := composePrompt(graphBaseOf(env), a.SystemPrompt)

	var msgs []Message
	if v, ok := inputs.Get("messages"); ok {
		switch typed := v.(type) {
		case []Message:
			msgs = append(msgs, typed...)
		case []any:
			for _, m := range typed {
				if mm, ok := m.(Message); ok {
					msgs = append(msgs, mm)
				}
			}
		}
	}
	// Fallback for chat-graph LoopNode bodies: when the model node sits
	// inside a loop body its inputs are threaded by the LoopNode's
	// `current` PortValues, not by edges touching the body node (the
	// kernel filters those). If the upstream didn't supply messages,
	// pull the live conversation from env.History so multi-turn chat
	// works without every body wiring re-threading the history.
	if len(msgs) == 0 && env.History != nil && env.SessionID != "" {
		hist, err := env.History.History(ctx, env.SessionID, 0)
		if err == nil {
			msgs = append(msgs, hist...)
		}
	}

	// Tool allowlist comes from attrs.
	tools := append([]string(nil), a.ToolAllowlist...)

	// Pre-call compaction (FR-041 site #1). The kernel's Compactor
	// looks at the prepared message slice + max_tokens budget and,
	// if the cascading config says "fire at pre_call", returns the
	// compacted messages. Errors from the compactor short-circuit the
	// LLM call so a misconfigured strategy never silently runs an
	// over-budget request.
	if env.Compactor != nil {
		ci := CompactionInput{
			Site:          CompactionSitePreCall,
			RunID:         env.RunID,
			NodeID:        node.ID,
			SessionID:     env.SessionID,
			ProjectID:     env.ProjectID,
			SystemPrompt:  systemPrompt,
			Messages:      msgs,
			TargetTokens:  a.MaxTokens,
			CurrentTokens: estimateTokens(msgs),
		}
		co, err := env.Compactor.Compact(ctx, ci)
		if err != nil {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err": err.Error(),
				"at":  "compaction.pre_call",
			})
			return res, fmt.Errorf("model: node %q: pre-call compaction: %w", node.ID, err)
		}
		if !co.Skipped && len(co.Messages) > 0 {
			msgs = co.Messages
		}
	}

	// Temperature is *float64 on the LLMRequest seam (nil = use provider
	// default). The codegen-emitted ModelAttrs uses bare float64, so we
	// pass nil for the zero value and a fresh pointer otherwise.
	var tempPtr *float64
	if a.Temperature != 0 {
		t := a.Temperature
		tempPtr = &t
	}
	// The rest of the ModelAttrs knob surface (model-request-path-live-
	// 01PMDL01 WP05) follows the same zero-means-unset convention as
	// Temperature above: codegen emits bare value types for manifest
	// attrs (no *T), so a zero value is indistinguishable from "not
	// authored" — matching Temperature's pre-existing limitation.
	var topPPtr *float64
	if a.TopP != 0 {
		v := a.TopP
		topPPtr = &v
	}
	var topKPtr *int
	if a.TopK != 0 {
		v := a.TopK
		topKPtr = &v
	}
	var freqPenaltyPtr *float64
	if a.FrequencyPenalty != 0 {
		v := a.FrequencyPenalty
		freqPenaltyPtr = &v
	}
	var presPenaltyPtr *float64
	if a.PresencePenalty != 0 {
		v := a.PresencePenalty
		presPenaltyPtr = &v
	}
	var seedPtr *int
	if a.Seed != 0 {
		v := a.Seed
		seedPtr = &v
	}
	var parallelToolCallsPtr *bool
	if a.ParallelToolCalls {
		v := a.ParallelToolCalls
		parallelToolCallsPtr = &v
	}
	// ReasoningBudgetTokens follows the same zero-means-unset convention
	// (model-request-path-live-01PMDL01 WP06b): a node that doesn't author
	// reasoning_budget_tokens must not force reasoning on with a budget of
	// zero.
	var reasoningBudgetPtr *int
	if a.ReasoningBudgetTokens != 0 {
		v := a.ReasoningBudgetTokens
		reasoningBudgetPtr = &v
	}
	req := LLMRequest{
		Provider:              a.Provider,
		Model:                 a.Model,
		SystemPrompt:          systemPrompt,
		Messages:              msgs,
		Tools:                 tools,
		MaxTokens:             a.MaxTokens,
		Temperature:           tempPtr,
		TopP:                  topPPtr,
		TopK:                  topKPtr,
		FrequencyPenalty:      freqPenaltyPtr,
		PresencePenalty:       presPenaltyPtr,
		Seed:                  seedPtr,
		ParallelToolCalls:     parallelToolCallsPtr,
		StopSequences:         append([]string(nil), a.StopSequences...),
		ReasoningBudgetTokens: reasoningBudgetPtr,
		FallbackChainId:       a.FallbackChainId,
	}

	if env.Counters != nil {
		// Cheap pre-check: if budget would already be over even before
		// the call, bail out.
		if env.Budget.MaxLLMCallsPerRun > 0 &&
			env.Counters.LLMCallsMade >= env.Budget.MaxLLMCallsPerRun {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventBudgetCapHit, map[string]any{
				"reason": "max_llm_calls_per_run",
				"limit":  env.Budget.MaxLLMCallsPerRun,
			})
			return res, ErrBudgetExceeded
		}
	}

	// Pre-LLM hook (WP06 chat-migration). Mirrors the toolloop's
	// PreSend extension point so the chassis can swap toolloop's
	// HookRunner for the kernel HookManager without losing the
	// pre-call surface (memory.retrieve, redaction transforms, ...).
	// Hooks here are fire-and-record: the FR-027 greedy-memory journal
	// captures the boundary; provider-side mutation (request rewrite)
	// stays in toolloop's HookRunner until that path is fully retired.
	if env.Hooks != nil {
		hookBatch := env.Hooks.Fire(ctx, HookPreLLM, "session",
			"pre-llm — "+node.ID, summarizeMessages(msgs), node.ID)
		for _, e := range hookBatch.Events {
			e.RunID = env.RunID
			e.NodeID = node.ID
			res.Events.Append(e)
		}
	}

	// Thread the kernel's StreamSink onto the call ctx so the
	// chassis-bound provider can pump tokens / tool deltas / usage to
	// the existing llm:stream-chunk topic without a new seam argument.
	// Nil-safe: withStreamSink returns ctx unchanged when env.StreamSink
	// is nil (test runs, scripted activities, batch executions).
	llmCtx := withStreamSink(ctx, env.StreamSink)
	callStart := time.Now()
	logging.L().Info("agentgraph.model.call",
		"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
		"provider", a.Provider, "model", a.Model,
		"messages", len(msgs), "tools", len(tools),
		"max_tokens", a.MaxTokens, "system_prompt_len", len(systemPrompt),
	)
	resp, err := env.LLM.Generate(llmCtx, req)
	if err != nil {
		logging.L().Warn("agentgraph.model.error",
			"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
			"provider", a.Provider, "model", a.Model,
			"err", err.Error(),
			// ctx_err disambiguates a provider failure from an upstream
			// cancellation (e.g. frontend disconnect on desktop focus loss).
			"ctx_err", ctxErrString(ctx),
			"duration_ms", time.Since(callStart).Milliseconds(),
		)
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err": err.Error(),
		})
		return res, fmt.Errorf("model: node %q: %w", node.ID, err)
	}

	logging.L().Info("agentgraph.model.result",
		"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
		"provider", a.Provider, "model", a.Model,
		"tokens", resp.TokensUsed, "cost_usd", resp.CostUSD,
		"finish_reason", resp.FinishReason, "tool_calls", len(resp.ToolCalls),
		"content_len", len(resp.Content),
		"duration_ms", time.Since(callStart).Milliseconds(),
	)

	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	// Build the assistant message + tool-call records.
	asst := Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	}
	out := append(append([]Message(nil), msgs...), asst)
	res.Outputs["response"] = out
	res.Outputs["assistant"] = asst
	res.Outputs["finish_reason"] = resp.FinishReason
	if len(resp.ToolCalls) > 0 {
		res.Outputs["tool_calls"] = resp.ToolCalls
	}

	_ = res.Events.AppendKind(env.RunID, node.ID, EventLLMCall, map[string]any{
		"provider":      a.Provider,
		"model":         a.Model,
		"tokens":        resp.TokensUsed,
		"cost_usd":      resp.CostUSD,
		"finish_reason": resp.FinishReason,
		"tool_calls":    len(resp.ToolCalls),
	})

	// Greedy memory hook: post-LLM (FR-027).
	if env.Hooks != nil && resp.Content != "" {
		hookBatch := env.Hooks.Fire(ctx, HookPostLLM, "session",
			"assistant turn — "+node.ID, resp.Content, node.ID)
		for _, e := range hookBatch.Events {
			e.RunID = env.RunID
			e.NodeID = node.ID
			res.Events.Append(e)
		}
	}

	return res, nil
}

// ---- ToolNode ----

type toolExecutor struct{}

func (toolExecutor) Kind() NodeKind { return NodeKindTool }

func (toolExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ToolAttrs)
	if !ok {
		return res, fmt.Errorf("tool: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	if env.Budget.MaxToolCallsPerRun > 0 && env.Counters != nil &&
		env.Counters.ToolCallsMade >= env.Budget.MaxToolCallsPerRun {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventBudgetCapHit, map[string]any{
			"reason": "max_tool_calls_per_run",
			"limit":  env.Budget.MaxToolCallsPerRun,
		})
		return res, ErrBudgetExceeded
	}

	if !env.Tools.Has(a.Name) {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err":  "unknown tool",
			"tool": a.Name,
		})
		return res, fmt.Errorf("tool: node %q: unknown tool %q", node.ID, a.Name)
	}

	args := make(map[string]any, len(a.Args))
	for k, v := range a.Args {
		args[k] = v
	}
	// Allow the upstream "args" port to override / augment static args.
	if v, ok := inputs.Get("args"); ok {
		if m, ok := v.(map[string]any); ok {
			for k, vv := range m {
				args[k] = vv
			}
		}
	}

	_ = res.Events.AppendKind(env.RunID, node.ID, EventToolCall, map[string]any{
		"tool": a.Name,
		"args": args,
	})

	// Greedy memory hook: pre-tool (WP06 chat-migration).
	if env.Hooks != nil {
		hookBatch := env.Hooks.Fire(ctx, HookPreTool, "session",
			"pre-tool — "+a.Name, summarizeArgs(args), node.ID)
		for _, e := range hookBatch.Events {
			e.RunID = env.RunID
			e.NodeID = node.ID
			res.Events.Append(e)
		}
	}

	// v2 lifecycle hooks: pre_tool_use (WP03, hooks-event-surface-expansion).
	// These may block the dispatch, rewrite args, or inject additional context.
	argsJSON, _ := json.Marshal(args)
	if env.LifecycleHooks != nil {
		merged, err := env.LifecycleHooks.FirePreToolUse(ctx, env.SessionID, a.Name, argsJSON, "", "")
		if err != nil {
			logging.L().Warn("agentgraph.tool.pre_tool_use_hook.failed",
				"run_id", env.RunID, "session_id", env.SessionID,
				"node_id", node.ID, "tool", a.Name, "err", err.Error())
		} else {
			if merged.Blocked {
				// Hook denied — short-circuit; return a tool error result.
				logging.L().Warn("agentgraph.tool.hook_denied",
					"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
					"tool", a.Name, "reason", merged.BlockReason)
				_ = res.Events.AppendKind(env.RunID, node.ID, EventHookDenied, map[string]any{
					"tool":   a.Name,
					"reason": merged.BlockReason,
				})
				res.Outputs["result"] = ToolResult{
					Content: "hook_denied: " + merged.BlockReason,
					IsError: true,
				}
				return res, nil
			}
			// Apply updated_input: rewrite args if the hook provided new input.
			if len(merged.UpdatedInput) > 0 {
				_ = res.Events.AppendKind(env.RunID, node.ID, EventHookInputRewrite, map[string]any{
					"tool":      a.Name,
					"original":  string(argsJSON),
					"rewritten": string(merged.UpdatedInput),
				})
				var rewrittenArgs map[string]any
				if jerr := json.Unmarshal(merged.UpdatedInput, &rewrittenArgs); jerr == nil {
					args = rewrittenArgs
				}
			}
			// Inject additional_context for next LLM turn.
			if merged.AdditionalContext != "" && env.PendingContext != nil {
				_ = env.PendingContext.AppendSystemContext(ctx, env.SessionID, merged.AdditionalContext)
			}
		}
	}

	argKeys := make([]string, 0, len(args))
	for k := range args {
		argKeys = append(argKeys, k)
	}
	callStart := time.Now()
	logging.L().Info("agentgraph.tool.call",
		"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
		"tool", a.Name, "arg_keys", argKeys)
	tr, err := env.Tools.Call(ctx, ToolCall{Name: a.Name, Args: args})
	// Gate: passive tools (e.g. kenaz__sleep) must not consume an
	// iteration slot (FR-010). toolloop.ShouldCountIteration returns
	// false for any tool registered as tool.passive in Cedar.
	if env.Counters != nil && toolloop.ShouldCountIteration(a.Name) {
		env.Counters.AddTool()
	}
	if err != nil {
		logging.L().Warn("agentgraph.tool.error",
			"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
			"tool", a.Name, "err", err.Error(),
			"ctx_err", ctxErrString(ctx),
			"duration_ms", time.Since(callStart).Milliseconds())
		// v2 lifecycle hook: post_tool_use_failure.
		if env.LifecycleHooks != nil {
			_, _ = env.LifecycleHooks.FirePostToolUse(ctx, env.SessionID, a.Name, argsJSON, nil,
				true, err.Error(), "", "")
		}
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err":  err.Error(),
			"tool": a.Name,
		})
		return res, fmt.Errorf("tool: node %q (%s): %w", node.ID, a.Name, err)
	}
	logging.L().Info("agentgraph.tool.result",
		"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
		"tool", a.Name, "is_error", tr.IsError, "bytes", len(tr.Content),
		"duration_ms", time.Since(callStart).Milliseconds())

	// Post-tool compaction (FR-041 site #2). The pipeline decides
	// whether to fire based on result byte size + cascading config.
	// We hand the result content as a single Message so every
	// strategy sees a uniform input shape; on success we replace
	// tr.Content with the compacted message's content.
	if env.Compactor != nil && tr.Content != "" {
		ci := CompactionInput{
			Site:          CompactionSitePostTool,
			RunID:         env.RunID,
			NodeID:        node.ID,
			SessionID:     env.SessionID,
			ProjectID:     env.ProjectID,
			Messages:      []Message{{Role: "tool", Name: a.Name, Content: tr.Content}},
			CurrentTokens: estimateTokens([]Message{{Content: tr.Content}}),
		}
		co, err := env.Compactor.Compact(ctx, ci)
		if err != nil {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err": err.Error(),
				"at":  "compaction.post_tool",
			})
			return res, fmt.Errorf("tool: node %q: post-tool compaction: %w", node.ID, err)
		}
		if !co.Skipped && len(co.Messages) > 0 {
			// Concatenate compacted messages' content; the kernel does
			// not introspect tool result structure beyond the raw
			// string today.
			var sb strings.Builder
			for i, m := range co.Messages {
				if i > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(m.Content)
			}
			tr.Content = sb.String()
		}
	}

	res.Outputs["result"] = tr
	_ = res.Events.AppendKind(env.RunID, node.ID, EventToolResult, map[string]any{
		"tool":     a.Name,
		"is_error": tr.IsError,
		"bytes":    len(tr.Content),
	})

	// v2 lifecycle hook: post_tool_use.
	if env.LifecycleHooks != nil {
		outJSON, _ := json.Marshal(map[string]any{
			"content":  tr.Content,
			"is_error": tr.IsError,
		})
		merged, herr := env.LifecycleHooks.FirePostToolUse(ctx, env.SessionID, a.Name, argsJSON, outJSON,
			false, "", "", "")
		if herr != nil {
			logging.L().Warn("agentgraph.tool.post_tool_use_hook.failed",
				"run_id", env.RunID, "session_id", env.SessionID,
				"node_id", node.ID, "tool", a.Name, "err", herr.Error())
		} else {
			// Apply updated_mcp_tool_output (MCP tools only, but we apply
			// unconditionally — non-MCP tools just never set it).
			if len(merged.UpdatedMCPOutput) > 0 {
				var updatedContent string
				if jerr := json.Unmarshal(merged.UpdatedMCPOutput, &updatedContent); jerr == nil {
					tr.Content = updatedContent
				}
			}
			if merged.AdditionalContext != "" && env.PendingContext != nil {
				_ = env.PendingContext.AppendSystemContext(ctx, env.SessionID, merged.AdditionalContext)
			}
		}
	}

	// Greedy memory hook: post-tool.
	if env.Hooks != nil && tr.Content != "" {
		hookBatch := env.Hooks.Fire(ctx, HookPostTool, "session",
			"tool result — "+a.Name, tr.Content, node.ID)
		for _, e := range hookBatch.Events {
			e.RunID = env.RunID
			e.NodeID = node.ID
			res.Events.Append(e)
		}
	}
	return res, nil
}

// summarizeMessages builds a compact text summary of an outbound LLM
// message slice for the pre-LLM hook payload. The greedy-memory journal
// records the summary string as the hook's content; the canonical
// EventLog batch already carries the full message bodies, so we keep
// this short to avoid duplicating bytes onto the journal.
func summarizeMessages(ms []Message) string {
	if len(ms) == 0 {
		return ""
	}
	last := ms[len(ms)-1]
	body := last.Content
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("%d messages; last(role=%s): %s", len(ms), last.Role, body)
}

// summarizeArgs builds a compact text summary of a tool's arg map for
// the pre-tool hook payload. We avoid serializing the whole arg map so
// secrets in arg values don't land in the journal; the EventToolCall
// payload already carries the canonical args.
func summarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "(no args)"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return fmt.Sprintf("args: %v", keys)
}

// estimateTokens approximates the token count of a message slice
// using the rule-of-thumb 4 bytes / token. The kernel uses this for
// the pre-call compaction threshold check; the canonical token count
// still comes from the LLM provider on the response side.
func estimateTokens(ms []Message) int {
	n := 0
	for _, m := range ms {
		n += len(m.Content) + len(m.Name)
	}
	return (n + 3) / 4
}

// composePrompt joins system-prompt fragments (e.g. a graph-level base
// constitution + a model node's own role prompt) into a single system
// prompt. It delegates to prompts.Compose so there is a single
// implementation shared by the kernel and any graph-authoring code that
// wants the same joining semantics (e.g. seeding a graph's base from
// prompts.DefaultBaseConstitution()).
func composePrompt(parts ...string) string {
	return prompts.Compose(parts...)
}

// graphBaseOf returns the graph-level base system prompt, nil-safe (env.Graph
// may be nil in unit tests). Every model-driven executor composes this with its
// node's own system_prompt so the shared grounding constitution reaches
// planner/reflect/review nodes too, not just plain model nodes.
//
// It also folds in the accumulated backtrack FailureAnnotations
// (autonomy-recovery-runtime-01PMDL03 WP01): every honored
// BacktrackRequest records why the prior attempt was rejected and what
// was tried, and every subsequent compute-executor call re-grounds
// with that history via this pinned system-prompt block — otherwise a
// re-fired node has no memory of its own rejected attempt and is free
// to repeat it verbatim, defeating the point of the rewind.
func graphBaseOf(env *Env) string {
	if env == nil || env.Graph == nil {
		return ""
	}
	base := env.Graph.SystemPrompt
	if env.State == nil {
		return base
	}
	return prompts.Compose(base, renderFailureAnnotations(env.State.FailureAnnotations()))
}

// renderFailureAnnotations formats the accumulated backtrack rejection
// records into a single pinned system-prompt block. Returns "" when
// there are none (the common case — most runs never backtrack).
func renderFailureAnnotations(anns []FailureAnnotation) string {
	if len(anns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Prior attempts were rejected and rewound — do not repeat them verbatim:\n")
	for _, a := range anns {
		fmt.Fprintf(&b, "- [backtrack %d] node %q was rewound: %s", a.Iteration, a.Node, a.Reason)
		if a.RejectedApproach != "" {
			fmt.Fprintf(&b, " (rejected approach: %s)", a.RejectedApproach)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- TransformNode ----

type transformExecutor struct{}

func (transformExecutor) Kind() NodeKind { return NodeKindTransform }

func (transformExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(TransformAttrs)
	if !ok {
		return res, fmt.Errorf("transform: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	fn, ok := env.Transforms.Lookup(a.Name)
	if !ok {
		return res, fmt.Errorf("transform: node %q: unknown transform %q", node.ID, a.Name)
	}
	out, err := fn(ctx, inputs, a.Params)
	if err != nil {
		return res, fmt.Errorf("transform: node %q (%s): %w", node.ID, a.Name, err)
	}
	res.Outputs = out
	return res, nil
}

// BuiltinTransforms registers the WP02 default transforms (FR-006).
//
// The kernel ships with a small set out of the box; project authors
// can register more via TransformRegistry.Register. Names are stable
// wire identifiers and must not change without a spec bump.
func BuiltinTransforms(r *TransformRegistry) {
	r.Register("concat", transformConcat)
	r.Register("json_extract", transformJSONExtract)
	r.Register("truncate_tokens", transformTruncateTokens)
	r.Register("uppercase", transformUppercase)
}

// transformConcat joins the "parts" input slice (or all string-valued
// inputs) with the configured separator (default newline).
func transformConcat(_ context.Context, in PortValues, params map[string]any) (PortValues, error) {
	sep, _ := params["sep"].(string)
	if sep == "" {
		sep = "\n"
	}
	var parts []string
	if v, ok := in["parts"]; ok {
		switch t := v.(type) {
		case []string:
			parts = append(parts, t...)
		case []any:
			for _, p := range t {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
		}
	} else {
		// fall back: every string-typed input, in alphabetical order.
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		// stable order — sort.Strings would do but we avoid the import
		// for one slot; insertion sort is plenty for the transform path.
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		for _, k := range keys {
			if s, ok := in[k].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return PortValues{"out": strings.Join(parts, sep)}, nil
}

// transformJSONExtract reads in["in"] as a JSON document and extracts
// the path keyed by params["path"] (a dot-separated string).
func transformJSONExtract(_ context.Context, in PortValues, params map[string]any) (PortValues, error) {
	doc, _ := in["in"].(string)
	if doc == "" {
		// Maybe the upstream passed []byte.
		if b, ok := in["in"].([]byte); ok {
			doc = string(b)
		}
	}
	if doc == "" {
		return PortValues{"out": nil}, nil
	}
	var data any
	if err := json.Unmarshal([]byte(doc), &data); err != nil {
		return nil, fmt.Errorf("json_extract: parse: %w", err)
	}
	path, _ := params["path"].(string)
	if path == "" {
		return PortValues{"out": data}, nil
	}
	cur := data
	for _, segment := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return PortValues{"out": nil}, nil
		}
		cur = m[segment]
	}
	return PortValues{"out": cur}, nil
}

// transformTruncateTokens truncates the input string to params["max"]
// rune-counted "tokens". Cheap-and-cheerful: no real tokenizer, just
// rune-counting suitable for the WP02 placeholder.
func transformTruncateTokens(_ context.Context, in PortValues, params map[string]any) (PortValues, error) {
	s, _ := in["in"].(string)
	max := 0
	switch m := params["max"].(type) {
	case int:
		max = m
	case int64:
		max = int(m)
	case float64:
		max = int(m)
	}
	if max <= 0 || len(s) == 0 {
		return PortValues{"out": s}, nil
	}
	runes := []rune(s)
	if len(runes) > max {
		runes = runes[:max]
	}
	return PortValues{"out": string(runes)}, nil
}

// transformUppercase upper-cases the input string.
func transformUppercase(_ context.Context, in PortValues, _ map[string]any) (PortValues, error) {
	s, _ := in["in"].(string)
	return PortValues{"out": strings.ToUpper(s)}, nil
}

// ---- ActivityNode ----

type activityExecutor struct{}

func (activityExecutor) Kind() NodeKind { return NodeKindActivity }

func (activityExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ActivityAttrs)
	if !ok {
		return res, fmt.Errorf("activity: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if env.Activities == nil {
		return res, fmt.Errorf("activity: node %q: no catalog wired", node.ID)
	}
	sub, err := env.Activities.Resolve(a.ActivityId, a.Version)
	if err != nil {
		return res, fmt.Errorf("activity: node %q: resolve %q: %w", node.ID, a.ActivityId, err)
	}
	// Run the sub-graph synchronously. The sub-run shares the parent
	// env (memory, counters, tools, etc.) so budgets cascade.
	subKernel := NewKernel(WithEventLog(NewMemoryEventLog()))
	subRunID := env.RunID + ":" + node.ID
	subEnv := *env
	subEnv.RunID = subRunID
	subEnv.Graph = sub
	subEnv.State = NewRunState()
	for k, v := range inputs {
		subEnv.State.SetOutputs("__activity_inputs__"+k, PortValues{"out": v})
	}
	if err := subKernel.Run(ctx, &subEnv); err != nil && !errors.Is(err, ErrPaused) {
		return res, fmt.Errorf("activity: node %q: sub-run: %w", node.ID, err)
	}

	// Surface the last-completed leaf's outputs as our output. If the
	// sub-graph produced no leaves we just pass through the inputs.
	leaves := subKernel.Leaves(subRunID, sub)
	if len(leaves) == 0 {
		res.Outputs["out"] = inputs
		return res, nil
	}
	res.Outputs["out"] = subEnv.State.Outputs(leaves[len(leaves)-1])
	_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeComplete, map[string]any{
		"activity_id": a.ActivityId,
		"version":     a.Version,
		"sub_run":     subRunID,
		"leaf_count":  len(leaves),
	})
	return res, nil
}

// ---- ReflectNode ----

type reflectExecutor struct{}

func (reflectExecutor) Kind() NodeKind { return NodeKindReflect }

func (reflectExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ReflectAttrs)
	if !ok {
		return res, fmt.Errorf("reflect: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	_ = res.Events.AppendKind(env.RunID, node.ID, EventReflectStarted, map[string]any{
		"model":              a.Model,
		"severity_threshold": a.SeverityThreshold,
		"include_trace":      a.IncludeTrace,
	})

	// The reflect step takes a draft, asks the configured model for a
	// critique, and returns both. The model + draft live in inputs.
	draft, _ := inputs.GetString("draft")
	if v, ok := inputs.Get("draft"); ok && draft == "" {
		// May have come through as a Message slice; flatten.
		if msgs, ok := v.([]Message); ok && len(msgs) > 0 {
			draft = msgs[len(msgs)-1].Content
		}
	}

	prompt := "Critique this draft. Reply with severity (low|medium|high) and a one-line revision suggestion.\n\nDraft:\n" + draft
	model := a.Model
	if model == "" {
		model = "default"
	}
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model: model, MaxTokens: 512,
		SystemPrompt: composePrompt(graphBaseOf(env), a.SystemPrompt),
		Messages:     []Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return res, fmt.Errorf("reflect: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	severity := "low"
	low := strings.ToLower(resp.Content)
	switch {
	case strings.Contains(low, "high"):
		severity = "high"
	case strings.Contains(low, "medium"):
		severity = "medium"
	}

	critique := map[string]any{
		"severity":           severity,
		"text":               resp.Content,
		"suggested_revision": resp.Content,
	}
	res.Outputs["critique"] = critique
	res.Outputs["revision"] = []Message{{Role: "user", Content: "Revise: " + resp.Content}}
	res.Outputs["severity"] = severity

	_ = res.Events.AppendKind(env.RunID, node.ID, EventReflectCompleted, map[string]any{
		"severity": severity,
		"tokens":   resp.TokensUsed,
	})
	return res, nil
}

// ---- ReviewNode ----

type reviewExecutor struct{}

func (reviewExecutor) Kind() NodeKind { return NodeKindReview }

func (reviewExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ReviewAttrs)
	if !ok {
		return res, fmt.Errorf("review: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// Track per-node iteration count via RunState (lives only in this
	// process, but matches the kernel's other re-fire counters).
	iterKey := "__review_iter__" + node.ID
	prev := env.State.Outputs(iterKey)
	iter := 0
	if v, ok := prev["count"]; ok {
		if n, ok := v.(int); ok {
			iter = n
		}
	}
	iter++
	env.State.SetOutputs(iterKey, PortValues{"count": iter})

	draft, _ := inputs.GetString("draft")
	if draft == "" {
		if v, ok := inputs.Get("draft"); ok {
			if msgs, ok := v.([]Message); ok && len(msgs) > 0 {
				draft = msgs[len(msgs)-1].Content
			}
		}
	}

	model := a.Model
	if model == "" {
		model = "default"
	}
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model: model, MaxTokens: 256,
		SystemPrompt: composePrompt(graphBaseOf(env), a.SystemPrompt),
		Messages:     []Message{{Role: "user", Content: "Review this. Reply PASS or FAIL with one-line reason.\n\n" + draft}},
	})
	if err != nil {
		return res, fmt.Errorf("review: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	verdict := "fail"
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(resp.Content)), "PASS") {
		verdict = "pass"
	}
	res.Outputs["verdict"] = map[string]any{
		"verdict": verdict,
		"text":    resp.Content,
		"iter":    iter,
	}
	res.Outputs["approved"] = draft

	if verdict == "pass" {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventReviewPass, map[string]any{
			"iter": iter, "tokens": resp.TokensUsed,
		})
		res.Outputs["should_retry"] = false
		return res, nil
	}

	// Fail path. If we still have iterations left, signal the kernel
	// to re-fire upstream; otherwise hard-error per on_cap_hit.
	_ = res.Events.AppendKind(env.RunID, node.ID, EventReviewFail, map[string]any{
		"iter": iter, "tokens": resp.TokensUsed,
	})

	if iter >= a.MaxIterations {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventReviewUnrecov, map[string]any{
			"iter": iter, "cap": a.MaxIterations,
		})
		switch a.OnCapHit {
		case "escalate":
			_ = res.Events.AppendKind(env.RunID, node.ID, EventEscalateTriggered, map[string]any{
				"reason": "review cap hit",
				"iter":   iter,
			})
			res.Outputs["should_retry"] = false
			res.Outputs["escalated"] = true
			return res, nil
		default:
			res.Outputs["should_retry"] = false
			return res, fmt.Errorf("review: node %q: cap hit at iter %d", node.ID, iter)
		}
	}
	res.Outputs["should_retry"] = true
	res.Outputs["retry_target"] = a.UpstreamNode
	return res, nil
}

// ---- PlannerNode (was PlanNode) ----

// plannerExecutor implements ExecPlanner (FR-031). Legacy graphs naming
// `plan` are alias-resolved at load time.
type plannerExecutor struct{}

func (plannerExecutor) Kind() NodeKind { return NodeKindPlanner }

func (plannerExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(PlannerAttrs)
	if !ok {
		return res, fmt.Errorf("planner: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	task, _ := inputs.GetString("task")
	if task == "" {
		task = "(unspecified task)"
	}
	verbosity := a.Verbosity
	if verbosity == "" {
		verbosity = "standard"
	}
	model := a.PlannerModel
	if model == "" {
		model = "default"
	}
	prompt := fmt.Sprintf("Produce a %s plan for the task. Reply with a numbered list of steps.\n\nTask: %s",
		verbosity, task)
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model: model, MaxTokens: 1024,
		SystemPrompt: composePrompt(graphBaseOf(env), a.SystemPrompt),
		Messages:     []Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return res, fmt.Errorf("planner: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	res.Outputs["plan"] = []Message{{Role: "assistant", Content: resp.Content}}
	res.Outputs["plan_text"] = resp.Content
	res.Outputs["verbosity"] = verbosity

	_ = res.Events.AppendKind(env.RunID, node.ID, EventPlanCreated, map[string]any{
		"verbosity": verbosity,
		"model":     model,
		"tokens":    resp.TokensUsed,
	})
	return res, nil
}

// ---- AskNode ----

type askExecutor struct{}

func (askExecutor) Kind() NodeKind { return NodeKindAsk }

func (askExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(AskAttrs)
	if !ok {
		return res, fmt.Errorf("ask: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// First check if a prior pause has already collected an answer.
	if ans, ok := env.Ask.LookupAnswer(ctx, env.RunID, node.ID); ok {
		res.Outputs["answer"] = ans
		_ = res.Events.AppendKind(env.RunID, node.ID, EventAskAnswered, map[string]any{
			"answer_len": len(ans),
		})
		// Hook the user message into greedy memory.
		if env.Hooks != nil && ans != "" {
			b := env.Hooks.Fire(ctx, HookPostUserMessage, "session",
				"user answer — "+node.ID, ans, node.ID)
			for _, e := range b.Events {
				e.RunID = env.RunID
				e.NodeID = node.ID
				res.Events.Append(e)
			}
		}
		return res, nil
	}

	// No answer yet — record pending and signal pause.
	if err := env.Ask.Pending(ctx, env.RunID, node.ID, a.Question); err != nil {
		return res, fmt.Errorf("ask: node %q: pending: %w", node.ID, err)
	}
	_ = res.Events.AppendKind(env.RunID, node.ID, EventAskPending, map[string]any{
		"question": a.Question,
	})
	res.Pause = true
	res.PauseReason = "ask: " + a.Question
	return res, nil
}

// ---- EscalateNode ----

type escalateExecutor struct{}

func (escalateExecutor) Kind() NodeKind { return NodeKindEscalate }

func (escalateExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(EscalateAttrs)
	if !ok {
		return res, fmt.Errorf("escalate: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// One escalation per leg (FR-012). Track via RunState.
	flagKey := "__escalated__" + node.ID
	prev := env.State.Outputs(flagKey)
	if v, ok := prev["fired"].(bool); ok && v && a.OneEscalationOnly {
		return res, fmt.Errorf("escalate: node %q: already escalated once", node.ID)
	}
	env.State.SetOutputs(flagKey, PortValues{"fired": true})

	// The "trigger" port carries the upstream context; we re-fire the
	// upstream's most recent draft via the configured TargetModel.
	upstreamOut := env.State.Outputs(a.UpstreamNode)
	var draft string
	if v, ok := upstreamOut["assistant"]; ok {
		if m, ok := v.(Message); ok {
			draft = m.Content
		}
	}
	if draft == "" {
		if v, ok := inputs.GetString("trigger"); ok {
			draft = v
		}
	}

	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model:     a.TargetModel,
		MaxTokens: 1024,
		Messages: []Message{
			{Role: "user", Content: "Re-do this with higher quality:\n\n" + draft},
		},
		FallbackChainId: a.FallbackChainId,
	})
	if err != nil {
		return res, fmt.Errorf("escalate: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	res.Outputs["result"] = []Message{{Role: "assistant", Content: resp.Content}}
	res.Outputs["model_used"] = a.TargetModel
	_ = res.Events.AppendKind(env.RunID, node.ID, EventEscalateTriggered, map[string]any{
		"target_model": a.TargetModel,
		"upstream":     a.UpstreamNode,
		"tokens":       resp.TokensUsed,
	})
	return res, nil
}

// ---- CompactNode (NEW, FR-039) ----

// compactExecutor implements ExecCompact (FR-039). It delegates to the
// existing core/agentgraph/compaction/ subsystem via the Env.Compactor
// seam — the manifest-declared `strategy` attr selects which strategy
// the compactor invokes. Pre-call / post-tool / manual invocations from
// the kernel continue to flow through the compactor; this kind makes
// the compaction seam graph-author-addressable.
type compactExecutor struct{}

func (compactExecutor) Kind() NodeKind { return NodeKindCompact }

func (compactExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(CompactAttrs)
	if !ok {
		return res, fmt.Errorf("compact: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// Pull the inbound messages — the canonical input port is `input`
	// (per the compute archetype default), but accept `messages` for
	// backwards compatibility with hand-built test graphs.
	var msgs []Message
	if v, ok := inputs.Get("input"); ok {
		if typed, ok := v.([]Message); ok {
			msgs = typed
		}
	}
	if msgs == nil {
		if v, ok := inputs.Get("messages"); ok {
			if typed, ok := v.([]Message); ok {
				msgs = typed
			}
		}
	}

	// When no compactor is wired, return the input unchanged + emit a
	// trace event so authors can spot the no-op.
	if env.Compactor == nil {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err": "no compactor configured",
			"at":  "compact.execute",
		})
		res.Outputs["result"] = msgs
		return res, fmt.Errorf("compact: node %q: no compactor configured", node.ID)
	}

	target := a.TargetTokenBudget
	co, err := env.Compactor.Compact(ctx, CompactionInput{
		Site:         CompactionSiteManual,
		Messages:     msgs,
		TargetTokens: target,
	})
	if err != nil {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err": err.Error(),
			"at":  "compact.execute",
		})
		return res, fmt.Errorf("compact: node %q: %w", node.ID, err)
	}

	res.Outputs["result"] = co.Messages
	_ = res.Events.AppendKind(env.RunID, node.ID, EventCompactionApplied, map[string]any{
		"strategy":        a.Strategy,
		"target_tokens":   target,
		"input_messages":  len(msgs),
		"output_messages": len(co.Messages),
		"skipped":         co.Skipped,
		"custom_subgraph": a.CustomSubgraphId,
	})
	return res, nil
}
