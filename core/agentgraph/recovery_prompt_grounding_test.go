package agentgraph

import (
	"context"
	"strings"
	"testing"
)

// wiring-integrity-01PMAG04 WP00.
//
// escalateExecutor and both escalationLadderExecutor rungs called
// env.LLM.Generate with no SystemPrompt at all, while the other four
// compute executors (model / reflect / review / planner) all compose
// graphBaseOf(env) + the node's own attr.
//
// That inverted the priority exactly: these three call sites fire only
// *after* something has already failed, which is precisely when
// graphBaseOf's TaskState block and the accumulated backtrack
// FailureAnnotations are populated. The recovery path was the one path
// in the harness running without the record of what had already been
// tried and rejected — and the replan rung's own prompt instructs the
// model to "avoid the rejected approach", referencing context it could
// not see.
//
// These tests pin that each site now carries the grounding.

// groundedEnv builds an Env whose graph base + failure annotations are
// both non-empty, so a composed system prompt is distinguishable from
// an empty one.
func groundedEnv(t *testing.T, llm LLMProvider) *Env {
	t.Helper()
	env := &Env{
		RunID: "r",
		LLM:   llm,
		Graph: &Graph{
			ID:           "g",
			SystemPrompt: "GRAPH-BASE-CONSTITUTION",
		},
		Ask: NewMemAskBus(),
	}
	applyEnvDefaults(env)
	env.State.AddFailureAnnotation(FailureAnnotation{
		Node:             "src",
		Reason:           "verdict failed",
		RejectedApproach: "REJECTED-APPROACH-MARKER",
		Iteration:        1,
	})
	return env
}

// assertGrounded checks a captured request carries the graph base and
// the rejected-approach record.
func assertGrounded(t *testing.T, req LLMRequest, site string) {
	t.Helper()
	if req.SystemPrompt == "" {
		t.Fatalf("%s: SystemPrompt is empty — the recovery path is running ungrounded", site)
	}
	if !strings.Contains(req.SystemPrompt, "GRAPH-BASE-CONSTITUTION") {
		t.Errorf("%s: SystemPrompt missing graph base:\n%s", site, req.SystemPrompt)
	}
	if !strings.Contains(req.SystemPrompt, "REJECTED-APPROACH-MARKER") {
		t.Errorf("%s: SystemPrompt missing the FailureAnnotation record — a re-fired node is free to repeat the rejected approach verbatim:\n%s", site, req.SystemPrompt)
	}
}

func TestEscalateExecutor_ComposesGroundedSystemPrompt(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "improved"}}}
	env := groundedEnv(t, llm)

	ex := escalateExecutor{}
	node := &Node{ID: "esc", Kind: NodeKindEscalate, Attrs: EscalateAttrs{
		TargetModel:  "big",
		UpstreamNode: "src",
		SystemPrompt: "NODE-ROLE-PROMPT",
	}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "draft"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("no LLM request captured")
	}
	assertGrounded(t, req, "escalate")
	// EscalateNode extends the compute archetype, so unlike the ladder
	// it also has its own system_prompt attr to layer in.
	if !strings.Contains(req.SystemPrompt, "NODE-ROLE-PROMPT") {
		t.Errorf("escalate: SystemPrompt missing the node's own role prompt:\n%s", req.SystemPrompt)
	}
}

func TestEscalationLadder_EscalateRungComposesGroundedSystemPrompt(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "escalated answer"}}}
	env := groundedEnv(t, llm)

	ex := escalationLadderExecutor{}
	node := &Node{ID: "ladder", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "src",
		TargetModel:  "strong-model",
		PlannerModel: "planner-model",
		MaxRetries:   1,
	}}
	inputs := PortValues{"trigger": "the failing draft"}

	// Fire 1 spends the retry rung (backtrack, no LLM call).
	if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
		t.Fatalf("fire 1 (retry): %v", err)
	}
	// Fire 2 exhausts retries and falls through into the escalate rung.
	if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
		t.Fatalf("fire 2 (escalate): %v", err)
	}
	if llm.callCount() != 1 {
		t.Fatalf("llm calls = %d, want 1 (escalate rung)", llm.callCount())
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("no LLM request captured")
	}
	assertGrounded(t, req, "ladder/escalate")
}

func TestEscalationLadder_ReplanRungComposesGroundedSystemPrompt(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{
		{Content: "escalated answer"},
		{Content: "1. revised step"},
	}}
	env := groundedEnv(t, llm)

	ex := escalationLadderExecutor{}
	node := &Node{ID: "ladder", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "src",
		TargetModel:  "strong-model",
		PlannerModel: "planner-model",
		MaxRetries:   1,
	}}
	inputs := PortValues{"trigger": "the failing draft"}

	// retry -> escalate -> replan.
	for i, phase := range []string{"retry", "escalate", "replan"} {
		if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
			t.Fatalf("fire %d (%s): %v", i+1, phase, err)
		}
	}
	if llm.callCount() != 2 {
		t.Fatalf("llm calls = %d, want 2 (escalate + replan)", llm.callCount())
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("no LLM request captured")
	}
	if req.Model != "planner-model" {
		t.Fatalf("last request model = %q, want planner-model", req.Model)
	}
	assertGrounded(t, req, "ladder/replan")
}
