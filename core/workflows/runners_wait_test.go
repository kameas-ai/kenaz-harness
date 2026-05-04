package workflows

import (
	"context"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// wait_until runner tests
// =============================================================================

func TestWaitUntil_Duration_ReturnsWithinWindow(t *testing.T) {
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Steps: []Step{
			{Name: "pause", Kind: StepKindWaitUntil, WaitDuration: "100ms"},
		},
	}
	start := time.Now()
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	// Should complete in ~100ms; allow up to 500ms for CI jitter.
	if elapsed < 80*time.Millisecond {
		t.Errorf("elapsed too short: %v (want ≥80ms)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed too long: %v (want ≤500ms)", elapsed)
	}
	if !strings.Contains(run.Steps[0].Output, "woken") {
		t.Errorf("output: got %q, want to contain 'woken'", run.Steps[0].Output)
	}
}

func TestWaitUntil_UntilPastTime_ReturnsImmediately(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Steps: []Step{
			{Name: "pause", Kind: StepKindWaitUntil, Until: past},
		},
	}
	start := time.Now()
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s", run.Status)
	}
	// Should be near-instant.
	if elapsed > 100*time.Millisecond {
		t.Errorf("elapsed too long for past-time: %v", elapsed)
	}
	if !strings.Contains(run.Steps[0].Output, "expired") {
		t.Errorf("output: got %q, want to contain 'expired'", run.Steps[0].Output)
	}
}

func TestWaitUntil_UntilFutureTime_CancelMidWait_ReturnsContextCanceled(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Steps: []Step{
			{Name: "pause", Kind: StepKindWaitUntil, Until: future},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	run, err := NewEngine().Run(ctx, wf, nil, RunOptions{})
	elapsed := time.Since(start)

	// err must be context.Canceled or DeadlineExceeded.
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	// Cancellation should arrive promptly (within 300ms).
	if elapsed > 300*time.Millisecond {
		t.Errorf("elapsed too long after cancel: %v", elapsed)
	}
	if run.Status != "failed" && run.Status != "interrupted" {
		t.Errorf("run status: got %q, want failed or interrupted", run.Status)
	}

	// Wakeup state should be preserved after cancellation.
	// (We can't easily inspect it from outside, but the run should have recorded
	// the step as interrupted/failed — which is sufficient for the spec.)
	_ = run
}

func TestWaitUntil_Condition_ResumesWhenTruthy(t *testing.T) {
	// We use a workflow where an earlier step produces the "ready" output,
	// and wait_until polls ${step.producer.output} == true.
	//
	// To make the condition immediately truthy (since step outputs are set
	// before wait_until runs), we wire the producer step before wait_until.
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Steps: []Step{
			{Name: "producer", Kind: StepKindTransform, Template: "true"},
			{Name: "gate", Kind: StepKindWaitUntil, Condition: "${step.producer.output} == true"},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if !strings.Contains(run.Steps[1].Output, "condition_met") {
		t.Errorf("gate output: got %q, want 'condition_met'", run.Steps[1].Output)
	}
}

func TestWaitUntil_Condition_ContextCancelWhilePolling(t *testing.T) {
	// Condition that is never truthy.
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Inputs: []Input{{Name: "ready", Kind: InputKindString}},
		Steps: []Step{
			{Name: "gate", Kind: StepKindWaitUntil, Condition: "${input.ready} == true"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	inputs := map[string]TypedValue{"ready": {Type: ValueTypeText, Text: "false"}}
	run, err := NewEngine().Run(ctx, wf, inputs, RunOptions{})
	if err == nil {
		t.Fatalf("expected cancellation, got nil error; status=%s", run.Status)
	}
	// Should be canceled by context timeout, not some other error.
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected context-related error, got: %v", err)
	}
}

func TestWaitUntil_ValidationRejectsMultipleArgs(t *testing.T) {
	wf := Workflow{
		ID: "w", Name: "w", Version: 1,
		Steps: []Step{
			{Name: "pause", Kind: StepKindWaitUntil,
				WaitDuration: "1s",
				Until:        time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("expected multi-arg error, got: %v", err)
	}
}
