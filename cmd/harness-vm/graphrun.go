// graphrun.go — bridges a task.start RPC onto a real core/ agentgraph run.
//
// Phase 8 (vm-rpc.md): task.start{prompt} is routed to the harness graph
// kernel as an `agent.task` invocation. This file builds the graph, runs it
// through the real agentgraph.Kernel, streams each node's output back to the
// client as task.running chunks, and drives the audit sink (via nodeTracer)
// off the kernel's per-node TraceSink hook.
//
// The agent step (the `run` node) executes through an agentExecutor
// (agentexec.go): a REAL core/llm-backed model call by default, or the
// deterministic offline echo when the process was started with
// KENAZ_AGENT_EXEC=stub (CI and the operator smoke, which run with no network
// and no provider credentials). Both executors drive the SAME kernel
// traversal, event log, and TraceSink hook points — the audit timeline and
// the RPC stream are shaped identically — so swapping executors is an
// env-wiring change here, not a protocol change: the wire contract
// (task.running / task.complete) and the audit contract (task.tool_call /
// task.tool_result) are unchanged (Spec 058 FR-001/FR-002).
//
// Every graph step honours ctx so a task.cancel arriving mid-run surfaces as
// a cancelled run: the kernel passes ctx into the transform fn — the stub via
// its cooperative delay, the real executor via context propagation into the
// in-flight provider call (FR-006). A context-cancelled transform returns an
// error that aborts the run, which runTask maps onto task.cancelled when
// ctx.Err() != nil.
//
// PRIVACY: the prompt text flows into the graph's run state (it is the user's
// task), but it NEVER reaches the audit sink — the kernel passes only structural
// attrs (node_id, run_id) to TraceSink, and graphrun emits task.running chunks
// only over the live RPC connection, never to the audit socket. The audit path
// stays metadata-only by construction.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// graphChunkSink receives each node's terminal string output so the caller can
// forward it as a task.running chunk. node is the graph node kind (structural);
// text is the node's "out" port rendered as a string (content — RPC stream
// only, never the audit socket).
type graphChunkSink func(node, text string)

// stepDelay is the per-node cooperative delay used by planTransform and the
// stub executor. It is small enough not to slow real use meaningfully, but
// large enough that a task.cancel issued shortly after task.start lands
// before the run finishes (cancel_test.go).
const stepDelay = 25 * time.Millisecond

// graph step / node identifiers. Structural strings — these are what the audit
// sink records as the `tool` field; they never carry prompt text or output.
const (
	stepPlanID = "plan"
	stepRunID  = "run"

	transformPlan = "agent.plan"
	transformRun  = "agent.run"
)

// runAgentTaskGraph builds and runs the agent.task graph for prompt, invoking
// onChunk for each node that produces a string "out" value. It returns the
// graph run error (nil on success; context.Canceled-derived error on cancel).
// The audit lifecycle (tool_call / tool_result per node) is emitted by tracer,
// which the kernel drives via the TraceSink seam.
//
// The graph is a two-node chain:
//
//	plan (transform:agent.plan)  →  run (transform:agent.run)
//
// `plan` surfaces the prompt into the run; `run` executes the agent step
// through exec (real model call, or the explicit stub echo). Each node fire
// produces a tool_call + tool_result audit pair and a task.running chunk, so
// a traversal yields the full lifecycle the host audit timeline expects.
func runAgentTaskGraph(
	ctx context.Context,
	taskID string,
	prompt string,
	exec agentExecutor,
	tracer agentgraph.TraceSink,
	onChunk graphChunkSink,
) error {
	// The default two-node plan→run chain. The chunk label is the node Kind
	// ("transform"), preserving the pre-run-params behaviour byte-for-byte.
	kindLabel := string(agentgraph.NodeKindTransform)
	steps := []graphStep{
		{id: stepPlanID, label: kindLabel, transform: transformPlan, params: map[string]any{"prompt": prompt}},
		{id: stepRunID, label: kindLabel, transform: transformRun},
	}
	return runGraphSteps(ctx, taskID, steps, exec, tracer, onChunk)
}

// runAgentPresetGraph drives the run from a resolved workflow-preset step
// sequence (Spec 056 AC-5). Each preset step becomes one graph node whose chunk
// label is the STEP NAME (structural — a fixed catalog identifier, never user
// content), so the preset's node sequence is observable on the ledger trail.
// The first node surfaces the prompt; each subsequent node runs the agent
// executor on its upstream input (its step label passed as structural
// context), exercising the same kernel traversal / TraceSink hook the default
// graph does. A mid-chain step failure aborts the run, halting downstream
// nodes (Spec 058 edge case).
func runAgentPresetGraph(
	ctx context.Context,
	taskID string,
	prompt string,
	presetSteps []string,
	exec agentExecutor,
	tracer agentgraph.TraceSink,
	onChunk graphChunkSink,
) error {
	if len(presetSteps) == 0 {
		return runAgentTaskGraph(ctx, taskID, prompt, exec, tracer, onChunk)
	}
	steps := make([]graphStep, 0, len(presetSteps))
	for i, name := range presetSteps {
		st := graphStep{id: fmt.Sprintf("s%d", i), label: name, transform: transformRun}
		if i == 0 {
			st.transform = transformPlan
			st.params = map[string]any{"prompt": prompt}
		} else {
			// The step label rides along as structural context for the
			// executor (a fixed catalog id — never user content).
			st.params = map[string]any{"step": name}
		}
		steps = append(steps, st)
	}
	return runGraphSteps(ctx, taskID, steps, exec, tracer, onChunk)
}

// graphStep is one node in the linear agent.task chain: a graph node id, the
// chunk label forwarded to onChunk (and thus the ledger tool_call `tool`
// value), the registered transform to fire, and its params.
type graphStep struct {
	id        string
	label     string
	transform string
	params    map[string]any
}

// runGraphSteps builds a linear transform graph from steps, runs it through the
// real agentgraph.Kernel, and streams each node's terminal "out" value back as
// a task.running chunk in declared order. It is the shared core behind both the
// default and preset graphs — the ONLY difference between them is the step list.
func runGraphSteps(
	ctx context.Context,
	taskID string,
	steps []graphStep,
	exec agentExecutor,
	tracer agentgraph.TraceSink,
	onChunk graphChunkSink,
) error {
	if len(steps) == 0 {
		return nil
	}

	nodes := make([]agentgraph.Node, len(steps))
	var edges []agentgraph.Edge
	for i, st := range steps {
		nodes[i] = agentgraph.Node{
			ID:    st.id,
			Kind:  agentgraph.NodeKindTransform,
			Attrs: agentgraph.TransformAttrs{Name: st.transform, Params: st.params},
		}
		if i > 0 {
			edges = append(edges, agentgraph.Edge{
				From: agentgraph.EndpointRef{Node: steps[i-1].id, Port: "out"},
				To:   agentgraph.EndpointRef{Node: st.id, Port: "in"},
			})
		}
	}

	g := &agentgraph.Graph{
		SpecVersion: agentgraph.SpecVersion,
		ID:          "agent.task",
		Entrypoints: []string{steps[0].id},
		Nodes:       nodes,
		Edges:       edges,
	}

	// Register the agent.task step transforms onto a registry seeded with the
	// builtins. Pre-setting env.Transforms means applyEnvDefaults (called
	// inside Run) keeps it rather than installing a fresh builtins-only one.
	transforms := agentgraph.NewTransformRegistry()
	agentgraph.BuiltinTransforms(transforms)
	transforms.Register(transformPlan, planTransform)
	transforms.Register(transformRun, makeRunTransform(exec))

	env := &agentgraph.Env{
		RunID:      taskID,
		SessionID:  taskID,
		Graph:      g,
		Transforms: transforms,
		Trace:      tracer, // audit tool_call / tool_result hook
		State:      agentgraph.NewRunState(),
	}

	k := agentgraph.NewKernel()
	if err := k.Run(ctx, env); err != nil {
		return err
	}

	// Stream each node's terminal "out" value as a task.running chunk, in
	// declared order. This is the live RPC surface; it carries content and goes
	// ONLY to the connected client.
	for _, st := range steps {
		out := env.State.Outputs(st.id)
		if v, ok := out["out"]; ok {
			if s := renderChunk(v); s != "" {
				onChunk(st.label, s)
			}
		}
	}
	return nil
}

// planTransform surfaces the prompt into the run. It reads params["prompt"]
// (the user's task — run content, never audited) and emits it on "out". It
// honours ctx so a cancel during the plan step aborts the run.
func planTransform(ctx context.Context, _ agentgraph.PortValues, params map[string]any) (agentgraph.PortValues, error) {
	if err := cooperativeDelay(ctx); err != nil {
		return nil, err
	}
	prompt, _ := params["prompt"].(string)
	return agentgraph.PortValues{"out": prompt}, nil
}

// makeRunTransform binds exec into the agent step's transform. The node's
// upstream "in" value (the prompt, or the previous preset step's output) is
// handed to the executor; the executor's text lands on "out" and is emitted
// as a task.running chunk at node completion. Cancellation propagates through
// ctx into the executor (and, for the real executor, into the in-flight
// provider call — Spec 058 FR-006). An executor error aborts the run, which
// runTask maps onto the task errored state — never a silent echo (FR-003/004).
func makeRunTransform(exec agentExecutor) func(context.Context, agentgraph.PortValues, map[string]any) (agentgraph.PortValues, error) {
	return func(ctx context.Context, in agentgraph.PortValues, params map[string]any) (agentgraph.PortValues, error) {
		s, _ := in["in"].(string)
		step, _ := params["step"].(string)
		out, err := exec.Generate(ctx, step, s)
		if err != nil {
			return nil, err
		}
		return agentgraph.PortValues{"out": out}, nil
	}
}

// cooperativeDelay waits stepDelay or returns ctx.Err() if the run is cancelled
// first. This is what makes a mid-run task.cancel observable.
func cooperativeDelay(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(stepDelay):
		return nil
	}
}

// renderChunk converts a node "out" value to a display string. The value is
// already RPC-stream content; this only normalises its rendering.
func renderChunk(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}
