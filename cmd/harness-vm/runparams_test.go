package main

import (
	"testing"
	"time"
)

// TestRunParamsParse verifies the additive run_params object is read off a
// task.start frame into the typed struct, and that an absent object yields
// hasParams=false (today's behaviour).
func TestRunParamsParse(t *testing.T) {
	// Absent → not present, zero value.
	if p, ok := parseRunParams(msg{"kind": "task.start", "task_id": "t", "prompt": "hi"}); ok || !p.isZero() {
		t.Fatalf("absent run_params: ok=%v params=%+v", ok, p)
	}

	// Present with a subset of keys.
	p, ok := parseRunParams(msg{
		"kind":    "task.start",
		"task_id": "t",
		"prompt":  "hi",
		"run_params": map[string]any{
			"workflow_preset":   "plan_implement_review",
			"autonomy_tier":     "bold",
			"review_rigor":      "high",
			"policy_strictness": "strict",
			"system_prompt_ref": "sys-1",
		},
	})
	if !ok {
		t.Fatalf("expected run_params present")
	}
	if p.WorkflowPreset != "plan_implement_review" || p.AutonomyTier != "bold" ||
		p.ReviewRigor != "high" || p.PolicyStrictness != "strict" || p.SystemPromptRef != "sys-1" {
		t.Fatalf("parsed params wrong: %+v", p)
	}
}

func TestValidateRunParams(t *testing.T) {
	// Empty and known-tier params validate.
	if r := validateRunParams(RunParams{}); r != "" {
		t.Fatalf("empty params should validate, got %q", r)
	}
	if r := validateRunParams(RunParams{AutonomyTier: "autonomous", ReviewRigor: "anything"}); r != "" {
		t.Fatalf("valid tier should pass, got %q", r)
	}
	// Unknown tier is rejected.
	if r := validateRunParams(RunParams{AutonomyTier: "ludicrous"}); r == "" {
		t.Fatalf("unknown tier should be rejected")
	}
}

func TestResolveWorkflowPreset(t *testing.T) {
	// Known builtin, by id (case-insensitive).
	steps, ok := resolveWorkflowPreset("Plan_Implement_Review")
	if !ok {
		t.Fatalf("expected plan_implement_review to resolve")
	}
	want := []string{"plan", "research", "implement", "review"}
	if len(steps) != len(want) {
		t.Fatalf("step count = %d, want %d (%v)", len(steps), len(want), steps)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step %d = %q, want %q", i, steps[i], want[i])
		}
	}
	// Unknown preset does not resolve.
	if _, ok := resolveWorkflowPreset("no-such-preset"); ok {
		t.Fatalf("unknown preset must not resolve")
	}
}

// TestTaskStartWithoutRunParamsUnchanged is the regression guard: a task.start
// with NO run_params drives the default two-node graph and completes exactly as
// before (two task.running chunks, then task.complete).
func TestTaskStartWithoutRunParamsUnchanged(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "t-default",
		"prompt":  "do the thing",
	})

	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	mu.Lock()
	var running int
	for _, m := range *msgs {
		if m["kind"] == "task.running" && m["task_id"] == "t-default" {
			running++
		}
	}
	mu.Unlock()
	if running != 2 {
		t.Fatalf("default graph should stream 2 task.running chunks, got %d", running)
	}
}

// TestTaskStartWithWorkflowPresetSelectsPreset drives task.start with a
// workflow_preset and asserts (via the ledger trail) that the harness selected
// the named preset — the ledger tool_call sequence matches the preset's step
// names. This is the unit-level shape of Spec 056 AC-5.
func TestTaskStartWithWorkflowPresetSelectsPreset(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	ledger := newTCPEmitter(ingest.addr(), "wb-preset")
	srv, addr := startTestServer(t, "", ledger)
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "t-preset",
		"prompt":  "ship the feature",
		"run_params": map[string]any{
			"workflow_preset": "plan_implement_review",
			"autonomy_tier":   "bold",
		},
	})

	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	// The preset has four steps; each surfaces a task.running chunk.
	mu.Lock()
	var running int
	for _, m := range *msgs {
		if m["kind"] == "task.running" && m["task_id"] == "t-preset" {
			running++
		}
	}
	mu.Unlock()
	if running != 4 {
		t.Fatalf("preset graph should stream 4 task.running chunks, got %d", running)
	}

	// The ledger tool_call phases carry the preset's step names — the observable
	// "node sequence" the run's ledger trail shows (AC-5). Each step emits one
	// tool_call record via a short-lived TCP connection; under load the fakeIngest
	// goroutines may append records in any order, so we assert the multiset rather
	// than arrival order to avoid a scheduling-dependent flake.
	recs := ingest.waitForPhases(t, 3*time.Second, phaseTaskStart, phaseToolCall, phaseTaskComplete)
	toolCount := map[string]int{}
	for _, r := range recs {
		if r.Payload["phase"] == phaseToolCall {
			if tool, _ := r.Payload["tool"].(string); tool != "" {
				toolCount[tool]++
			}
		}
	}
	want := []string{"plan", "research", "implement", "review"}
	if len(toolCount) != len(want) {
		t.Fatalf("expected %d distinct preset step tool_calls %v, got %v", len(want), want, toolCount)
	}
	for _, name := range want {
		if toolCount[name] != 1 {
			t.Fatalf("preset step %q: expected 1 tool_call record, got %d (full: %v)", name, toolCount[name], toolCount)
		}
	}
}

// TestTaskStartUnknownPresetErrors verifies an unresolvable workflow_preset is
// rejected with task.error{code:unknown_preset} and never marks the connection
// busy (a subsequent well-formed default task.start still completes).
func TestTaskStartUnknownPresetErrors(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "t-bad-preset",
		"prompt":  "x",
		"run_params": map[string]any{
			"workflow_preset": "does-not-exist",
		},
	})

	got := waitForKind(t, msgs, mu, "task.error", 2*time.Second)
	if got["code"] != "unknown_preset" {
		t.Fatalf("expected code unknown_preset, got %v", got["code"])
	}

	// The connection was never marked busy — a plain task.start now completes.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "t-after",
		"prompt":  "recover",
	})
	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)
}

// TestTaskStartInvalidAutonomyTierErrors verifies a malformed autonomy_tier is
// rejected with task.error{code:bad_request} before dispatch.
func TestTaskStartInvalidAutonomyTierErrors(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "t-bad-tier",
		"prompt":  "x",
		"run_params": map[string]any{
			"autonomy_tier": "ludicrous",
		},
	})

	got := waitForKind(t, msgs, mu, "task.error", 2*time.Second)
	if got["code"] != "bad_request" {
		t.Fatalf("expected code bad_request, got %v", got["code"])
	}
}
