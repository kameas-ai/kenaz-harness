package agentgraph

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
	// model_used moved off the Outputs port onto EventEscalateTriggered's
	// target_model field (wiring-integrity-01PMAG04 WP03).
	assertEventFieldString(t, r, EventEscalateTriggered, "target_model", "strong-model")
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
	if r1.Outputs["rung"] != "escalate" {
		t.Fatalf("node1: unexpected outputs %+v", r1.Outputs)
	}
	assertEventFieldString(t, r1, EventEscalateTriggered, "target_model", "strong1")

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

// TestEscalationLadderExecutor_GlobalEscalationClaimIsAtomicUnderConcurrency
// is the regression test for the review-flagged TOCTOU: the escalate
// rung used to read env.State.Outputs(ladderGlobalEscalatedKey), run a
// slow env.LLM.Generate call with no lock held, and only then write
// the flag back — leaving a window in which two concurrent
// EscalationLadder nodes could both observe "not yet escalated" and
// both fire a real, costed escalation. Unlike the sequential
// TestEscalationLadderExecutor_GlobalEscalationSharedAcrossNodes test
// above, this drives two nodes' Execute calls from actual concurrent
// goroutines (release via a shared start gate so both attempt the
// claim at the same instant) and repeats it to make a reintroduced
// race likely to surface even without -race, though -race is what
// definitively proves RunState.CompareAndSetOnce itself is race-free.
func TestEscalationLadderExecutor_GlobalEscalationClaimIsAtomicUnderConcurrency(t *testing.T) {
	t.Parallel()
	const iterations = 50
	for i := 0; i < iterations; i++ {
		llm := &stubLLM{}
		env := &Env{RunID: "r", LLM: llm}
		applyEnvDefaults(env)

		ex := escalationLadderExecutor{}
		node1 := &Node{ID: "ladder1", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
			UpstreamNode: "src1", TargetModel: "strong1", MaxRetries: 1,
		}}
		node2 := &Node{ID: "ladder2", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
			UpstreamNode: "src2", TargetModel: "strong2", MaxRetries: 1,
		}}
		env.State.SetOutputs(ladderStateKey(node1.ID), PortValues{"rung": ladderRungEscalate, "retry_attempts": 1})
		env.State.SetOutputs(ladderStateKey(node2.ID), PortValues{"rung": ladderRungEscalate, "retry_attempts": 1})

		type outcome struct {
			rung string
			err  error
		}
		results := make(chan outcome, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, n := range []*Node{node1, node2} {
			n := n
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r, err := ex.Execute(context.Background(), env, n, PortValues{"trigger": "draft"})
				rung, _ := r.Outputs["rung"].(string)
				results <- outcome{rung: rung, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		var escalateCount, replanCount int
		for o := range results {
			if o.err != nil {
				t.Fatalf("iteration %d: Execute error: %v", i, o.err)
			}
			switch o.rung {
			case "escalate":
				escalateCount++
			case "replan":
				replanCount++
			default:
				t.Fatalf("iteration %d: unexpected rung %q", i, o.rung)
			}
		}
		// Exactly one goroutine must win the claim (rung="escalate",
		// calling Generate at its own TargetModel) and the other must
		// lose it and fall through to the replan rung (rung="replan",
		// calling Generate at the planner model) — same fallthrough
		// behavior TestEscalationLadderExecutor_GlobalEscalationSharedAcrossNodes
		// exercises sequentially above. A non-atomic claim would let
		// both goroutines win, producing escalateCount==2 with two
		// distinct strong-model calls instead of one.
		if escalateCount != 1 || replanCount != 1 {
			t.Fatalf("iteration %d: escalateCount=%d replanCount=%d, want exactly one of each (the claim must be exclusive)",
				i, escalateCount, replanCount)
		}
		if got := llm.callCount(); got != 2 {
			t.Fatalf("iteration %d: llm calls = %d, want exactly 2 (1 escalate + 1 replan fallthrough) — "+
				"a non-atomic claim would double-escalate instead", i, got)
		}
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

// assertEventFieldString asserts that some event of the given kind carries
// a string field equal to want. Used to verify a value moved off an
// Outputs port and onto an event payload still reaches an observer
// (wiring-integrity-01PMAG04 WP03).
func assertEventFieldString(t *testing.T, r Result, kind EventKind, field, want string) {
	t.Helper()
	for _, e := range r.Events.Events {
		if e.Kind != kind {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v", kind, err)
		}
		if v, _ := payload[field].(string); v == want {
			return
		}
	}
	t.Fatalf("no %s event with %s=%q found among %d events", kind, field, want, len(r.Events.Events))
}

// TestEscalationLadder_AskRungWithheldUnderAskPolicy is the ladder's
// half of the autonomy F7 finding (agentgraph-total-convergence-
// 01PMGX01 WP11c).
//
// The terminal rung asks a human. At askOnAmbiguity proceed/never the
// chat surface already withholds the ask tool from the model, so a
// ladder that pauses the run with a question anyway would honour the
// dial for the model and ignore it for the runtime — one control
// meaning two things, which is the defect class this campaign exists to
// delete. Under Withhold the ladder ends at replan instead.
func TestEscalationLadder_AskRungWithheldUnderAskPolicy(t *testing.T) {
	t.Parallel()
	bus := NewMemAskBus()
	env := &Env{RunID: "r", LLM: &stubLLM{}, Ask: bus, AskPolicy: AskPolicyWithhold}
	applyEnvDefaults(env)

	ex := escalationLadderExecutor{}
	node := &Node{ID: "recover", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "agent_loop", TargetModel: "strong", AskQuestion: "proceed?", MaxRetries: 1,
	}}
	// Jump straight to the ask rung — the earlier rungs are covered by
	// TestEscalationLadderExecutor_FullProgression.
	env.State.SetOutputs(ladderStateKey(node.ID), PortValues{"rung": ladderRungAsk, "retry_attempts": 1})

	r, err := ex.Execute(context.Background(), env, node, PortValues{"trigger": "the failing draft"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Pause {
		t.Error("the ladder paused the run to ask a question the user pre-declined")
	}
	for _, e := range r.Events.Events {
		if e.Kind == EventAskPending {
			t.Error("ask_pending emitted while questions are withheld")
		}
	}
	if got, _ := r.Outputs["rung"].(string); got != "exhausted" {
		t.Errorf("rung = %q, want %q", got, "exhausted")
	}
	// The suppression is still on the audit trail: the ladder reached
	// its end, it did not silently stop one rung short.
	var sawSkipped bool
	for _, e := range r.Events.Events {
		if e.Kind == EventLadderRung && strings.Contains(string(e.Payload), `"skipped":true`) {
			sawSkipped = true
		}
	}
	if !sawSkipped {
		t.Error("no escalation_ladder_rung event recording the skipped ask rung")
	}
}

// TestEscalationLadder_AskRungRunsUnderTheDefaultPolicy is the control:
// the default posture is untouched, the ladder still asks.
func TestEscalationLadder_AskRungRunsUnderTheDefaultPolicy(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", LLM: &stubLLM{}, Ask: NewMemAskBus()}
	applyEnvDefaults(env)

	node := &Node{ID: "recover", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
		UpstreamNode: "agent_loop", TargetModel: "strong", AskQuestion: "proceed?", MaxRetries: 1,
	}}
	env.State.SetOutputs(ladderStateKey(node.ID), PortValues{"rung": ladderRungAsk, "retry_attempts": 1})

	r, err := escalationLadderExecutor{}.Execute(context.Background(), env, node, PortValues{"trigger": "d"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !r.Pause {
		t.Error("the ladder did not pause to ask under the default ask policy")
	}
	assertHasEvent(t, r, EventAskPending)
}
