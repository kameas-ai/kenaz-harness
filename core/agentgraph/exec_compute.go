package agentgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
	"github.com/kameas-ai/kenaz-harness/core/logging"
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
	systemPrompt := composePrompt(resolvePromptTemplate(env, a.Provider, a.Model), graphBaseOf(env), a.SystemPrompt)

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

	// autonomy-knobs-live-01PMAG02 WP04 fix F2: a preceding body-node
	// failure that was adapted (continueOnError=adapt, see
	// adaptNodeErrorPayload in exec_control.go) leaves its untrusted-
	// error note on a separate "adapted_error" input rather than
	// inside "messages". Appending it here, AFTER the len(msgs)==0
	// history-fallback check above, is deliberate: an earlier version
	// folded the note directly into a synthesized "messages" slice,
	// which made that slice non-empty and silently suppressed the
	// history fallback — the model saw exactly one orphan note and
	// never recovered the transcript. Keeping the note on its own port
	// means it is additive over whatever msgs ended up being (a real
	// upstream input or the recovered history), never a substitute
	// for it.
	if v, ok := inputs.Get("adapted_error"); ok {
		if note, ok := v.(string); ok && note != "" {
			msgs = append(msgs, Message{Role: "user", Content: note})
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
	//
	// The gate is Env.admitAutomaticCompaction: a nil-Compactor Env is
	// refused outright, and an Env carrying a growth watermark is
	// refused until the live context has grown past its baseline by the
	// policy margin. The very first visit latches that baseline and is
	// therefore always refused — which is exactly the
	// single-fire guarantee compaction-convergence-01PMDL05 wanted,
	// now obtained by construction rather than by welding the site shut.
	liveTokens := estimateTokens(msgs)
	if env.admitAutomaticCompaction(liveTokens) {
		ci := CompactionInput{
			Site:         CompactionSitePreCall,
			RunID:        env.RunID,
			NodeID:       node.ID,
			SessionID:    env.SessionID,
			ProjectID:    env.ProjectID,
			SystemPrompt: systemPrompt,
			Messages:     msgs,
			// TargetTokens is a *context-compaction* budget, not
			// a.MaxTokens (the node's *output* token cap — how much
			// the LLM is allowed to generate). Those are unrelated
			// quantities; conflating them here used to mean a small
			// output cap (e.g. 512-4096) made nearly every real
			// conversation look "over budget" the moment
			// env.Compactor went live in production. Leave it at the
			// documented zero-value ("no specific target — apply
			// cascading-config defaults", see ContextSlice) until a
			// real per-session/model context-window budget is
			// threaded through Env for this seam to size against.
			// SiteConfig.PreCallThreshold (compaction/presets.go)
			// reserves the config-driven slot for that follow-up;
			// today it is consumed by Pipeline.Run (see its threshold gate).
			// Strategies must treat TargetTokens<=0 as a no-op (see
			// DropOldestStrategy.Compact), not an unconditional trim
			// — see compaction-convergence-01PMDL05 WP02.
			TargetTokens:  0,
			CurrentTokens: liveTokens,
			// ContextWindow is the denominator for the site's
			// PreCallThreshold. 0 (unknown model / no catalog) makes the
			// pipeline skip rather than compact toward a guess.
			ContextWindow: resolveContextWindow(env, a.Provider, a.Model),
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
			// A compaction actually landed: re-baseline the watermark so
			// the next site has to clear the margin from the *new*
			// starting point. Without this a long turn would compact on
			// every subsequent model call once it crossed once.
			env.AutoCompaction.Rearm()
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
	// ResponseSchema (structured-output-is-reachable-01PMZE14 WP02): the
	// node's authored json_schema attr, marshalled once here rather than
	// once per adapter. ModelAttrs.Validate() does not check JsonSchema
	// (it declares no manifest constraints on an `object`-typed attr), so
	// this is the first place a malformed authored schema can be caught —
	// a marshal failure is a node error, not a silent drop.
	var responseSchema json.RawMessage
	if len(a.JsonSchema) > 0 {
		b, err := json.Marshal(a.JsonSchema)
		if err != nil {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err": fmt.Sprintf("json_schema attr is not valid JSON: %v", err),
			})
			return res, fmt.Errorf("model: node %q: json_schema attr is not valid JSON: %w", node.ID, err)
		}
		responseSchema = b
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
		ResponseSchema:        responseSchema,
		FallbackChainId:       a.FallbackChainId,
		// model-moves-transcript-01PMCH01 WP02: the node's own
		// stream_to_chat attr, forwarded so the chassis provider seam can
		// tell the chat's assistant turn apart from the six other
		// executors that also call env.LLM.Generate. This is the attr's
		// first reader.
		StreamToChat: a.StreamToChat,
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

	// Pre-LLM hook (WP06 chat-migration). This boundary is what the
	// pre-kernel chassis loop's PreSend extension point became, so the
	// pre-call surface (memory.retrieve, redaction transforms, ...)
	// survived the cutover.
	//
	// Hooks here are fire-and-RECORD: the FR-027 greedy-memory journal
	// captures the boundary but nothing here rewrites the outgoing
	// request. Provider-side MUTATION lives one layer up, in the LLM
	// view's HookRunner seam (core/rpc/views/llm/impl.go RunPreSend,
	// backed by core/hooks.Runner) — that is a real, live split, not a
	// migration remnant. (The comment here used to say the mutation
	// path "stays in toolloop's HookRunner until that path is fully
	// retired"; core/toolloop has no HookRunner and never did after the
	// cutover. Corrected under 01PMGX01 invariant I8, 2026-08-13.)
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

// ---- Builtin tool nodes (the `tool` archetype) ----
//
// agentgraph-total-convergence-01PMGX01 WP04 (spec §2.5, §4.2): "tools
// are nodes". This executor is the successor to the old generic `tool`
// KIND — same body, one structural change: the tool it invokes is no
// longer read from a `name` attr the graph author picks, it is fixed by
// the node's KIND through the archetype's kenaz__<kind> naming
// contract. That is what makes a kind the unit of accounting and
// authorisation rather than a label on a call.
//
// One instance is registered per callable kind whose inheritance chain
// includes the `tool` archetype (see builtinToolExecutors in
// executor.go), so declaring a manifest that extends `tool` is the
// whole contract for adding a builtin tool node.

// builtinToolExecutor invokes exactly one builtin tool, named by the
// kind it is registered for.
type builtinToolExecutor struct {
	kind     NodeKind
	toolName string
}

func (b builtinToolExecutor) Kind() NodeKind { return b.kind }

// builtinToolNameFor renders the archetype's naming contract: a
// tool-archetype kind `sleep` invokes the builtin `kenaz__sleep`.
func builtinToolNameFor(kind NodeKind) string { return "kenaz__" + string(kind) }

// toolArgsFromAttrs renders a node's typed attrs as the tool-call
// argument map. The generated *Attrs structs carry `json:"...,omitempty"`
// tags matching the manifest attr names, so a round-trip through JSON
// yields exactly the attrs the author set — inherited-but-unset
// archetype attrs drop out via omitempty rather than being sent to the
// tool as zero values.
func toolArgsFromAttrs(attrs NodeAttrs) map[string]any {
	if attrs == nil {
		return map[string]any{}
	}
	buf, err := json.Marshal(attrs)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(buf, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func (b builtinToolExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a := struct{ Name string }{Name: b.toolName}

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
		return res, fmt.Errorf("%s: node %q: unknown tool %q", b.kind, node.ID, a.Name)
	}

	args := toolArgsFromAttrs(node.Attrs)
	// Allow the upstream "args" port to override / augment the attrs.
	// Non-map payloads (e.g. an upstream ToolResult threaded in purely
	// to order two tool nodes) are ignored rather than coerced.
	if v, ok := inputs.Get("args"); ok {
		if m, ok := v.(map[string]any); ok {
			for k, vv := range m {
				args[k] = vv
			}
		}
	}

	// The pre-call gate is shared with tool_dispatch — greedy-memory
	// hook, canonical EventToolCall, v2 pre_tool_use hooks (block /
	// rewrite args / inject context). See tool_invocation.go.
	tc := toolCallContext{NodeID: node.ID, NodeKind: b.kind, ToolName: a.Name}
	args, argsJSON, _, blocked, preEvents := toolPreDispatch(ctx, env, tc, args)
	res.Events.Events = append(res.Events.Events, preEvents.Events...)
	if blocked != nil {
		res.Outputs["result"] = *blocked
		return res, nil
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
	chargeToolIteration(env, a.Name)
	if err != nil {
		logging.L().Warn("agentgraph.tool.error",
			"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
			"tool", a.Name, "err", err.Error(),
			"ctx_err", ctxErrString(ctx),
			"duration_ms", time.Since(callStart).Milliseconds())
		_, failEvents := toolPostDispatch(ctx, env, tc, argsJSON, tr, err, nil)
		res.Events.Events = append(res.Events.Events, failEvents.Events...)
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err":  err.Error(),
			"tool": a.Name,
		})
		return res, fmt.Errorf("%s: node %q (%s): %w", b.kind, node.ID, a.Name, err)
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
	//
	// WP02 gate: this site sees a single tool result, not the live
	// transcript, so it cannot latch or evaluate a watermark baseline of
	// its own — feeding it a result byte count would corrupt the
	// baseline. It rides the pre_call site's verdict instead
	// (Env.automaticCompactionCrossed): once the run's context has
	// genuinely grown past the watermark, the automatic sites are live.
	//
	// This is also the one step tool_dispatch does NOT share (see the
	// "STILL SPLIT, DELIBERATELY" note in tool_invocation.go): Phase 4
	// WP08 replaces it with a `compact` node rather than duplicating a
	// per-call compactor into a parallel fan-out.
	if tr.Content != "" && env.automaticCompactionCrossed() {
		ci := CompactionInput{
			Site:          CompactionSitePostTool,
			RunID:         env.RunID,
			NodeID:        node.ID,
			SessionID:     env.SessionID,
			ProjectID:     env.ProjectID,
			Messages:      []Message{{Role: "tool", Name: a.Name, Content: tr.Content}},
			CurrentTokens: estimateTokens([]Message{{Content: tr.Content}}),
		}
		co, cerr := env.Compactor.Compact(ctx, ci)
		if cerr != nil {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err": cerr.Error(),
				"at":  "compaction.post_tool",
			})
			return res, fmt.Errorf("%s: node %q: post-tool compaction: %w", b.kind, node.ID, cerr)
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

	// Shared post-call bookkeeping: EventToolResult + v2 post_tool_use
	// hooks (output rewrite / additional context).
	tr, postEvents := toolPostDispatch(ctx, env, tc, argsJSON, tr, nil, nil)
	res.Events.Events = append(res.Events.Events, postEvents.Events...)
	res.Outputs["result"] = tr

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

// estimateTokens approximates the token count of a message slice.
//
// compaction-convergence-01PMDL05: this used to be its own independent
// byte-length/4 heuristic (no per-message framing overhead, no rune
// awareness) — a second, weaker token estimator living alongside
// core/llm/tokenizer.CountRequestTokens, which core/agentgraph/compaction's
// pre-send path already used. Two estimators meant the kernel's
// pre_call/post_tool compaction sites and the pre-send compactor could
// disagree about how close a conversation is to its context-window
// cap. This now delegates to the canonical estimator so there is one
// token-counting implementation regardless of which compaction path is
// active. The kernel uses this for the pre-call/post-tool compaction
// threshold checks; the canonical token count still comes from the LLM
// provider on the response side.
func estimateTokens(ms []Message) int {
	translated := make([]tokenizer.Message, len(ms))
	for i, m := range ms {
		translated[i] = tokenizer.Message{Role: m.Role, Content: m.Content}
	}
	// No system prompt here: callers that track one separately (e.g.
	// the pre_call site) pass it to the compactor via
	// CompactionInput.SystemPrompt, not through this message slice.
	return tokenizer.CountRequestTokens("", translated)
}

// composePrompt joins system-prompt fragments (e.g. a graph-level base
// constitution + a model node's own role prompt) into a single system
// prompt. It delegates to prompts.Compose so there is a single
// implementation shared by the kernel and any graph-authoring code that
// wants the same joining semantics (e.g. seeding a graph's base from
// prompts.DefaultBaseConstitution()).
//
// tmpl is the per-family-message-shaping-01PMDL06 descriptor (nil until
// resolvePromptTemplate resolves a live one — see that function's doc
// for why nothing does yet); it flows straight through to
// prompts.Compose, which is the single place that decides what a nil
// vs. populated tmpl renders as.
func composePrompt(tmpl *corellm.PromptTemplateRef, parts ...string) string {
	return prompts.Compose(tmpl, parts...)
}

// graphBaseOf returns the graph-level base system prompt, nil-safe (env.Graph
// may be nil in unit tests). Every model-driven executor composes this with its
// node's own system_prompt so the shared grounding constitution reaches
// planner/reflect/review nodes too, not just plain model nodes.
//
// It also folds in the run's structured TaskState — goal, completed-step
// summary, forbidden actions — plus the accumulated backtrack
// FailureAnnotations (autonomy-recovery-runtime-01PMDL03 WP01 + WP03):
// every honored BacktrackRequest records why the prior attempt was
// rejected and what was tried, and every subsequent compute-executor
// call re-grounds with that history via this pinned system-prompt
// block — otherwise a re-fired node has no memory of its own rejected
// attempt and is free to repeat it verbatim, defeating the point of
// the rewind. This is the single injection site for both mechanisms;
// there is deliberately no second place a compute executor pulls this
// context from.
func graphBaseOf(env *Env) string {
	if env == nil || env.Graph == nil {
		return ""
	}
	base := env.Graph.SystemPrompt
	if env.State == nil {
		return base
	}
	// nil tmpl: this composition folds the graph base + task-state
	// annotations into a single "base" string that becomes one of the
	// *parts* fed to the outer composePrompt call at each executor call
	// site — that outer call is the one that resolves and applies the
	// per-family renderer, so this inner join stays the plain default.
	return prompts.Compose(nil, base, renderTaskState(env.TaskState, env.State.FailureAnnotations()))
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
	// MaxIterations: an explicit author value always wins; when unset
	// (<= 0, the manifest's existing "optional" constraint), derive the
	// cap from the active model's tier the same way reviewExecutor does
	// (versioned-model-profile-01PMDL04 WP05). Note: nothing currently
	// enforces this cap when a Reflect node is placed inside a Loop body
	// — that wiring gap pre-dates WP05 and is out of scope here. This
	// resolves and surfaces the value on the started event so a future
	// Loop-embedding consumer has a real number to read instead of the
	// schema-only zero value.
	maxIterations := a.MaxIterations
	if maxIterations <= 0 {
		maxIterations = maxIterationsForTier(resolveNodeTier(env, a.Provider, a.Model))
	}
	_ = res.Events.AppendKind(env.RunID, node.ID, EventReflectStarted, map[string]any{
		"model":              a.Model,
		"severity_threshold": a.SeverityThreshold,
		"include_trace":      a.IncludeTrace,
		"max_iterations":     maxIterations,
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
		SystemPrompt: composePrompt(resolvePromptTemplate(env, a.Provider, model), graphBaseOf(env), a.SystemPrompt),
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
	// MaxIterations: an explicit author value (the manifest allows any
	// non-negative int now — 0 means "unset") always wins. When unset,
	// derive the cap from the active model's tier rather than the one
	// fixed default every graph got before (versioned-model-profile-
	// 01PMDL04 WP05). No tier data anywhere resolves to ModelTierMedium,
	// which maxIterationsForTier maps to 3 — review.yaml's pre-existing
	// explicit default — so a run with no tier source configured keeps
	// today's behaviour.
	maxIterations := a.MaxIterations
	if maxIterations <= 0 {
		maxIterations = maxIterationsForTier(resolveNodeTier(env, a.Provider, model))
	}
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model: model, MaxTokens: 256,
		SystemPrompt: composePrompt(resolvePromptTemplate(env, a.Provider, model), graphBaseOf(env), a.SystemPrompt),
		Messages:     []Message{{Role: "user", Content: reviewPrompt(env, draft)}},
	})
	if err != nil {
		return res, fmt.Errorf("review: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	verdict, reason := parseReviewVerdict(resp.Content)
	res.Outputs["verdict"] = map[string]any{
		"verdict": verdict,
		"reason":  reason,
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
		"iter": iter, "tokens": resp.TokensUsed, "reason": reason,
	})

	if iter >= maxIterations {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventReviewUnrecov, map[string]any{
			"iter": iter, "cap": maxIterations,
		})
		// F7 (autonomy-knobs review finding, landed here in WP11b).
		// At askOnAmbiguity proceed/never the user has said they do not
		// want to be interrupted, and the chat surface already withholds
		// kenaz__ask_user_question from the model's catalogue. An exit
		// gate that responds to a cap hit by escalating re-opens that
		// door from a path the user never saw: `escalate` leads to the
		// ladder, and the ladder's terminal rung asks a human.
		//
		// So under Withhold the cap hit RETURNS THE BEST DRAFT. The
		// verdict is still FAIL, still on the EventLog, still visible —
		// what changes is that the run ends with the work in hand
		// rather than with a question. That is the honest reading of
		// "proceed": proceed.
		if env.AskPolicy == AskPolicyWithhold {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventReviewCapProceeded, map[string]any{
				"iter": iter, "cap": maxIterations,
				"on_cap_hit": a.OnCapHit,
				"reason":     reason,
			})
			logging.L().Info("agentgraph.review.cap_proceeded",
				"run_id", env.RunID, "node_id", node.ID,
				"iter", iter, "cap", maxIterations,
				"detail", "askOnAmbiguity withholds questions; returning the best draft instead of escalating")
			res.Outputs["should_retry"] = false
			return res, nil
		}
		switch a.OnCapHit {
		case "escalate":
			_ = res.Events.AppendKind(env.RunID, node.ID, EventEscalateTriggered, map[string]any{
				"reason": "review cap hit",
				"iter":   iter,
			})
			res.Outputs["should_retry"] = false
			// wiring:deferred(no reader found for this specific flag — should_retry=false plus the EventEscalateTriggered event above already carry the cap-hit-escalate signal to the kernel promotion path and EventLog respectively; surfaced by check-output-ports.sh, wiring-integrity-01PMAG04 WP05)
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

// reviewPrompt builds the user-side message for a review call
// (agentgraph-total-convergence-01PMGX01 WP11b; design in
// agentic-turn-routing-01PMAG01 §3.4 hardening item 1).
//
// WHAT WAS WRONG. The prompt was the fixed string "Review this. Reply
// PASS or FAIL with one-line reason.\n\n" + draft. A reviewer handed a
// draft and nothing else is not checking the work against the user's
// request — it is rating prose in a vacuum, and it will happily PASS a
// confident, well-written answer to a question nobody asked. That is
// precisely the "check its work before returning" clause failing open.
//
// The goal and the completed-step trail come from TaskState, which is
// why WP11b also made TaskState always-armed: on a run that never
// failed — the run an exit gate is most likely to see — the goal was
// empty by construction (01PMAG01 G5). When TaskState is empty this
// degrades to the old vacuum prompt rather than erroring, because a
// review node in a hand-built graph with no history seam is a
// legitimate configuration.
//
// The JSON instruction is the structured-verdict half. There is no
// provider-level response_format on the LLMRequest seam today, so
// "structured output" is requested in the prompt and parsed
// tolerantly; parseReviewVerdict keeps the substring path as the
// fallback for a provider that ignores the instruction.
func reviewPrompt(env *Env, draft string) string {
	var b strings.Builder
	b.WriteString("You are the exit gate for a task. Decide whether the work below is complete and correct.\n\n")

	if env != nil {
		if goal := env.TaskState.Goal(); goal != "" {
			b.WriteString("The user's goal:\n")
			b.WriteString(goal)
			b.WriteString("\n\n")
		}
		if steps, elided := env.TaskState.CompletedSteps(); len(steps) > 0 {
			b.WriteString("Steps completed so far")
			if elided > 0 {
				fmt.Fprintf(&b, " (+%d earlier steps elided)", elided)
			}
			b.WriteString(":\n")
			for _, s := range steps {
				b.WriteString("- ")
				b.WriteString(s)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("The work to review:\n")
	b.WriteString(draft)
	b.WriteString("\n\n")
	b.WriteString(`Reply with a JSON object and nothing else: {"verdict": "pass" | "fail", "reason": "<one line>"}. ` +
		`Answer "pass" only if the work actually satisfies the goal above; answer "fail" if it is partial, ` +
		`unverified, or answers a different question.`)
	return b.String()
}

// parseReviewVerdict extracts a review verdict from a model reply
// (01PMAG01 §3.4 hardening item 2).
//
// WHAT WAS WRONG. The old rule was
// strings.HasPrefix(upper(trim(resp)), "PASS"), so any preamble at all
// — "Sure! PASS", "Verdict: PASS", a leading newline plus a markdown
// heading — scored a FAIL. A gate that fails on the model being polite
// burns the retry budget on work that was already correct, and at the
// cap it either errors the run or escalates. The defect is not
// cosmetic; it is a gate that is wrong in the safe-looking direction.
//
// Three passes, most-structured first:
//  1. a JSON object carrying {"verdict": ...} (what reviewPrompt asks
//     for, and what a provider with real structured output returns);
//  2. a bare `verdict` token scan, for JSON-ish-but-invalid replies;
//  3. the historical substring rule, widened from HasPrefix to a
//     word-boundary scan of the first non-empty line.
//
// Defaulting to "fail" is deliberate and unchanged: an unreadable
// verdict must not be read as approval.
func parseReviewVerdict(text string) (verdict, reason string) {
	norm := func(s string) string {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "pass", "passed", "approve", "approved":
			return "pass"
		case "fail", "failed", "reject", "rejected":
			return "fail"
		}
		return ""
	}

	// (1) structured object.
	for _, obj := range jsonObjectCandidates(text) {
		var m map[string]any
		if err := json.Unmarshal([]byte(obj), &m); err != nil {
			continue
		}
		raw, ok := m["verdict"].(string)
		if !ok {
			continue
		}
		if v := norm(raw); v != "" {
			r, _ := m["reason"].(string)
			return v, strings.TrimSpace(r)
		}
	}

	// (2) bare token scan, reusing the router's tolerant field parser.
	if picked, ok := parseChoiceField(text, "verdict", map[string]bool{
		"pass": true, "fail": true, "passed": true, "failed": true,
		"PASS": true, "FAIL": true,
	}); ok {
		if v := norm(picked); v != "" {
			return v, firstNonEmptyLine(text)
		}
	}

	// (3) substring fallback, so a preamble no longer flips the verdict.
	//
	// The first non-empty line is preferred over the whole reply,
	// because that is where a verdict actually goes and a long
	// explanation below it may mention the other word in passing
	// ("...otherwise this would fail"). Only when the first line
	// carries no verdict at all — "## Review" above the answer — does
	// the scan widen to the whole reply.
	//
	// Within each pass, FAIL is checked first: a reply carrying both
	// words ("this is not a PASS. FAIL: the table is missing") is a
	// rejection, and a gate that is unsure must not approve.
	//
	// containsWord, not Contains, so "PASSAGE" is not a PASS.
	head := firstNonEmptyLine(text)
	for _, scope := range []string{head, text} {
		upper := strings.ToUpper(scope)
		switch {
		case containsWord(upper, "FAIL"):
			return "fail", head
		case containsWord(upper, "PASS"):
			return "pass", head
		}
	}
	return "fail", head
}

// firstNonEmptyLine returns the first line of s with content, trimmed.
// Rune-safe: it splits on '\n', which can never fall inside a
// multi-byte rune in valid UTF-8.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// containsWord reports whether word appears in s delimited by
// non-alphanumeric boundaries, so "PASSAGE" does not match "PASS".
// s and word are expected pre-upper-cased by the caller.
func containsWord(s, word string) bool {
	isAlnum := func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
	}
	from := 0
	for {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(word)
		beforeOK := start == 0
		if !beforeOK {
			r, _ := utf8.DecodeLastRuneInString(s[:start])
			beforeOK = !isAlnum(r)
		}
		afterOK := end == len(s)
		if !afterOK {
			r, _ := utf8.DecodeRuneInString(s[end:])
			afterOK = !isAlnum(r)
		}
		if beforeOK && afterOK {
			return true
		}
		from = end
	}
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
	model := a.PlannerModel
	if model == "" {
		model = "default"
	}
	// Verbosity: an explicit author value always wins. When unset,
	// derive it from the active model's tier instead of hardcoding one
	// tier's behaviour for every model (versioned-model-profile-01PMDL04
	// WP05) — resolveNodeTier resolves ModelTierMedium when there's no
	// tier data anywhere, and verbosityForTier maps that to "standard",
	// so a run with no tier source configured sees this executor's
	// pre-WP05 output unchanged.
	verbosity := a.Verbosity
	if verbosity == "" {
		verbosity = verbosityForTier(resolveNodeTier(env, a.Provider, model))
	}
	prompt := fmt.Sprintf("Produce a %s plan for the task. Reply with a numbered list of steps.\n\nTask: %s",
		verbosity, task)
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model: model, MaxTokens: 1024,
		SystemPrompt: composePrompt(resolvePromptTemplate(env, a.Provider, model), graphBaseOf(env), a.SystemPrompt),
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
	// wiring:deferred(capability library — no shipped graph includes a planner node; see docs/wiring-audit.md item 3d)
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

// askQuestion builds the canonical question from the node's attrs.
//
// The free-form case (no question_kind, no question_spec) produces
// exactly what the node has always produced: an elicitation.Question
// carrying only Text. The structured case decodes question_spec through
// the same shape and the same validator kenaz__ask_user_question uses
// (01PMGX01 WP06, spec §4.3), so a graph can pose any question the
// model-facing tool can.
//
// question_spec must not redeclare `question` or `kind`: those are the
// `question` / `question_kind` attrs, and a second spelling of either
// would be a silent-precedence trap. The decoder rejects them.
func askQuestion(a AskAttrs) (elicitation.Question, error) {
	q := elicitation.Question{
		Text: a.Question,
		Kind: elicitation.Kind(a.QuestionKind),
	}
	if len(a.QuestionSpec) > 0 {
		raw, err := json.Marshal(a.QuestionSpec)
		if err != nil {
			return q, fmt.Errorf("question_spec: encode: %w", err)
		}
		var spec elicitation.Question
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return q, fmt.Errorf("question_spec: %w", err)
		}
		if spec.Text != "" {
			return q, errors.New("question_spec: `question` belongs in the question attr, not the spec")
		}
		if spec.Kind != elicitation.KindFreeform {
			return q, errors.New("question_spec: `kind` belongs in the question_kind attr, not the spec")
		}
		spec.Text, spec.Kind = q.Text, q.Kind
		q = spec
	}
	if err := q.Validate(); err != nil {
		return q, err
	}
	return q, nil
}

func (askExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(AskAttrs)
	if !ok {
		return res, fmt.Errorf("ask: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// First check if a prior pause has already collected an answer.
	if ans, ok := env.Ask.LookupAnswer(ctx, env.RunID, node.ID); ok {
		text := ans.String()
		res.Outputs["answer"] = ans.Decoded()
		_ = res.Events.AppendKind(env.RunID, node.ID, EventAskAnswered, map[string]any{
			"answer_len": len(text),
		})
		// Hook the user message into greedy memory.
		if env.Hooks != nil && text != "" {
			b := env.Hooks.Fire(ctx, HookPostUserMessage, "session",
				"user answer — "+node.ID, text, node.ID)
			for _, e := range b.Events {
				e.RunID = env.RunID
				e.NodeID = node.ID
				res.Events.Append(e)
			}
		}
		return res, nil
	}

	// autonomy-knobs-live-01PMAG02 WP02: askOnAmbiguity=never resolves an
	// unseeded AskNode to its stated default rather than pausing the run
	// (spec §3.1 bullet 2). DefaultAnswer is only ever populated by
	// chat_runner.go's applyAskOnAmbiguityDial when the knob is "never";
	// an empty DefaultAnswer (every other knob value, and every AskNode
	// outside the chat runner's wiring) preserves today's
	// pause-on-no-answer behaviour exactly (FR-005).
	if a.DefaultAnswer != "" {
		ans := a.DefaultAnswer
		res.Outputs["answer"] = ans
		_ = res.Events.AppendKind(env.RunID, node.ID, EventAskAnswered, map[string]any{
			"answer_len":    len(ans),
			"auto_resolved": true,
		})
		if env.Hooks != nil {
			b := env.Hooks.Fire(ctx, HookPostUserMessage, "session",
				"auto-resolved answer — "+node.ID, ans, node.ID)
			for _, e := range b.Events {
				e.RunID = env.RunID
				e.NodeID = node.ID
				res.Events.Append(e)
			}
		}
		return res, nil
	}

	question, err := askQuestion(a)
	if err != nil {
		return res, fmt.Errorf("ask: node %q: %w", node.ID, err)
	}

	// No answer yet — record pending and signal pause.
	if err := env.Ask.Pending(ctx, env.RunID, node.ID, question); err != nil {
		return res, fmt.Errorf("ask: node %q: pending: %w", node.ID, err)
	}
	// The payload stays exactly `{"question": …}` for a free-form ask so
	// pinned event goldens do not move; a structured ask additionally
	// reports the control it asked for.
	payload := map[string]any{"question": question.Text}
	if question.Kind != elicitation.KindFreeform {
		payload["kind"] = string(question.Kind)
	}
	_ = res.Events.AppendKind(env.RunID, node.ID, EventAskPending, payload)
	res.Pause = true
	res.PauseReason = "ask: " + question.Text
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

	// Ground the escalated call the same way every other compute
	// executor does (wiring-integrity-01PMAG04 WP00). This call fires
	// *after* something failed — exactly when graphBaseOf's TaskState
	// block and the accumulated backtrack FailureAnnotations are
	// populated and most valuable. Sending it bare meant the one path
	// that most needs to know what was already tried and rejected was
	// the only path running without that context.
	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Model:     a.TargetModel,
		MaxTokens: 1024,
		SystemPrompt: composePrompt(
			resolvePromptTemplate(env, a.Provider, a.TargetModel),
			graphBaseOf(env), a.SystemPrompt),
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
	// model_used dropped (wiring-integrity-01PMAG04 WP03, docs/wiring-audit.md
	// item 3c): the value is already carried on EventEscalateTriggered's
	// target_model field below, and the Outputs port had no reader.
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

	// When no compactor is wired, pass the input through untouched and
	// record a skip so authors can see the node ran and did nothing.
	//
	// This used to be a hard error, which was defensible while no
	// shipped graph contained a compact node: an author who placed one
	// deliberately wanted to know it was inert. It stopped being
	// defensible when agentgraph-total-convergence-01PMGX01 WP08 put a
	// compact node in chat_default.yaml — the graph every chat turn
	// runs — because a chassis with no compaction wired (a test
	// fixture, a boot where the engine failed to construct) would then
	// fail every chat turn outright rather than simply not compacting.
	// "No compactor configured" is the absence of a capability, not a
	// broken graph, and the honest response is a skip.
	if env.Compactor == nil {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventCompactionApplied, map[string]any{
			"strategy":        a.Strategy,
			"input_messages":  len(msgs),
			"output_messages": len(msgs),
			"skipped":         true,
			"skip_reason":     "no compactor configured",
		})
		res.Outputs["result"] = msgs
		return res, nil
	}

	// TargetTokens: the graph author's explicit budget when they set
	// one, otherwise zero — which the pipeline reads as "derive the
	// target from the model's context window and the resolved
	// threshold" (Pipeline.Run).
	//
	// It is emphatically NOT a.MaxTokens. That attr is the node's
	// *output* cap — how many tokens the model may generate — and the
	// two are unrelated quantities. Conflating them (which the pre-call
	// site did until compaction-convergence-01PMDL05 WP02 was folded
	// into this WP) makes a small output cap of 512-4096 look like a
	// context budget, so nearly every real conversation reads as
	// wildly over budget the moment a compactor is actually wired.
	// compaction_target_test.go pins the separation.
	target := a.TargetTokenBudget
	liveTokens := estimateTokens(msgs)
	co, err := env.Compactor.Compact(ctx, CompactionInput{
		Site:      CompactionSiteManual,
		RunID:     env.RunID,
		NodeID:    node.ID,
		SessionID: env.SessionID,
		ProjectID: env.ProjectID,
		// Strategy forces the pipeline to dispatch the author's choice
		// rather than falling through to the resolved cascading config
		// (CHAT-07) — this is the node's whole reason to carry a
		// `strategy` attr in the first place.
		Strategy: a.Strategy,
		// SystemPrompt was omitted here entirely (CHAT-14/CK-14): the
		// attr exists on CompactAttrs and the seam has always had a
		// field for it, but nothing threaded the value through, so a
		// manual compaction silently dropped the system prompt every
		// time.
		SystemPrompt: a.SystemPrompt,
		Messages:     msgs,
		// The context-window budget this compaction sizes against —
		// the denominator for every threshold decision downstream.
		ContextWindow: resolveContextWindow(env, a.Provider, a.Model),
		CurrentTokens: liveTokens,
		TargetTokens:  target,
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
		// co.Strategy is what the pipeline actually dispatched — never
		// a.Strategy, the node's unresolved request. Reporting the
		// attr here is the exact CHAT-07 defect: an event that
		// describes a strategy that did not run.
		"strategy":        co.Strategy,
		"target_tokens":   target,
		"input_messages":  len(msgs),
		"output_messages": len(co.Messages),
		"skipped":         co.Skipped,
		"custom_subgraph": a.CustomSubgraphId,
	})
	return res, nil
}
