package agentgraph

import (
	"fmt"
	"strings"
	"testing"
)

// ---- TaskState basics (autonomy-recovery-runtime-01PMDL03 WP03) ----

func TestTaskState_NilSafe(t *testing.T) {
	t.Parallel()
	var ts *TaskState
	if got := ts.Goal(); got != "" {
		t.Errorf("nil.Goal() = %q, want \"\"", got)
	}
	ts.SetGoal("should not panic")
	if steps, elided := ts.CompletedSteps(); steps != nil || elided != 0 {
		t.Errorf("nil.CompletedSteps() = %v, %d, want nil, 0", steps, elided)
	}
	ts.AddCompletedStep("should not panic")
	if got := ts.ForbiddenActions(); got != nil {
		t.Errorf("nil.ForbiddenActions() = %v, want nil", got)
	}
	ts.AddForbidden("should not panic")
}

func TestTaskState_GoalRoundTrip(t *testing.T) {
	t.Parallel()
	ts := NewTaskState()
	if got := ts.Goal(); got != "" {
		t.Errorf("fresh Goal() = %q, want \"\"", got)
	}
	ts.SetGoal("ship the feature")
	if got := ts.Goal(); got != "ship the feature" {
		t.Errorf("Goal() = %q", got)
	}
	// Setting again replaces rather than appends (e.g. after a replan).
	ts.SetGoal("ship the feature, revised")
	if got := ts.Goal(); got != "ship the feature, revised" {
		t.Errorf("Goal() after re-set = %q", got)
	}
}

func TestTaskState_ForbiddenActionsDedupedAndSorted(t *testing.T) {
	t.Parallel()
	ts := NewTaskState()
	ts.AddForbidden("delete_file", "bash", "delete_file", "")
	ts.AddForbidden("bash")
	got := ts.ForbiddenActions()
	want := []string{"bash", "delete_file"}
	if len(got) != len(want) {
		t.Fatalf("ForbiddenActions() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ForbiddenActions()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTaskState_CompletedStepsCapped asserts the stored completed-step
// trail is actually bounded (not just its rendering) — the spec risk
// is a runaway list growing forever, so the cap must apply at the
// storage layer.
func TestTaskState_CompletedStepsCapped(t *testing.T) {
	t.Parallel()
	ts := NewTaskState()
	const total = maxTaskCompletedSteps + 10
	for i := 0; i < total; i++ {
		ts.AddCompletedStep(fmt.Sprintf("step-%d", i))
	}
	steps, elided := ts.CompletedSteps()
	if len(steps) != maxTaskCompletedSteps {
		t.Errorf("len(steps) = %d, want %d (storage must stay bounded)", len(steps), maxTaskCompletedSteps)
	}
	if elided != total-maxTaskCompletedSteps {
		t.Errorf("elided = %d, want %d", elided, total-maxTaskCompletedSteps)
	}
	// The retained window must be the most recent entries, oldest of
	// the kept window first.
	if steps[0] != fmt.Sprintf("step-%d", total-maxTaskCompletedSteps) {
		t.Errorf("steps[0] = %q, want the oldest of the retained window", steps[0])
	}
	if steps[len(steps)-1] != fmt.Sprintf("step-%d", total-1) {
		t.Errorf("steps[last] = %q, want the most recent entry", steps[len(steps)-1])
	}
}

// ---- renderTaskState (pinned system-context block) ----

func TestRenderTaskState_EmptyIsByteIdenticalToBefore(t *testing.T) {
	t.Parallel()
	// No TaskState content, no failure annotations — this must render
	// to "" exactly as renderFailureAnnotations(nil) did pre-WP03, so
	// the happy path composes byte-identically (no regression).
	if got := renderTaskState(nil, nil); got != "" {
		t.Errorf("renderTaskState(nil, nil) = %q, want \"\"", got)
	}
	if got := renderTaskState(NewTaskState(), nil); got != "" {
		t.Errorf("renderTaskState(empty, nil) = %q, want \"\"", got)
	}
}

func TestRenderTaskState_OnlyAnnotationsMatchesPriorRendering(t *testing.T) {
	t.Parallel()
	anns := []FailureAnnotation{
		{Node: "draft", Reason: "too vague", RejectedApproach: "v1 text", Iteration: 1},
	}
	got := renderTaskState(nil, anns)
	want := renderFailureAnnotations(anns)
	if got != want {
		t.Errorf("renderTaskState with only annotations =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderTaskState_ComposesAllDimensions(t *testing.T) {
	t.Parallel()
	ts := NewTaskState()
	ts.SetGoal("write the report")
	ts.AddCompletedStep("outline drafted")
	ts.AddCompletedStep("intro written")
	ts.AddForbidden("delete_file")
	anns := []FailureAnnotation{{Node: "draft", Reason: "rejected", Iteration: 1}}

	got := renderTaskState(ts, anns)

	for _, want := range []string{
		"write the report",
		"outline drafted",
		"intro written",
		"delete_file",
		"rejected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderTaskState() = %q, want it to contain %q", got, want)
		}
	}
	// Order: goal, then completed steps, then forbidden, then failed attempts.
	goalIdx := strings.Index(got, "write the report")
	stepsIdx := strings.Index(got, "outline drafted")
	forbIdx := strings.Index(got, "delete_file")
	annIdx := strings.Index(got, "rejected")
	if !(goalIdx < stepsIdx && stepsIdx < forbIdx && forbIdx < annIdx) {
		t.Errorf("expected goal < steps < forbidden < annotations ordering, got %q", got)
	}
}

// TestRenderTaskState_FailedAttemptsCapped asserts the render itself
// stays token-bounded regardless of how many FailureAnnotations have
// accumulated — the spec risk is "a runaway failed-attempts list must
// be summarised, not appended forever ... this rides on every model
// call".
func TestRenderTaskState_FailedAttemptsCapped(t *testing.T) {
	t.Parallel()
	const total = maxRenderedFailedAttempts + 4
	anns := make([]FailureAnnotation, total)
	for i := range anns {
		anns[i] = FailureAnnotation{Node: "draft", Reason: fmt.Sprintf("reason-%d", i), Iteration: i + 1}
	}
	got := renderTaskState(nil, anns)

	// The oldest entries must be summarised away, not rendered verbatim.
	if strings.Contains(got, "reason-0") {
		t.Errorf("expected oldest annotation to be elided, got %q", got)
	}
	// The most recent entries must still be present.
	if !strings.Contains(got, fmt.Sprintf("reason-%d", total-1)) {
		t.Errorf("expected most recent annotation present, got %q", got)
	}
	wantElided := total - maxRenderedFailedAttempts
	if !strings.Contains(got, fmt.Sprintf("+%d earlier rejected attempts elided", wantElided)) {
		t.Errorf("expected elided-count summary line for %d elided, got %q", wantElided, got)
	}

	// The number of "[backtrack N]" lines rendered must be capped
	// regardless of how large `anns` grows.
	if got2 := renderTaskState(nil, append(anns, anns...)); strings.Count(got2, "[backtrack") != maxRenderedFailedAttempts {
		t.Errorf("rendered backtrack-line count = %d for doubled input, want bounded at %d",
			strings.Count(got2, "[backtrack"), maxRenderedFailedAttempts)
	}
}
