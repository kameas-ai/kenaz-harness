// graphrun.go — bridges a task.start RPC onto a real core/ agentgraph run.
//
// Phase 8 (vm-rpc.md): task.start{prompt} is routed to the harness graph
// kernel as an `agent.task` invocation. This file builds the graph, runs it
// through the real agentgraph.Kernel, streams each node's output back to the
// client as task.running chunks, and drives the audit sink (via nodeTracer)
// off the kernel's per-node TraceSink hook.
//
// Why a transform graph (not an LLM graph): the in-VM service must run with no
// network and no provider credentials in CI and on the operator smoke. A chain
// of transform nodes exercises the SAME kernel traversal, event log, and
// TraceSink hook points that an LLM graph would — the audit timeline and the
// RPC stream are shaped identically — while staying deterministic and offline.
// Swapping in LLMNode kinds later is an env-wiring change here, not a protocol
// change: the wire contract (task.running / task.complete) and the audit
// contract (task.tool_call / task.tool_result) are unchanged.
//
// The two graph steps each honour ctx with a short delay so a task.cancel
// arriving mid-run surfaces as a cancelled run (the kernel passes ctx into the
// transform fn; a context-cancelled transform returns an error that aborts the
// run, which runTask maps onto task.cancelled when ctx.Err() != nil).
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

// stepDelay is the per-node cooperative delay. It is small enough not to slow
// real use meaningfully, but large enough that a task.cancel issued shortly
// after task.start lands before the run finishes (cancel_test.go).
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
// `plan` surfaces the prompt into the run; `run` is a stand-in for the agent
// step. Each node fire produces a tool_call + tool_result audit pair and a
// task.running chunk, so a real traversal yields the full lifecycle the host
// audit timeline expects.
func runAgentTaskGraph(
	ctx context.Context,
	taskID string,
	prompt string,
	tracer agentgraph.TraceSink,
	onChunk graphChunkSink,
) error {
	g := &agentgraph.Graph{
		SpecVersion: agentgraph.SpecVersion,
		ID:          "agent.task",
		Entrypoints: []string{stepPlanID},
		Nodes: []agentgraph.Node{
			{
				ID:   stepPlanID,
				Kind: agentgraph.NodeKindTransform,
				Attrs: agentgraph.TransformAttrs{
					Name:   transformPlan,
					Params: map[string]any{"prompt": prompt},
				},
			},
			{
				ID:    stepRunID,
				Kind:  agentgraph.NodeKindTransform,
				Attrs: agentgraph.TransformAttrs{Name: transformRun},
			},
		},
		Edges: []agentgraph.Edge{
			{
				From: agentgraph.EndpointRef{Node: stepPlanID, Port: "out"},
				To:   agentgraph.EndpointRef{Node: stepRunID, Port: "in"},
			},
		},
	}

	// Register the agent.task step transforms onto a registry seeded with the
	// builtins. Pre-setting env.Transforms means applyEnvDefaults (called
	// inside Run) keeps it rather than installing a fresh builtins-only one.
	transforms := agentgraph.NewTransformRegistry()
	agentgraph.BuiltinTransforms(transforms)
	transforms.Register(transformPlan, planTransform)
	transforms.Register(transformRun, runTransform)

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
	// topological order. This is the live RPC surface; it carries content and
	// goes ONLY to the connected client.
	for _, nodeID := range []string{stepPlanID, stepRunID} {
		out := env.State.Outputs(nodeID)
		if v, ok := out["out"]; ok {
			if s := renderChunk(v); s != "" {
				onChunk(nodeKindOf(g, nodeID), s)
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

// runTransform is the agent step stand-in. It echoes its input upward (a real
// LLM/tool step replaces this later) and honours ctx for cancellation.
func runTransform(ctx context.Context, in agentgraph.PortValues, _ map[string]any) (agentgraph.PortValues, error) {
	if err := cooperativeDelay(ctx); err != nil {
		return nil, err
	}
	s, _ := in["in"].(string)
	return agentgraph.PortValues{"out": s}, nil
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

// nodeKindOf returns the structural node kind for nodeID, or "" if absent.
func nodeKindOf(g *agentgraph.Graph, nodeID string) string {
	for i := range g.Nodes {
		if g.Nodes[i].ID == nodeID {
			return string(g.Nodes[i].Kind)
		}
	}
	return ""
}
