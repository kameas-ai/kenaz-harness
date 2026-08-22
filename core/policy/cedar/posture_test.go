package cedar_test

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// fakeGate records every Evaluate call and returns a fixed Decision.
type fakeGate struct {
	calls []fakeGateCall
	fixed cedar.Decision
}

type fakeGateCall struct {
	action string
}

func (f *fakeGate) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	f.calls = append(f.calls, fakeGateCall{action: action})
	out := f.fixed
	out.Action = action
	out.Principal = principal.String()
	out.Resource = resource.String()
	return out
}

// TestWithPostureMode_PassThroughWhenNoMode verifies that a non-plan_mode
// value returns the inner gate directly (transparent pass-through), meaning
// evaluation still delegates to inner and inner sees the call.
func TestWithPostureMode_PassThroughWhenNoMode(t *testing.T) {
	inner := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow}}
	gate := cedar.WithPostureMode("", inner)
	// Evaluate any action — should be forwarded to inner.
	gate.Evaluate(context.Background(), cedar.UserUID(), cedar.ActionFileWrite, cedar.FilesystemUID("/tmp/x"), nil)
	if len(inner.calls) != 1 {
		t.Errorf("inner called %d times, want 1 (pass-through)", len(inner.calls))
	}
}

// TestWithPostureMode_PassThroughUnknownMode verifies an unknown posture string
// also passes through to inner.
func TestWithPostureMode_PassThroughUnknownMode(t *testing.T) {
	inner := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow}}
	gate := cedar.WithPostureMode("custom_mode", inner)
	gate.Evaluate(context.Background(), cedar.UserUID(), cedar.ActionFileWrite, cedar.FilesystemUID("/tmp/x"), nil)
	if len(inner.calls) != 1 {
		t.Errorf("inner called %d times for unknown mode, want 1 (pass-through)", len(inner.calls))
	}
}

// TestWithPostureMode_DeniesWriteActions verifies that every action in
// PlanModeDeniedActions is denied with plan_mode:read_only reason.
func TestWithPostureMode_DeniesWriteActions(t *testing.T) {
	inner := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow}}
	gate := cedar.WithPostureMode(cedar.PostureModePlanMode, inner)

	for _, action := range cedar.PlanModeDeniedActions {
		d := gate.Evaluate(
			context.Background(),
			cedar.UserUID(),
			action,
			cedar.FilesystemUID("/tmp/test.txt"),
			nil,
		)
		if d.Outcome != cedar.Deny {
			t.Errorf("action %q: outcome = %v, want Deny", action, d.Outcome)
		}
		if d.Reason != "plan_mode:read_only" {
			t.Errorf("action %q: reason = %q, want plan_mode:read_only", action, d.Reason)
		}
		if d.MatchedPolicy != "plan_mode:read_only" {
			t.Errorf("action %q: matched_policy = %q, want plan_mode:read_only", action, d.MatchedPolicy)
		}
	}

	// Inner should NOT have been called for any denied action.
	if len(inner.calls) != 0 {
		t.Errorf("inner gate called %d times for denied actions, want 0", len(inner.calls))
	}
}

// TestWithPostureMode_AllowsReadActions verifies that read-class actions
// pass through to the inner gate.
func TestWithPostureMode_AllowsReadActions(t *testing.T) {
	inner := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow}}
	gate := cedar.WithPostureMode(cedar.PostureModePlanMode, inner)

	readActions := []string{
		cedar.ActionFileRead,
		cedar.ActionReadFilesystem,
		cedar.ActionStateRead,
		cedar.ActionUseTool,
		cedar.ActionToolReadGlob,
		cedar.ActionToolReadGrep,
		cedar.ActionWorkflowRun,
		cedar.ActionPassive,
	}

	for _, action := range readActions {
		d := gate.Evaluate(
			context.Background(),
			cedar.UserUID(),
			action,
			cedar.FilesystemUID("/tmp/test.txt"),
			nil,
		)
		if d.Outcome != cedar.Allow {
			t.Errorf("read action %q: outcome = %v, want Allow (should pass through)", action, d.Outcome)
		}
	}

	if len(inner.calls) != len(readActions) {
		t.Errorf("inner called %d times, want %d", len(inner.calls), len(readActions))
	}
}

// TestWithPostureMode_PlanModeNilPanic verifies that a nil inner gate panics.
func TestWithPostureMode_NilInnerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("WithPostureMode with nil inner should panic")
		}
	}()
	cedar.WithPostureMode(cedar.PostureModePlanMode, nil)
}

// TestPlanModeDeniedActions_Coverage verifies the denied set is non-empty
// and contains the four expected write-family representatives.
func TestPlanModeDeniedActions_Coverage(t *testing.T) {
	if len(cedar.PlanModeDeniedActions) == 0 {
		t.Fatal("PlanModeDeniedActions is empty")
	}
	required := []string{
		cedar.ActionFileWrite,
		cedar.ActionWriteFilesystem,
		cedar.ActionMemoryWrite,
		cedar.ActionRunBashCommand,
	}
	denied := make(map[string]struct{}, len(cedar.PlanModeDeniedActions))
	for _, a := range cedar.PlanModeDeniedActions {
		denied[a] = struct{}{}
	}
	for _, r := range required {
		if _, ok := denied[r]; !ok {
			t.Errorf("PlanModeDeniedActions missing required action %q", r)
		}
	}
}

// TestPlanModeDeniedActions_IncludesScheduledRunExecute is AC-011c
// (model-scheduled-jobs-01PMSJ01 WP09, owner ruling B-3: "plan mode
// should not fire schedules"). Before this WP, ActionScheduledRunCreate
// and ActionScheduledRunDelete were denied in plan_mode but
// ActionScheduledRunExecute was not — a gap this test pins shut. It is
// deliberately a dedicated, named check (not folded into the generic
// membership loop above) so reverting the one-line addition to
// PlanModeDeniedActions turns THIS test red specifically.
func TestPlanModeDeniedActions_IncludesScheduledRunExecute(t *testing.T) {
	found := false
	for _, a := range cedar.PlanModeDeniedActions {
		if a == cedar.ActionScheduledRunExecute {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("PlanModeDeniedActions does not include ActionScheduledRunExecute — " +
			"a plan-mode session could fire a scheduled run (ruling B-3 violation)")
	}
}

// TestWithPostureMode_DeniesScheduledRunExecute drives the actual
// enforcement path (postureModeGate.Evaluate), not just slice
// membership: in plan_mode, ActionScheduledRunExecute must be denied
// with the standard plan_mode:read_only reason and the inner gate must
// never see the call — mirroring TestWithPostureMode_DeniesWriteActions
// but pinned to this one action so AC-011c is falsifiable independent
// of every other entry in the slice.
func TestWithPostureMode_DeniesScheduledRunExecute(t *testing.T) {
	inner := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow}}
	gate := cedar.WithPostureMode(cedar.PostureModePlanMode, inner)

	d := gate.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionScheduledRunExecute,
		cedar.ScheduledChatRunUID("run-1"),
		nil,
	)
	if d.Outcome != cedar.Deny {
		t.Fatalf("Outcome = %v, want Deny", d.Outcome)
	}
	if d.Reason != "plan_mode:read_only" {
		t.Fatalf("Reason = %q, want plan_mode:read_only", d.Reason)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("inner gate called %d times, want 0 (denied before delegation)", len(inner.calls))
	}
}
