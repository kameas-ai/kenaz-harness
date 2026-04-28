package agentgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
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

	dispatchOne := func(i int) {
		oc := &outcomes[i]
		oc.args, oc.rawArgs = parseToolArgs(oc.call.Arguments)

		// Pre-tool boundary (mirror toolExecutor). Greedy memory hook +
		// chassis-side mutation extension point.
		if env.Hooks != nil {
			hookBatch := env.Hooks.Fire(ctx, HookPreTool, "session",
				"pre-tool — "+oc.call.Name, summarizeArgs(oc.args), node.ID)
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

		// Emit the canonical EventToolCall for parity with the static
		// tool kind so the EventLog projection sees the same shape
		// regardless of which kind dispatched it.
		_ = oc.events.AppendKind(env.RunID, node.ID, EventToolCall, map[string]any{
			"tool":      oc.call.Name,
			"args":      oc.args,
			"call_id":   oc.call.ID,
			"node_kind": "tool_dispatch",
		})

		startedAt := time.Now()
		tr, callErr := env.Tools.Call(ctx, ToolCall{Name: oc.call.Name, Args: oc.args})
		duration := time.Since(startedAt)

		if env.Counters != nil {
			env.Counters.AddTool()
		}

		if callErr != nil {
			// Wrap deny / dispatch failure into an IsError result so the
			// model sees the failure as a tool message; the err return
			// only surfaces for genuine kernel failures (nil pool, etc.).
			tr = ToolResult{
				Content: callErr.Error(),
				IsError: true,
			}
			oc.err = callErr
			_ = oc.events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
				"err":     callErr.Error(),
				"tool":    oc.call.Name,
				"call_id": oc.call.ID,
			})
		}
		oc.result = tr
		_ = oc.events.AppendKind(env.RunID, node.ID, EventToolResult, map[string]any{
			"tool":     oc.call.Name,
			"call_id":  oc.call.ID,
			"is_error": tr.IsError,
			"bytes":    len(tr.Content),
		})

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

	if len(calls) > 1 && a.MaxConcurrent != 1 {
		// Default behaviour is parallel; max_concurrent caps the
		// goroutine count via errgroup's SetLimit. Setting
		// max_concurrent=1 forces serial dispatch.
		grp, _ := errgroup.WithContext(ctx)
		grp.SetLimit(maxConcurrent)
		for i := range calls {
			i := i
			grp.Go(func() error {
				dispatchOne(i)
				return nil
			})
		}
		_ = grp.Wait()
	} else {
		for i := range calls {
			dispatchOne(i)
		}
	}

	// Aggregate outputs + events in original tool_calls order so the
	// model and EventLog projection see stable indexing.
	results := make([]ToolResult, 0, len(outcomes))
	toolMsgs := make([]Message, 0, len(outcomes))
	for _, oc := range outcomes {
		results = append(results, oc.result)
		toolMsgs = append(toolMsgs, Message{
			Role:    "tool",
			Name:    oc.call.Name,
			Content: oc.result.Content,
		})
		for _, e := range oc.events.Events {
			res.Events.Append(e)
		}
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
