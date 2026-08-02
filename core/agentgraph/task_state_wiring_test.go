package agentgraph

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// task_state_wiring_test.go exercises the production call sites added in
// autonomy-recovery-runtime-01PMDL03 WP05: Kernel.Run now populates
// TaskState (goal / completed-step trail / forbidden actions) from real
// execution — a first user turn, meaningful node completions, and denied
// policy gates / rejected backtrack approaches — instead of leaving
// SetTaskGoal / AddTaskCompletedStep / AddTaskForbidden dead code.
//
// The single hard requirement threaded through every test here: a run
// that never fails or backtracks must compose a byte-identical system
// prompt to pre-WP05 behavior (TestKernel_ZeroFailureRunComposesByteIdenticalPrompt).
// Every other test below deliberately drives a failure or backtrack
// first, then asserts population — proving the gate (kernel.go's
// recoveryArmed flag) opens exactly when it should and never before.

// fakePolicyDenial is a minimal fake satisfying the unexported
// policyDenialError structural interface (kernel.go) without pulling
// core/policy/cedar into this test package — mirrors how a real
// *cedar.PolicyDeniedError (which gets its DeniedSummary method from
// core/policy/cedar/engine.go) is detected via errors.As in production.
type fakePolicyDenial struct {
	msg     string
	summary string
}

func (e *fakePolicyDenial) Error() string         { return e.msg }
func (e *fakePolicyDenial) DeniedSummary() string { return e.summary }

var _ error = (*fakePolicyDenial)(nil)

// TestKernel_ZeroFailureRunComposesByteIdenticalPrompt is the critical
// regression guard the task requires: a run that never hits a node
// error or an honored backtrack must never call SetTaskGoal /
// AddTaskCompletedStep / AddTaskForbidden, so renderTaskState keeps
// returning "" and the composed SystemPrompt is byte-identical to
// pre-WP05 behavior (composePrompt(graph base, node role) only).
func TestKernel_ZeroFailureRunComposesByteIdenticalPrompt(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "g", SystemPrompt: "BASE",
		Entrypoints: []string{"n"},
		Nodes: []Node{
			{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}},
		},
	}
	k := NewKernel()
	env := &Env{RunID: "zf-run", SessionID: "s", Graph: g, LLM: llm}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	const want = "BASE\n\nROLE"
	if req.SystemPrompt != want {
		t.Errorf("SystemPrompt = %q, want byte-identical %q (a zero-failure run must never add a TaskState block)", req.SystemPrompt, want)
	}

	if got := env.TaskState.Goal(); got != "" {
		t.Errorf("Goal() = %q, want empty on a zero-failure run", got)
	}
	if steps, elided := env.TaskState.CompletedSteps(); len(steps) != 0 || elided != 0 {
		t.Errorf("CompletedSteps() = %v/%d, want none on a zero-failure run", steps, elided)
	}
	if got := env.TaskState.ForbiddenActions(); len(got) != 0 {
		t.Errorf("ForbiddenActions() = %v, want none on a zero-failure run", got)
	}
}

// TestKernel_RecoveryPopulatesGoalStepsAndForbiddenAfterBacktrack drives
// one honored backtrack (fakeBacktrackExecutor, same fixture as
// TestKernel_BacktrackRewindsAndRefires) through a graph with a real
// env.History seam and a downstream model node, then asserts all three
// TaskState dimensions:
//   - Goal populates from the session's first user turn and reaches the
//     composed SystemPrompt.
//   - Forbidden actions pick up the reviewer's RejectedApproach.
//   - Completed steps record only the *post-recovery* completions
//     (draft's rewound re-fire, review's passing re-fire, model's single
//     fire) — draft's original pre-backtrack completion and review's
//     rejected first fire (about to re-fire, not "done") are correctly
//     excluded.
func TestKernel_RecoveryPopulatesGoalStepsAndForbiddenAfterBacktrack(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_taskstate")
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-taskstate-graph", SystemPrompt: "BASE",
		Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "model", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "review", Port: "value"}, To: EndpointRef{Node: "model", Port: "messages"}},
		},
	}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	history := HistoryReaderFunc(func(_ context.Context, sessionID string, _ int) ([]Message, error) {
		if sessionID != "s" {
			return nil, nil
		}
		return []Message{{Role: "user", Content: "Build the widget end to end"}}, nil
	})
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-taskstate-run", SessionID: "s", Graph: g, LLM: llm, History: history}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := env.TaskState.Goal(); got != "Build the widget end to end" {
		t.Errorf("Goal() = %q, want %q", got, "Build the widget end to end")
	}

	req, ok := llm.lastRequest()
	if !ok {
		t.Fatalf("no LLM request captured")
	}
	if !strings.Contains(req.SystemPrompt, "Build the widget end to end") {
		t.Errorf("SystemPrompt = %q, want it to contain the populated goal", req.SystemPrompt)
	}
	if !strings.Contains(req.SystemPrompt, "BASE") || !strings.Contains(req.SystemPrompt, "ROLE") {
		t.Errorf("SystemPrompt = %q, want BASE and ROLE layers still present", req.SystemPrompt)
	}

	forbidden := env.TaskState.ForbiddenActions()
	if len(forbidden) != 1 || forbidden[0] != "v1" {
		t.Errorf("ForbiddenActions() = %v, want [v1] (from the reviewer's RejectedApproach)", forbidden)
	}

	steps, elided := env.TaskState.CompletedSteps()
	if elided != 0 {
		t.Errorf("elided = %d, want 0", elided)
	}
	if len(steps) != 3 {
		t.Fatalf("CompletedSteps() = %v, want exactly 3 entries (draft re-fire, review passing re-fire, model)", steps)
	}
	joined := strings.Join(steps, "|")
	for _, want := range []string{"draft", "review", "model"} {
		if !strings.Contains(joined, want) {
			t.Errorf("CompletedSteps() = %v, want an entry mentioning %q", steps, want)
		}
	}
}

// TestKernel_CompletedStepsCappedAfterManyPostRecoveryCompletions proves
// the completed-steps trail stays bounded (maxTaskCompletedSteps, see
// task_state.go) when a real run's post-recovery completions exceed the
// cap, not just when TaskState.AddCompletedStep is called directly
// (already covered by TestTaskState_CompletedStepsCapped). A single
// backtrack arms recovery, then 25 downstream sink nodes each produce a
// completed-step entry — well past the cap of 20 — so the oldest
// post-recovery entries (draft's and review's re-fires) must be evicted
// in favor of the most recent sink completions.
func TestKernel_CompletedStepsCappedAfterManyPostRecoveryCompletions(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_cap")
	const nSinks = 25
	nodes := []Node{
		{ID: "draft", Kind: kind},
		{ID: "review", Kind: kind},
	}
	edges := []Edge{
		{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
	}
	prev := "review"
	for i := 1; i <= nSinks; i++ {
		id := fmt.Sprintf("s%02d", i)
		nodes = append(nodes, Node{ID: id, Kind: kind})
		edges = append(edges, Edge{From: EndpointRef{Node: prev, Port: "value"}, To: EndpointRef{Node: id, Port: "in"}})
		prev = id
	}
	g := &Graph{SpecVersion: SpecVersion, ID: "bt-cap-graph", Entrypoints: []string{"draft"}, Nodes: nodes, Edges: edges}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-cap-run", Graph: g}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	steps, elided := env.TaskState.CompletedSteps()
	if len(steps) != maxTaskCompletedSteps {
		t.Fatalf("CompletedSteps() len = %d, want %d (cap)", len(steps), maxTaskCompletedSteps)
	}
	// Post-recovery completions: draft's rewound re-fire + review's
	// passing re-fire + nSinks sink completions.
	const wantTotal = 2 + nSinks
	wantElided := wantTotal - maxTaskCompletedSteps
	if elided != wantElided {
		t.Errorf("elided = %d, want %d", elided, wantElided)
	}
	for _, s := range steps {
		if strings.Contains(s, "draft") || strings.Contains(s, "review") {
			t.Errorf("expected draft/review completions to be evicted by the cap, found %q in %v", s, steps)
		}
	}
}

// TestKernel_ForbiddenActionsPopulateFromPolicyDenial exercises the
// other forbidden-actions source: a denied policy gate. readFileExecutor
// (exec_state.go) returns env.Policy.CheckFileRead's error verbatim as
// the node's error; the kernel's node-error branch detects the
// policyDenialError shape via errors.As and records DeniedSummary() as
// a forbidden action.
func TestKernel_ForbiddenActionsPopulateFromPolicyDenial(t *testing.T) {
	t.Parallel()
	denial := &fakePolicyDenial{msg: "policy denied", summary: "Read file:/etc/passwd"}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "policy-deny-graph", Entrypoints: []string{"rf"},
		Nodes: []Node{
			{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: "/etc/passwd"}},
		},
	}
	k := NewKernel()
	env := &Env{RunID: "policy-deny-run", Graph: g, Policy: stubPolicyGate{denyFileRead: denial}}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err == nil {
		t.Fatal("expected Run to fail on a denied file read")
	}

	forbidden := env.TaskState.ForbiddenActions()
	if len(forbidden) != 1 || forbidden[0] != "Read file:/etc/passwd" {
		t.Errorf("ForbiddenActions() = %v, want [%q]", forbidden, "Read file:/etc/passwd")
	}
}

// TestKernel_AutoPopulatedTaskStateSurvivesRebuildState is the
// RebuildState half of the WP05 contract: it isn't enough that a live
// Run's dispatch loop calls SetTaskGoal / AddTaskCompletedStep /
// AddTaskForbidden — those calls must go through the same durable
// event-sourced path as their pre-existing manually-invoked siblings
// (TestKernel_TaskStateSurvivesRebuildState) so a resumed run replays
// them. Drives the same backtrack + history scenario as
// TestKernel_RecoveryPopulatesGoalStepsAndForbiddenAfterBacktrack, then
// rebuilds a fresh Env from the event log and asserts all three
// dimensions replay identically.
func TestKernel_AutoPopulatedTaskStateSurvivesRebuildState(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_taskstate_rebuild")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-taskstate-rebuild-graph",
		Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "sink", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "review", Port: "value"}, To: EndpointRef{Node: "sink", Port: "in"}},
		},
	}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	history := HistoryReaderFunc(func(_ context.Context, sessionID string, _ int) ([]Message, error) {
		if sessionID != "s" {
			return nil, nil
		}
		return []Message{{Role: "user", Content: "Ship the widget"}}, nil
	})
	log := NewMemoryEventLog()
	k := NewKernel(WithExecutor(ex), WithEventLog(log))
	env := &Env{RunID: "bt-taskstate-rebuild-run", SessionID: "s", Graph: g, History: history}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	origGoal := env.TaskState.Goal()
	origForbidden := env.TaskState.ForbiddenActions()
	origSteps, origElided := env.TaskState.CompletedSteps()
	if origGoal == "" || len(origForbidden) == 0 || len(origSteps) == 0 {
		t.Fatalf("expected recovery to have populated TaskState before rebuild: goal=%q forbidden=%v steps=%v",
			origGoal, origForbidden, origSteps)
	}

	env2 := &Env{RunID: "bt-taskstate-rebuild-run", Graph: g}
	applyEnvDefaults(env2)
	if err := k.RebuildState(env2); err != nil {
		t.Fatalf("RebuildState: %v", err)
	}

	if got := env2.TaskState.Goal(); got != origGoal {
		t.Errorf("rebuilt Goal() = %q, want %q", got, origGoal)
	}
	rebuiltSteps, rebuiltElided := env2.TaskState.CompletedSteps()
	if rebuiltElided != origElided || len(rebuiltSteps) != len(origSteps) {
		t.Errorf("rebuilt CompletedSteps() = %v/%d, want %v/%d", rebuiltSteps, rebuiltElided, origSteps, origElided)
	}
	for i := range origSteps {
		if i >= len(rebuiltSteps) || rebuiltSteps[i] != origSteps[i] {
			t.Errorf("rebuilt CompletedSteps()[%d] = %v, want %v", i, rebuiltSteps, origSteps)
			break
		}
	}
	rebuiltForbidden := env2.TaskState.ForbiddenActions()
	if len(rebuiltForbidden) != len(origForbidden) {
		t.Errorf("rebuilt ForbiddenActions() = %v, want %v", rebuiltForbidden, origForbidden)
	} else {
		for i := range origForbidden {
			if rebuiltForbidden[i] != origForbidden[i] {
				t.Errorf("rebuilt ForbiddenActions() = %v, want %v", rebuiltForbidden, origForbidden)
				break
			}
		}
	}
}
