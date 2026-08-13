package chat

import (
	"context"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// routed_turn_integration_test.go — the agentic-turn-routing flag's ON
// position, driven end to end through the real chat_default.yaml
// (agentgraph-total-convergence-01PMGX01 WP11b; design in
// agentic-turn-routing-01PMAG01 §3.1/§3.4/§3.6).
//
// The rest of this package's integration tests run the flag's OFF
// position, because that is what ships. These run the other one, so the
// opt-in path is not shipped untested — a flag with only one position
// exercised is a flag with a trapdoor under it.

// TestRoutedTurn_ExitGateVerifiesBeforeThePersistedAnswer is the
// headline: with routing on, the assistant's reply reaches session
// history only after the exit gate has passed it.
//
// Before this, `agent_loop` exited when tool_call_count hit 0 — "the
// model stopped calling tools" was the whole completion criterion
// (01PMAG01 G1).
func TestRoutedTurn_ExitGateVerifiesBeforeThePersistedAnswer(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	// Call 1: the assistant turn. Its text carries the fused routing
	// choice, which is what makes routing free.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "the answer"}},
		resp: coreag.LLMResponse{
			Content:      `the answer {"next_choice": "done"}`,
			FinishReason: "stop",
		},
	})
	// Call 2: the exit gate's verdict.
	llm.push(stubLLMResponse{
		resp: coreag.LLMResponse{
			Content:      `{"verdict": "pass", "reason": "answers the question"}`,
			FinishReason: "stop",
		},
	})

	runner, broker, writer := buildIntegrationRunnerWithGraph(t, llm, newStubTools(), 25,
		[]coreag.Message{{Role: "user", Content: "say hi"}}, loadRoutedChatGraph(t))

	if _, err := runner.StartStream(context.Background(), "p", "s", "", "say hi"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	// Two calls: the assistant turn and the exit gate. The ROUTER
	// contributes zero — that is FR-003, and the reason `route` is
	// fused off assistant_turn rather than making its own call. The
	// gate's call is the deliberate, documented cost of a verified exit
	// (§3.4: it fires once per turn, not per loop iteration), which is
	// exactly why the whole topology is behind a flag.
	if got := llm.calls.Load(); got != 2 {
		t.Errorf("llm.calls = %d, want 2 (assistant turn + exit gate; routing adds none)", got)
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) < 2 {
		t.Fatalf("len(writer.calls) = %d, want >=2 (user turn + assistant turn)", len(writer.calls))
	}
	last := writer.calls[len(writer.calls)-1]
	if last.role != "assistant" {
		t.Errorf("last writer call role = %q, want assistant", last.role)
	}
	if !strings.Contains(last.content, "the answer") {
		t.Errorf("assistant content = %q, want the verified draft", last.content)
	}
}

// TestRoutedTurn_ExitGateFailRewindsIntoTheLoop is the other half of a
// verified exit: a FAIL verdict must actually send the turn back, not
// merely be recorded. The rewind runs on the kernel's existing
// backtrack primitive via should_retry/retry_target — no new kernel
// machinery (01PMAG01 §3.4).
func TestRoutedTurn_ExitGateFailRewindsIntoTheLoop(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	// Pass 1: a half-done answer.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "half"}},
		resp:   coreag.LLMResponse{Content: `half {"next_choice": "done"}`, FinishReason: "stop"},
	})
	// Gate 1: reject it.
	llm.push(stubLLMResponse{
		resp: coreag.LLMResponse{Content: `{"verdict":"fail","reason":"only half the work"}`, FinishReason: "stop"},
	})
	// Pass 2: the rewound assistant turn.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "complete"}},
		resp:   coreag.LLMResponse{Content: `complete {"next_choice": "done"}`, FinishReason: "stop"},
	})
	// Gate 2: accept.
	llm.push(stubLLMResponse{
		resp: coreag.LLMResponse{Content: `{"verdict":"pass","reason":"now complete"}`, FinishReason: "stop"},
	})

	runner, broker, writer := buildIntegrationRunnerWithGraph(t, llm, newStubTools(), 25,
		[]coreag.Message{{Role: "user", Content: "do the work"}}, loadRoutedChatGraph(t))

	if _, err := runner.StartStream(context.Background(), "p", "s", "", "do the work"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	// Four calls: turn, reject, re-run turn, accept. Three would mean
	// the FAIL was recorded and ignored.
	if got := llm.calls.Load(); got != 4 {
		t.Errorf("llm.calls = %d, want 4 (turn, FAIL, rewound turn, PASS)", got)
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()
	last := writer.calls[len(writer.calls)-1]
	if !strings.Contains(last.content, "complete") {
		t.Errorf("persisted answer = %q, want the post-rewind draft — the FAIL did not send the turn back", last.content)
	}
	if strings.Contains(last.content, "half") {
		t.Errorf("persisted answer = %q, want the rejected draft NOT to be what the user gets", last.content)
	}
}

// TestRoutedTurn_TaskStateArmingFollowsTheTopology pins the coupling
// that keeps the flag from being half-applied. The exit gate checks the
// draft against the run's goal; a routed graph running on failure-only
// TaskState would be checking it against nothing (01PMAG01 G5).
func TestRoutedTurn_TaskStateArmingFollowsTheTopology(t *testing.T) {
	t.Parallel()
	routed := loadRoutedChatGraph(t)
	classic := loadProductionChatGraph(t)

	if got := taskStateArmingFor(routed); got != coreag.TaskStateArmAlways {
		t.Errorf("taskStateArmingFor(routed) = %q, want %q", got, coreag.TaskStateArmAlways)
	}
	if got := taskStateArmingFor(classic); got != coreag.TaskStateArmOnFailure {
		t.Errorf("taskStateArmingFor(classic) = %q, want %q — the classic topology must keep today's prompt exactly", got, coreag.TaskStateArmOnFailure)
	}
}

// TestRoutedTurn_GoalReachesTheExitGate is the end-to-end statement of
// why TaskState had to become always-armed: on a run that never failed,
// the gate's prompt must still carry the user's request.
func TestRoutedTurn_GoalReachesTheExitGate(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "done"}},
		resp:   coreag.LLMResponse{Content: `done {"next_choice": "done"}`, FinishReason: "stop"},
	})
	llm.push(stubLLMResponse{
		resp: coreag.LLMResponse{Content: `{"verdict":"pass","reason":"ok"}`, FinishReason: "stop"},
	})

	runner, broker, _ := buildIntegrationRunnerWithGraph(t, llm, newStubTools(), 25,
		[]coreag.Message{{Role: "user", Content: "Summarise the Q3 revenue report"}}, loadRoutedChatGraph(t))

	if _, err := runner.StartStream(context.Background(), "p", "s", "", "Summarise the Q3 revenue report"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q; msg=%q", closed.Reason, closed.Message)
	}

	reqs := llm.snapshotRequests()
	if len(reqs) < 2 {
		t.Fatalf("captured %d requests, want >=2", len(reqs))
	}
	gate := reqs[len(reqs)-1]
	var body string
	if len(gate.Messages) > 0 {
		body = gate.Messages[len(gate.Messages)-1].Content
	}
	if !strings.Contains(body, "Summarise the Q3 revenue report") {
		t.Errorf("the exit gate's prompt does not carry the user's goal — it is checking the answer against nothing:\n%s", body)
	}
}

// TestRoutedTurn_DoomLoopRoutesIntoTheLadder is WP11c's acceptance
// driven end to end: a model that keeps making the SAME tool call with
// the SAME arguments trips tool_dispatch's doom-loop guard, and the
// turn leaves the loop through the recovery ladder instead of grinding
// the budget down.
//
// This is the behaviour that had every piece built and none of them
// connected — the guard emitted `should_replan` into the void, and
// escalationLadderExecutor was referenced by zero graphs (01PMAG01 G4).
func TestRoutedTurn_DoomLoopRoutesIntoTheLadder(t *testing.T) {
	t.Parallel()
	tools := newStubTools("search")

	llm := &stubLLM{}
	// The same call, with identical arguments, forever — the exact
	// pattern doomLoopKey normalises onto one counter, and a model that
	// never volunteers to stop. The guard trips at
	// DefaultDoomLoopThreshold (3) repeats.
	//
	// The queue is deliberately longer than the loop's 25-iteration cap
	// so that this test DISCRIMINATES: with no consumer for the guard's
	// signal, the router keeps honouring the model's "continue" and the
	// turn grinds all 25 iterations down. It is the doom-loop override
	// that gets it out, and the call counts below are how we know.
	for i := 0; i < 40; i++ {
		llm.push(stubLLMResponse{
			resp: coreag.LLMResponse{
				Content:      `working on it {"next_choice": "continue"}`,
				FinishReason: "tool_use",
				ToolCalls: []coreag.ToolCallRequest{
					{ID: "c", Name: "search", Arguments: `{"q":"same"}`},
				},
			},
		})
		tools.push(coreag.ToolResult{Content: "no results"})
	}

	runner, broker, _ := buildIntegrationRunnerWithGraph(t, llm, tools, 25,
		[]coreag.Message{{Role: "user", Content: "search for it"}}, loadRoutedChatGraph(t))

	if _, err := runner.StartStream(context.Background(), "p", "s", "", "search for it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q; msg=%q", closed.Reason, closed.Message)
	}

	// MEASURED, both ways. With the consumer wired: 6 LLM calls, 4 tool
	// calls — the guard trips on the third identical call, the router
	// overrides the model's "continue", and the turn leaves the loop
	// through the ladder. With the override disabled (verified by
	// stubbing the condition to false): 14 LLM calls, 12 tool calls,
	// climbing with the iteration cap.
	//
	// The bounds sit between those two numbers on purpose. A test whose
	// assertion both configurations satisfy would be decoration — this
	// one fails if the signal ever goes nowhere again, which is exactly
	// the state the mission found (01PMAG01 G4).
	if got := llm.calls.Load(); got > 10 {
		t.Errorf("llm.calls = %d, want <=10 — the doom loop was not broken; the guard's signal is going nowhere", got)
	}
	if toolCalls := tools.snapshotCalls(); len(toolCalls) > 6 {
		t.Errorf("tool calls = %d, want <=6 — the same failing call kept running past the guard", len(toolCalls))
	}
}
