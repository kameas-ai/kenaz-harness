package agentgraph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
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
// prompt to pre-WP05 behavior (TestKernel_ZeroFailureRunComposesByteIdenticalPrompt)
// — at the DEFAULT arming policy. WP11b added a second policy
// (Env.TaskStateArming = TaskStateArmAlways) for the verified-exit
// path, pinned by its own block at the bottom of this file.
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
//
// THE INVARIANT BECAME CONDITIONAL IN WP11b — read this before
// "fixing" a failure here (agentgraph-total-convergence-01PMGX01;
// design in agentic-turn-routing-01PMAG01 §3.5).
//
// §3.5 called for retiring this test outright, on the grounds that
// FR-002 needs a goal on every run: a run that succeeded all the way to
// an exit gate has by construction never armed recovery, so the gate
// would be checking the answer against nothing (01PMAG01 G5).
//
// It was NOT retired, because the trade turned out to be avoidable.
// Arming is now a policy on Env (TaskStateArming), so the guarantee
// this test pins survives intact at the DEFAULT — every Kernel.Run
// caller that has not opted in, which is all of them except the chat
// surface with the routing flag on. What §3.5 asked to retire was an
// UNCONDITIONAL invariant; what remains is the same invariant scoped to
// the policy it was always really about.
//
// The other side of the trade is pinned, deliberately, right next to
// this one: TestKernel_ArmAlwaysPopulatesGoalAndStepsOnACleanRun
// asserts that a clean run under TaskStateArmAlways DOES get a goal and
// a step trail, and that they reach the composed prompt. If you are
// here because that test and this one look contradictory — they are the
// two positions of one switch, and both are load-bearing.
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

// ─────────────────────────────────────────────────────────────────────
// TaskStateArmAlways (agentgraph-total-convergence-01PMGX01 WP11b;
// design in agentic-turn-routing-01PMAG01 §3.5).
//
// A run that succeeded all the way to an exit gate has, by
// construction, never armed recovery — so under the failure-only rule
// the gate would be checking the answer against an empty goal
// (01PMAG01 G5). These tests pin the second policy, and pin that the
// FIRST one is untouched.
// ─────────────────────────────────────────────────────────────────────

// countingHistory is a race-safe HistoryReader that records how many
// full reads it served. The double-read regression WP05 fixed is the
// specific thing this guards.
type countingHistory struct {
	mu    sync.Mutex
	reads int
	msgs  []Message
}

func (h *countingHistory) History(_ context.Context, _ string, _ int) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	out := make([]Message, len(h.msgs))
	copy(out, h.msgs)
	return out, nil
}

func (h *countingHistory) readCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

// TestKernel_ArmAlwaysPopulatesGoalAndStepsOnACleanRun is the headline
// WP11b behaviour: no failure, no backtrack, and TaskState is populated
// anyway — which is the only way a completion check has anything to
// check against.
func TestKernel_ArmAlwaysPopulatesGoalAndStepsOnACleanRun(t *testing.T) {
	t.Parallel()
	hist := &countingHistory{msgs: []Message{{Role: "user", Content: "Summarise the Q3 report"}}}
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "arm-always", SystemPrompt: "BASE",
		Entrypoints: []string{"history_in"},
		Nodes: []Node{
			{ID: "history_in", Kind: NodeKindHistoryRead, Attrs: HistoryReadAttrs{N: 0}},
			{ID: "model", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "history_in", Port: "messages"}, To: EndpointRef{Node: "model", Port: "messages"}},
		},
	}
	k := NewKernel()
	env := &Env{
		RunID: "arm-always-run", SessionID: "s", Graph: g, LLM: llm, History: hist,
		TaskStateArming: TaskStateArmAlways,
	}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := env.TaskState.Goal(); got != "Summarise the Q3 report" {
		t.Errorf("Goal() = %q, want the first user turn — a clean run must record its goal", got)
	}
	steps, _ := env.TaskState.CompletedSteps()
	if len(steps) == 0 {
		t.Error("CompletedSteps() empty on a clean run; the exit gate has no trail to check against")
	}
	// And it reaches the composed prompt, which is the point of
	// recording it at all.
	req, ok := llm.lastRequest()
	if !ok {
		t.Fatal("no LLM request captured")
	}
	if !strings.Contains(req.SystemPrompt, "Summarise the Q3 report") {
		t.Errorf("goal did not reach the composed system prompt:\n%s", req.SystemPrompt)
	}
}

// TestKernel_ArmAlwaysIssuesNoExtraHistoryRead is the §3.5 "keep the
// lazy half" requirement. firstUserTurnGoal issues an unbounded
// History(..., 0), and a chat graph's history_in node already performs
// the identical read every turn — arming eagerly would double it, and
// the cost grows with conversation length.
func TestKernel_ArmAlwaysIssuesNoExtraHistoryRead(t *testing.T) {
	t.Parallel()
	hist := &countingHistory{msgs: []Message{{Role: "user", Content: "do the thing"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "arm-always-reads",
		Entrypoints: []string{"history_in"},
		Nodes: []Node{
			{ID: "history_in", Kind: NodeKindHistoryRead, Attrs: HistoryReadAttrs{N: 0}},
		},
	}
	env := &Env{
		RunID: "arm-reads-run", SessionID: "s", Graph: g, History: hist,
		TaskStateArming: TaskStateArmAlways,
	}
	applyEnvDefaults(env)
	if err := NewKernel().Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := hist.readCount(); got != 1 {
		t.Errorf("History reads = %d, want exactly 1 — the goal must come off history_in's own output, not a second read", got)
	}
	if got := env.TaskState.Goal(); got != "do the thing" {
		t.Errorf("Goal() = %q, want it derived from the already-loaded history", got)
	}
}

// TestKernel_ArmAlwaysWithoutHistoryNodeFallsBackToOneRead covers the
// other branch: a graph with no history_read node has nothing to
// piggyback on, so one read of its own is correct — nothing else was
// going to issue it.
//
// convergence:exercised checkpoint
//
// The graph under test is a single production checkpoint node driven by
// NewKernel().Run — the checkpointExecutor is real, not scripted. The
// test's own subject is the history-read arming policy; the checkpoint
// is the neutral node it uses to observe it, which is exactly what
// makes it honest evidence that the kind executes end to end.
func TestKernel_ArmAlwaysWithoutHistoryNodeFallsBackToOneRead(t *testing.T) {
	t.Parallel()
	hist := &countingHistory{msgs: []Message{{Role: "user", Content: "ship it"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "arm-always-nohist",
		Entrypoints: []string{"mark"},
		Nodes:       []Node{{ID: "mark", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "x"}}},
	}
	env := &Env{
		RunID: "arm-nohist-run", SessionID: "s", Graph: g, History: hist,
		TaskStateArming: TaskStateArmAlways,
	}
	applyEnvDefaults(env)
	if err := NewKernel().Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := hist.readCount(); got != 1 {
		t.Errorf("History reads = %d, want exactly 1", got)
	}
	if got := env.TaskState.Goal(); got != "ship it" {
		t.Errorf("Goal() = %q, want %q", got, "ship it")
	}
}

// TestKernel_ArmOnFailureIsUnchanged is the other half of the trade.
// The byte-identical-prompt invariant was not retired, it was made
// CONDITIONAL: it still holds at the default policy, which is every
// Kernel.Run caller that has not opted in. This test asserts that on
// the same graph the always-armed tests use, so the two policies are
// compared like for like rather than across different fixtures.
func TestKernel_ArmOnFailureIsUnchanged(t *testing.T) {
	t.Parallel()
	hist := &countingHistory{msgs: []Message{{Role: "user", Content: "Summarise the Q3 report"}}}
	llm := &stubLLM{responses: []LLMResponse{{Content: "ok", FinishReason: "stop"}}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "arm-default", SystemPrompt: "BASE",
		Entrypoints: []string{"history_in"},
		Nodes: []Node{
			{ID: "history_in", Kind: NodeKindHistoryRead, Attrs: HistoryReadAttrs{N: 0}},
			{ID: "model", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", SystemPrompt: "ROLE"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "history_in", Port: "messages"}, To: EndpointRef{Node: "model", Port: "messages"}},
		},
	}
	env := &Env{RunID: "arm-default-run", SessionID: "s", Graph: g, LLM: llm, History: hist}
	applyEnvDefaults(env)
	if err := NewKernel().Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := env.TaskState.Goal(); got != "" {
		t.Errorf("Goal() = %q, want empty at the default arming policy", got)
	}
	if steps, _ := env.TaskState.CompletedSteps(); len(steps) != 0 {
		t.Errorf("CompletedSteps() = %v, want none at the default arming policy", steps)
	}
	req, _ := llm.lastRequest()
	if req.SystemPrompt != "BASE\n\nROLE" {
		t.Errorf("SystemPrompt = %q, want byte-identical %q", req.SystemPrompt, "BASE\n\nROLE")
	}
}

// TestGoalFromMessages covers the derivation helper directly, including
// the rune-safety of its cap: a goal cut mid-rune would put invalid
// UTF-8 into every system prompt for the rest of the run.
func TestGoalFromMessages(t *testing.T) {
	t.Parallel()
	if got := goalFromMessages([]Message{
		{Role: "system", Content: "ignore me"},
		{Role: "user", Content: "  the goal  "},
		{Role: "user", Content: "later turn"},
	}); got != "the goal" {
		t.Errorf("goalFromMessages = %q, want the first user turn trimmed", got)
	}
	// []any is the shape a YAML/JSON round-trip of a port value takes.
	if got := goalFromMessages([]any{Message{Role: "user", Content: "via any"}}); got != "via any" {
		t.Errorf("goalFromMessages([]any) = %q, want %q", got, "via any")
	}
	if got := goalFromMessages("not messages"); got != "" {
		t.Errorf("goalFromMessages(non-messages) = %q, want empty", got)
	}
	if got := goalFromMessages(nil); got != "" {
		t.Errorf("goalFromMessages(nil) = %q, want empty", got)
	}
	// Multi-byte truncation stays on rune boundaries.
	long := strings.Repeat("日", maxTaskGoalRunes+50)
	got := goalFromMessages([]Message{{Role: "user", Content: long}})
	if !utf8.ValidString(got) {
		t.Errorf("truncated goal is not valid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) != maxTaskGoalRunes+1 { // +1 for the ellipsis
		t.Errorf("truncated goal is %d runes, want %d", len(r), maxTaskGoalRunes+1)
	}
}
