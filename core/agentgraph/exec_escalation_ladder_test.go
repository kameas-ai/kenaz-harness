package agentgraph

import (
	"context"
	"encoding/json"
	"testing"
)

// TestEscalationLadderExecutor_FullProgression drives one EscalationLadder
// node through every rung end-to-end (autonomy-recovery-runtime-01PMDL03
// WP04): retry (exhausted after MaxRetries) -> escalate -> replan -> ask,
// then asserts a further fire errors once every rung is spent.
func TestEscalationLadderExecutor_FullProgression(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{
		{Content: "escalated answer"},
		{Content: "1. revised step"},
	}}
	bus := NewMemAskBus()
	env := &Env{RunID: "r", LLM: llm, Ask: bus}
	applyEnvDefaults(env)

	ex := escalationLadderExecutor{}
	node := &Node{ID: "ladder", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "src",
		TargetModel:  "strong-model",
		PlannerModel: "planner-model",
		AskQuestion:  "proceed?",
		MaxRetries:   1,
	}}
	inputs := PortValues{"trigger": "the failing draft"}

	// Fire 1: rung 0 (retry), attempt 1 <= MaxRetries(1) -> backtrack.
	r, err := ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("fire 1 (retry): %v", err)
	}
	if r.Backtrack == nil || r.Backtrack.TargetNode != "src" {
		t.Fatalf("fire 1: expected backtrack to %q, got %+v", "src", r.Backtrack)
	}
	assertLadderRungEvent(t, r, "retry")

	// Fire 2: rung 0 again, attempt 2 > MaxRetries(1) -> retries exhausted,
	// falls through into the escalate rung in the same call.
	r, err = ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("fire 2 (escalate): %v", err)
	}
	if r.Backtrack != nil {
		t.Fatalf("fire 2: unexpected backtrack %+v", r.Backtrack)
	}
	if r.Outputs["rung"] != "escalate" {
		t.Fatalf("fire 2: rung = %v, want escalate", r.Outputs["rung"])
	}
	if r.Outputs["model_used"] != "strong-model" {
		t.Fatalf("fire 2: model_used = %v, want strong-model", r.Outputs["model_used"])
	}
	if llm.callCount() != 1 {
		t.Fatalf("fire 2: llm calls = %d, want 1", llm.callCount())
	}
	if req, ok := llm.lastRequest(); !ok || req.Model != "strong-model" {
		t.Fatalf("fire 2: last request model = %+v, want strong-model", req)
	}
	assertLadderRungEvent(t, r, "escalate")
	assertHasEvent(t, r, EventEscalateTriggered)

	// Fire 3: rung 2 (replan).
	r, err = ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("fire 3 (replan): %v", err)
	}
	if r.Outputs["rung"] != "replan" {
		t.Fatalf("fire 3: rung = %v, want replan", r.Outputs["rung"])
	}
	if llm.callCount() != 2 {
		t.Fatalf("fire 3: llm calls = %d, want 2", llm.callCount())
	}
	if req, ok := llm.lastRequest(); !ok || req.Model != "planner-model" {
		t.Fatalf("fire 3: last request model = %+v, want planner-model", req)
	}
	assertLadderRungEvent(t, r, "replan")
	assertHasEvent(t, r, EventPlanCreated)

	// Fire 4: rung 3 (ask), no answer yet -> pause.
	r, err = ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("fire 4 (ask/pending): %v", err)
	}
	if !r.Pause {
		t.Fatalf("fire 4: expected pause")
	}
	if q, ok := bus.PendingQuestion("r", "ladder"); !ok || q != "proceed?" {
		t.Fatalf("fire 4: pending question = %q (%v), want %q", q, ok, "proceed?")
	}
	assertLadderRungEvent(t, r, "ask")
	assertHasEvent(t, r, EventAskPending)

	// Resume: answer posted, fire 5 (rung 3, ask) resolves.
	bus.Answer("r", "ladder", "go ahead with plan B")
	r, err = ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("fire 5 (ask/answered): %v", err)
	}
	if r.Pause {
		t.Fatalf("fire 5: should not pause once answered")
	}
	msgs, ok := r.Outputs["result"].([]Message)
	if !ok || len(msgs) == 0 || msgs[0].Content != "go ahead with plan B" {
		t.Fatalf("fire 5: result = %+v", r.Outputs["result"])
	}
	assertHasEvent(t, r, EventAskAnswered)

	// Fire 6: every rung is now spent -> hard error.
	if _, err := ex.Execute(context.Background(), env, node, inputs); err == nil {
		t.Fatalf("fire 6: expected error once all rungs are exhausted")
	}
}

// TestEscalationLadderExecutor_GlobalEscalationSharedAcrossNodes asserts
// the "one global escalation counter" requirement (generalising
// EscalateAttrs.OneEscalationOnly from per-node to per-run): once any
// EscalationLadder node in the run has spent the escalate rung, every
// other ladder node skips straight past it to replan without another
// LLMProvider.Generate call at the stronger model.
func TestEscalationLadderExecutor_GlobalEscalationSharedAcrossNodes(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{
		{Content: "escalated from node1"},
		{Content: "replanned for node2"},
	}}
	env := &Env{RunID: "r", LLM: llm}
	applyEnvDefaults(env)

	ex := escalationLadderExecutor{}
	node1 := &Node{ID: "ladder1", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "src1", TargetModel: "strong1", MaxRetries: 1,
	}}
	node2 := &Node{ID: "ladder2", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "src2", TargetModel: "strong2", MaxRetries: 1,
	}}

	// Drive node1 straight to (and through) the escalate rung.
	env.State.SetOutputs(ladderStateKey(node1.ID), PortValues{"rung": ladderRungEscalate, "retry_attempts": 1})
	r1, err := ex.Execute(context.Background(), env, node1, PortValues{"trigger": "d1"})
	if err != nil {
		t.Fatalf("node1 escalate: %v", err)
	}
	if r1.Outputs["rung"] != "escalate" || r1.Outputs["model_used"] != "strong1" {
		t.Fatalf("node1: unexpected outputs %+v", r1.Outputs)
	}

	// Drive node2 to the escalate rung too — the global flag node1 set
	// must make node2 skip straight to replan instead of calling
	// LLMProvider.Generate against strong2.
	env.State.SetOutputs(ladderStateKey(node2.ID), PortValues{"rung": ladderRungEscalate, "retry_attempts": 1})
	r2, err := ex.Execute(context.Background(), env, node2, PortValues{"trigger": "d2"})
	if err != nil {
		t.Fatalf("node2 (should skip to replan): %v", err)
	}
	if r2.Outputs["rung"] != "replan" {
		t.Fatalf("node2: rung = %v, want replan (escalate already spent globally)", r2.Outputs["rung"])
	}
	if llm.callCount() != 2 {
		t.Fatalf("total llm calls = %d, want 2 (1 escalate + 1 replan, never 2 escalates)", llm.callCount())
	}
	if req, ok := llm.lastRequest(); !ok || req.Model == "strong2" {
		t.Fatalf("node2 must not call LLMProvider.Generate against strong2; last request = %+v", req)
	}
}

func assertLadderRungEvent(t *testing.T, r Result, wantRung string) {
	t.Helper()
	for _, e := range r.Events.Events {
		if e.Kind != EventLadderRung {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode EventLadderRung payload: %v", err)
		}
		if payload["rung"] == wantRung {
			return
		}
	}
	t.Fatalf("no EventLadderRung with rung=%q found among %d events", wantRung, len(r.Events.Events))
}

func assertHasEvent(t *testing.T, r Result, kind EventKind) {
	t.Helper()
	for _, e := range r.Events.Events {
		if e.Kind == kind {
			return
		}
	}
	t.Fatalf("expected event kind %q, got none among %d events", kind, len(r.Events.Events))
}
