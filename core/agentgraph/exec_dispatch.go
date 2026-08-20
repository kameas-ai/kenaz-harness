package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// toolDispatchExecutor implements ExecToolDispatch (mission
// tool-dispatch-node). Reads a slice of model-emitted ToolCallRequest
// records from its `tool_calls` input port, dispatches each one through
// the kernel ToolRegistry, and emits the aggregated results on
// `tool_results` plus a Message-shaped slice on `messages` ready to be
// folded back into the next LLMNode call.
//
// Compared to the static-name `tool` kind:
//   - tool dispatches one statically-configured (name, args) pair.
//   - tool_dispatch dispatches every record on its upstream tool_calls
//     port, in parallel by default. The model decides what to call;
//     the kernel only routes.
//
// This is what closes the chat LoopNode's model→tool→model cycle: the
// LLMNode emits tool_calls, the LoopNode body's tool_dispatch fans them
// out, and the body re-enters the LLMNode with the tool messages.
type toolDispatchExecutor struct{}

func (toolDispatchExecutor) Kind() NodeKind { return NodeKindToolDispatch }

// dispatchOutcome is the per-call book-keeping kept alongside the
// ToolResult so we can build the tool messages slice in the original
// tool_calls order (errgroup parallel dispatch loses ordering otherwise).
type dispatchOutcome struct {
	call    ToolCallRequest
	args    map[string]any
	rawArgs string
	result  ToolResult
	err     error
	events  EventBatch

	// doomLoopHit / doomLoopCount are set when this call's (tool,
	// normalized-arg-hash) key has repeated at least the configured
	// threshold this run (WP02 doom-loop guard). Aggregated after
	// dispatch into the node-level `should_replan` signal.
	doomLoopHit   bool
	doomLoopCount int
}

func (toolDispatchExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ToolDispatchAttrs)
	if !ok {
		return res, fmt.Errorf("tool_dispatch: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	calls, err := extractToolCalls(inputs)
	if err != nil {
		return res, fmt.Errorf("tool_dispatch: node %q: %w", node.ID, err)
	}
	if len(calls) == 0 {
		// No tool calls is a soft no-op — the LoopNode body upstream of
		// us decided not to dispatch this iteration, but the kernel still
		// needs a clean output so downstream consumers see empty slices.
		// Surface a `tool_call_count` so loop conditions can break on
		// `tool_call_count == 0` without inspecting slice length.
		//
		// Pass the upstream assistant Message + text through so that an
		// outside-loop SessionWriteNode can read `assistant_text` /
		// `assistant` from the loop's flattened outputs once the
		// LoopNode breaks on the no-tool-call iteration.
		res.Outputs["tool_results"] = []ToolResult{}
		res.Outputs["messages"] = []Message{}
		res.Outputs["tool_messages"] = []Message{}
		res.Outputs["tool_call_count"] = 0
		passthroughAssistant(inputs, res.Outputs)
		return res, nil
	}

	// Pre-flight budget gate (mirrors the static tool executor): if the
	// run would already exceed the tool-call cap before dispatch, bail
	// fast so the model gets a budget-cap signal rather than partial
	// dispatch.
	if env.Budget.MaxToolCallsPerRun > 0 && env.Counters != nil {
		need := env.Counters.ToolCallsMade + len(calls)
		if need > env.Budget.MaxToolCallsPerRun {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventBudgetCapHit, map[string]any{
				"reason":  "max_tool_calls_per_run",
				"limit":   env.Budget.MaxToolCallsPerRun,
				"pending": len(calls),
				"used":    env.Counters.ToolCallsMade,
			})
			return res, ErrBudgetExceeded
		}
	}

	// Confirmation batching (confirm-each-enforcement-01PMAG05 WP02).
	//
	// One Execute is one turn's worth of model-emitted tool calls, which
	// is exactly the grouping the batched confirmation modal needs
	// (owner decision 3: one dialog with N rows, not N dialogs). Stamp a
	// batch id on the context every dispatched call inherits; any call
	// that parks for confirmation carries it onto its ConfirmRequest, so
	// the frontend can render the whole turn as one modal and cancel it
	// as one unit.
	//
	// Stamped here rather than in the chat runner because the runner
	// cannot see turn boundaries — a single run drives many
	// LLM→tool_dispatch→LLM cycles, and a run-scoped batch would glue
	// every turn's confirmations into one ever-growing dialog.
	//
	// Costs nothing when confirmation is not in play: the value is a
	// string on a context that only the confirm path ever reads.
	ctx = toolloop.WithConfirmBatch(ctx, toolloop.NewConfirmID("batch"))

	maxConcurrent := a.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	if maxConcurrent > 16 {
		maxConcurrent = 16
	}

	outcomes := make([]dispatchOutcome, len(calls))
	for i, c := range calls {
		outcomes[i] = dispatchOutcome{call: c}
	}

	// WP07 — mutation-safe concurrency (FR-007):
	// Mutating tools (names present in env.MutatingTools) acquire this
	// mutex before calling env.Tools.Call so they cannot interleave with
	// each other. Read-only tools bypass the lock and parallelize freely
	// up to MaxConcurrent.  nil / empty MutatingTools == all tools are
	// read-only (pre-WP07 behaviour).
	var mutateLock sync.Mutex

	dispatchOne := func(i int) {
		oc := &outcomes[i]
		oc.args, oc.rawArgs = parseToolArgs(oc.call.Arguments)

		// Doom-loop guard (autonomy-recovery-runtime-01PMDL03 WP02):
		// record this (tool, normalized-args) pair against the
		// run-scoped bounded LRU before validation, so a model stuck
		// resending the same malformed arguments to a tool trips the
		// guard too — not just well-formed thrash. This is a
		// *behavioral* cap distinct from MaxToolCallsPerRun: it fires
		// on repeated near-identical calls regardless of remaining
		// budget.
		threshold := env.Budget.DoomLoopThreshold
		if threshold <= 0 {
			threshold = DefaultDoomLoopThreshold
		}
		if repeats := env.State.RecordToolCall(oc.call.Name, oc.args); repeats >= threshold {
			oc.doomLoopHit = true
			oc.doomLoopCount = repeats
			_ = oc.events.AppendKind(env.RunID, node.ID, EventDoomLoopDetected, map[string]any{
				"tool":      oc.call.Name,
				"call_id":   oc.call.ID,
				"repeats":   repeats,
				"threshold": threshold,
			})
		}

		// WP06 — tool-input schema validation (FR-006):
		// Validate args against the tool's input_schema before dispatch.
		// On mismatch, surface a model-readable is_error result so the
		// model can self-correct next turn rather than receiving an opaque
		// tool failure. Also rejects the _raw passthrough for unparseable
		// args — parseToolArgs still produces the _raw map for partial
		// compatibility, but we check for it here and reject it.
		if _, isRaw := oc.args["_raw"]; isRaw {
			errMsg := fmt.Sprintf(
				"Tool %q received non-JSON arguments that could not be parsed as a JSON object. "+
					"Please provide a valid JSON object as arguments.",
				oc.call.Name,
			)
			oc.result = ToolResult{Content: errMsg, IsError: true}
			oc.err = fmt.Errorf("tool_dispatch: %s", errMsg)
			_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err":     errMsg,
				"tool":    oc.call.Name,
				"call_id": oc.call.ID,
			})
			return
		}
		var schemaJSON []byte
		if env.ToolSchemas != nil {
			schemaJSON = env.ToolSchemas[oc.call.Name]
		}
		if msg := validateToolArgs(oc.call.Arguments, oc.call.Name, schemaJSON); msg != "" {
			oc.result = ToolResult{Content: msg, IsError: true}
			oc.err = fmt.Errorf("tool_dispatch: validation: %s", msg)
			_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err":     msg,
				"tool":    oc.call.Name,
				"call_id": oc.call.ID,
			})
			return
		}

		// Cedar tool policy gate (trust-surfaces-that-fire-01PMZ202
		// WP17, UNIT-15). Consults BOTH the coarse Action::"use_tool"
		// family every shipped tool policy actually names and the
		// finer-grained tool_exec sibling — see PolicyGateAdapter.CheckTool.
		// A deny surfaces as a model-visible is_error result (matching
		// the hook-block shape immediately below) rather than failing the
		// whole node, so sibling calls in this fan-out still run. nil
		// env.Policy is the AllowAll boot-stage default.
		if env.Policy != nil {
			if err := env.Policy.CheckTool(ctx, oc.call.Name); err != nil {
				oc.result = ToolResult{Content: "tool denied by policy: " + err.Error(), IsError: true}
				oc.err = err
				_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
					"err":     err.Error(),
					"tool":    oc.call.Name,
					"call_id": oc.call.ID,
					"reason":  "policy_denied",
				})
				return
			}
		}

		// Shared pre-call gate (WP05, tool_invocation.go): greedy-memory
		// hook, canonical EventToolCall, and the v2 pre_tool_use
		// lifecycle hooks. The lifecycle hooks are NEW on this path —
		// before agentgraph-total-convergence-01PMGX01 they only ran on
		// the dead `tool` kind, so a user-configured pre_tool_use hook
		// could not block, rewrite, or annotate any tool call the
		// product actually made.
		tc := toolCallContext{
			NodeID:   node.ID,
			NodeKind: NodeKindToolDispatch,
			CallID:   oc.call.ID,
			ToolName: oc.call.Name,
		}
		var argsJSON json.RawMessage
		var blocked *ToolResult
		var preEvents EventBatch
		var rewritten bool
		oc.args, argsJSON, rewritten, blocked, preEvents = toolPreDispatch(ctx, env, tc, oc.args)
		oc.events.Events = append(oc.events.Events, preEvents.Events...)
		if blocked != nil {
			// A hook denied this call. Surface it as a model-readable
			// is_error result — the sibling calls in this fan-out still
			// run, exactly as they do for a tool that fails.
			oc.result = *blocked
			return
		}
		if rewritten {
			// Keep the post-tool hook payload consistent with what was
			// actually dispatched. Absent a rewrite oc.rawArgs stays the
			// model's own argument string, byte-for-byte.
			oc.rawArgs = string(argsJSON)
		}

		// WP07 — per-call timeout (FR-007):
		// Wrap the dispatch context with a deadline when the caller has
		// configured one.  A zero ToolCallTimeout disables the deadline so
		// the existing behaviour is preserved for callers that do not opt in.
		callCtx := ctx
		var callCancel context.CancelFunc
		if env.ToolCallTimeout > 0 {
			callCtx, callCancel = context.WithTimeout(ctx, env.ToolCallTimeout)
			defer callCancel()
		}

		// WP07 — mutation-safe serialization (FR-007):
		// Acquire the per-Execute mutex when this tool is declared mutating.
		// This ensures that two concurrently dispatched mutating calls never
		// overlap, while read-only calls proceed without blocking.
		isMutating := env.MutatingTools != nil && env.MutatingTools[oc.call.Name]
		if isMutating {
			mutateLock.Lock()
		}

		startedAt := time.Now()
		tr, callErr := env.Tools.Call(callCtx, ToolCall{ID: oc.call.ID, Name: oc.call.Name, Args: oc.args})
		duration := time.Since(startedAt)

		if isMutating {
			mutateLock.Unlock()
		}

		// WP07 — surface timeout as is_error so the model sees a clear
		// failure rather than an opaque context cancellation.
		if callErr != nil && errors.Is(callErr, context.DeadlineExceeded) {
			tr = ToolResult{
				Content: fmt.Sprintf(
					"tool %q timed out after %s — the operation did not complete within the allowed window",
					oc.call.Name, env.ToolCallTimeout,
				),
				IsError: true,
			}
		}

		// FR-010 iteration gate, shared with the tool-node path.
		chargeToolIteration(env, oc.call.Name)

		if callErr != nil {
			// Wrap deny / dispatch failure into an IsError result so the
			// model sees the failure as a tool message; the err return
			// only surfaces for genuine kernel failures (nil pool, etc.).
			// Skip if the timeout handler already set tr (WP07): the
			// timeout is_error is more informative than a raw
			// context.DeadlineExceeded string.
			if !errors.Is(callErr, context.DeadlineExceeded) {
				tr = ToolResult{
					// WP02 (tool-error-legibility-01PMDL02): append a
					// conservative environment-drift hint when the raw
					// error signature-matches a well-known case
					// ("no such file or directory", "permission denied",
					// "not found"). Never rewrites callErr.Error().
					Content: AppendEnvironmentDriftHint(callErr.Error()),
					IsError: true,
				}
			}
			oc.err = callErr
			_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err":     callErr.Error(),
				"tool":    oc.call.Name,
				"call_id": oc.call.ID,
			})
		}
		// WP01 (turn-context-runway-01PMAG03): bound the result at the
		// source. Tool output is the dominant context accumulator in a
		// long turn and truncation is free where compaction costs an LLM
		// call, so the cap is applied here — before the result becomes a
		// Message AND before the post_tool_use hook sees the output —
		// rather than being cleaned up downstream. Results at or under
		// the cap pass through byte-for-byte.
		originalBytes := len(tr.Content)
		capped, elided, handle, archiveErr := capToolOutput(ctx, env, ToolOutputRef{
			RunID:  env.RunID,
			NodeID: node.ID,
			Tool:   oc.call.Name,
			CallID: oc.call.ID,
		}, tr.Content)
		if elided > 0 {
			tr.Content = capped
			logging.L().Info("agentgraph.tool_dispatch.result_truncated",
				"run_id", env.RunID, "node_id", node.ID,
				"tool", oc.call.Name, "call_id", oc.call.ID,
				"bytes_original", originalBytes,
				"bytes_retained", len(capped),
				"bytes_elided", elided,
				"handle", handle,
			)
		}
		if archiveErr != nil {
			// Non-fatal: the truncation still happened, only the
			// re-read handle is missing. Surfaced as an event so the
			// gap is visible in the run trace.
			logging.L().Warn("agentgraph.tool_dispatch.archive_failed",
				"run_id", env.RunID, "tool", oc.call.Name,
				"call_id", oc.call.ID, "err", archiveErr.Error())
		}

		// Shared post-call bookkeeping: EventToolResult + the v2
		// post_tool_use / post_tool_use_failure hooks (also new on this
		// path). The truncation telemetry threads through as extra
		// payload fields so exactly one EventToolResult is emitted
		// (integration of runway WP01 + nodes WP04, 2026-08-12).
		var truncExtra map[string]any
		if elided > 0 {
			truncExtra = map[string]any{
				"truncated":      true,
				"bytes_original": originalBytes,
				"bytes_elided":   elided,
			}
			if handle != "" {
				truncExtra["output_handle"] = handle
			}
			if archiveErr != nil {
				truncExtra["archive_err"] = archiveErr.Error()
			}
		}
		var postEvents EventBatch
		tr, postEvents = toolPostDispatch(ctx, env, tc, argsJSON, tr, callErr, truncExtra)
		oc.events.Events = append(oc.events.Events, postEvents.Events...)
		oc.result = tr

		// Greedy memory hook (post-tool) + new tool-shaped post hook for
		// the chassis-side artifact-output capture.
		if env.Hooks != nil {
			if tr.Content != "" {
				hookBatch := env.Hooks.Fire(ctx, HookPostTool, "session",
					"tool result — "+oc.call.Name, tr.Content, node.ID)
				for _, e := range hookBatch.Events {
					if e.RunID == "" {
						e.RunID = env.RunID
					}
					if e.NodeID == "" {
						e.NodeID = node.ID
					}
					oc.events.Append(e)
				}
			}
			env.Hooks.FirePostToolHooks(ctx, env.SessionID,
				oc.call.Name, oc.rawArgs, tr.Content, duration)
		}
	}

	// safeDispatchOne wraps dispatchOne with panic recovery (FR-001).
	// A panicking tool goroutine yields an is_error ToolResult visible to
	// the model; sibling tool calls in the same fan-out continue and the
	// process does not crash. This mirrors the kernel's safeExecute pattern.
	safeDispatchOne := func(i int) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				oc := &outcomes[i]
				logging.L().Error("agentgraph.tool_dispatch.panic",
					"tool", oc.call.Name,
					"call_id", oc.call.ID,
					"panic", fmt.Sprintf("%v", r),
					"stack", string(stack),
				)
				oc.result = ToolResult{
					Content: fmt.Sprintf("tool panicked: %v", r),
					IsError: true,
				}
				oc.err = fmt.Errorf("tool %q panicked: %v", oc.call.Name, r)
				_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
					"err":     fmt.Sprintf("panic: %v", r),
					"tool":    oc.call.Name,
					"call_id": oc.call.ID,
				})
			}
		}()
		dispatchOne(i)
	}

	if len(calls) > 1 && a.MaxConcurrent != 1 {
		// Default behaviour is parallel; max_concurrent caps the
		// goroutine count via errgroup's SetLimit. Setting
		// max_concurrent=1 forces serial dispatch.
		grp, _ := errgroup.WithContext(ctx)
		grp.SetLimit(maxConcurrent)
		for i := range calls {
			i := i
			grp.Go(func() error {
				safeDispatchOne(i)
				return nil
			})
		}
		_ = grp.Wait()
	} else {
		for i := range calls {
			safeDispatchOne(i)
		}
	}

	// Aggregate outputs + events in original tool_calls order so the
	// model and EventLog projection see stable indexing.
	results := make([]ToolResult, 0, len(outcomes))
	toolMsgs := make([]Message, 0, len(outcomes))
	var doomLoopHits []map[string]any
	for _, oc := range outcomes {
		results = append(results, oc.result)
		toolMsgs = append(toolMsgs, Message{
			Role:       "tool",
			Name:       oc.call.Name,
			ToolCallID: oc.call.ID,
			Content:    oc.result.Content,
			IsError:    oc.result.IsError,
		})
		for _, e := range oc.events.Events {
			res.Events.Append(e)
		}
		if oc.doomLoopHit {
			doomLoopHits = append(doomLoopHits, map[string]any{
				"tool":    oc.call.Name,
				"call_id": oc.call.ID,
				"repeats": oc.doomLoopCount,
			})
		}
	}

	// WP02 — surface the doom-loop signal at node level so a ladder
	// controller can force a replan/escalation regardless of remaining
	// budget. Kept as a plain output rather than an error: the
	// underlying tool calls still ran and their real results still flow
	// to the model this turn — only the *next* turn's routing decision
	// is what should_replan informs.
	//
	// THE CONSUMER EXISTS NOW (agentgraph-total-convergence-01PMGX01
	// WP11c; design in agentic-turn-routing-01PMAG01 §3.7). Both ports
	// carried a deferred-wiring directive from
	// wiring-integrity-01PMAG04 WP05 until then, because nothing read
	// either one: events.go called the intended reader "a future ladder
	// controller" and it was never built, so a model stuck calling the
	// same failing tool kept calling it until the budget ran out
	// (01PMAG01 G4). routerExecutor reads both — `should_replan` to
	// override the model's stated choice with the replan route, and
	// `doom_loop_hits` for the override's audit payload — and
	// chat_default routes that choice into an escalation_ladder. The
	// directives are gone because the ports are read; check-output-
	// ports.sh is what holds that honest.
	if len(doomLoopHits) > 0 {
		res.Outputs["should_replan"] = true
		res.Outputs["doom_loop_hits"] = doomLoopHits
	}

	// Build the messages port to feed the next LLMNode iteration: the
	// upstream model's `response` slice (full history including the
	// assistant tool_use turn) followed by every tool result message.
	// When no upstream response is present (hand-built graphs that
	// directly hand us tool_calls) we fall back to just the tool
	// messages so the executor remains useful in isolation.
	var combined []Message
	if v, ok := inputs.Get("response"); ok {
		switch typed := v.(type) {
		case []Message:
			combined = append(combined, typed...)
		case []any:
			for _, m := range typed {
				if mm, ok := m.(Message); ok {
					combined = append(combined, mm)
				}
			}
		}
	}
	combined = append(combined, toolMsgs...)

	res.Outputs["tool_results"] = results
	res.Outputs["messages"] = combined
	res.Outputs["tool_messages"] = toolMsgs
	res.Outputs["tool_call_count"] = len(results)
	passthroughAssistant(inputs, res.Outputs)
	return res, nil
}

// passthroughAssistant copies the upstream LLMNode's `assistant`
// Message and the conventional `assistant_text` string forward onto the
// dispatch outputs so an outside-loop session_write can pull the
// terminal turn's text directly from the LoopNode-flattened outputs.
// No-op when the upstream did not produce an assistant message.
func passthroughAssistant(in PortValues, out PortValues) {
	if v, ok := in.Get("assistant"); ok {
		out["assistant"] = v
		if m, ok := v.(Message); ok {
			out["assistant_text"] = m.Content
		}
	}
	if v, ok := in.Get("response"); ok {
		out["response"] = v
	}
	if v, ok := in.Get("finish_reason"); ok {
		out["finish_reason"] = v
	}
}

// extractToolCalls reads the tool_calls input port and normalises it
// into a []ToolCallRequest. Accepts the canonical slice shape, an []any
// boxed slice (json round-trip via wire), or nil (empty).
func extractToolCalls(inputs PortValues) ([]ToolCallRequest, error) {
	v, ok := inputs.Get("tool_calls")
	if !ok || v == nil {
		return nil, nil
	}
	switch typed := v.(type) {
	case []ToolCallRequest:
		return append([]ToolCallRequest(nil), typed...), nil
	case []any:
		out := make([]ToolCallRequest, 0, len(typed))
		for i, raw := range typed {
			if tc, ok := raw.(ToolCallRequest); ok {
				out = append(out, tc)
				continue
			}
			// JSON round-tripped shape: re-marshal + decode into the
			// canonical struct.
			buf, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("tool_calls[%d]: marshal: %w", i, err)
			}
			var tc ToolCallRequest
			if err := json.Unmarshal(buf, &tc); err != nil {
				return nil, fmt.Errorf("tool_calls[%d]: decode: %w", i, err)
			}
			out = append(out, tc)
		}
		return out, nil
	}
	return nil, fmt.Errorf("tool_calls: unsupported input type %T", v)
}

// parseToolArgs accepts the model's JSON-encoded argument string and
// returns it as both a typed map (for the kernel ToolRegistry seam) and
// the raw JSON (for the post-tool hook). An unparseable string falls
// back to a single-key map so the tool registry still gets something
// addressable rather than an opaque blob.
func parseToolArgs(raw string) (map[string]any, string) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, "{}"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil && m != nil {
		return m, raw
	}
	// Args couldn't be parsed as a JSON object — surface the raw string
	// under a conventional `_raw` key so the tool's signature can still
	// pull the bytes if it knows to look. The static toolExecutor
	// performs the same fallback semantics implicitly via map override.
	return map[string]any{"_raw": raw}, raw
}
