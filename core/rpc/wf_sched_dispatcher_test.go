package rpc

// wf_sched_dispatcher_test.go — automation-actually-runs-01PMZ404 UNIT-2
// (AC-003). Drives the REAL production wiring: core.New + rpc.New over a
// temp DataDir with an on-disk Cedar policy, exactly like
// api_cedar_gate_wiring_test.go's Site 4 workflow tests, but through
// api.wfScheduler.RunNow rather than api.Workflows().Run — proving the
// scheduled/RunNow dispatch path (wfSchedDispatcher, wired via
// wfsched.Config.DispatcherFunc) reaches the SAME live engine + Cedar
// gate as the manual Run button, with no new bypass (spec D-5).
//
// Before this unit, wfsched.Config.DispatcherFunc was never assigned in
// production (core/rpc/api.go's sole wfsched.New call site), so EVERY
// call below would have failed identically with
// wfsched.ErrNoDispatcherWired regardless of the Cedar policy or the
// workflow's inputs — an engine that is never armed passes every
// scheduler-package unit test (those construct their own CronScheduler
// with an explicit stub Dispatcher). These tests distinguish "dispatcher
// wired" failures (Cedar denial, workflow-not-found, missing required
// input) from the nil-dispatcher failure by asserting errors.Is(err,
// wfsched.ErrNoDispatcherWired) is false on the paths that should reach
// the real engine.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
)

const wfSchedDispatcherTestYAML = `
id: zz-wfsched-dispatcher-probe
name: "wfsched dispatcher probe"
version: 1
steps:
  - name: run_it
    kind: shell
    cmd: "echo"
    args: ["hello"]
`

// TestWfSchedDispatcher_ProductionPath_DispatcherIsWired is the
// production-path assertion tasks.md UNIT-2 / spec §10 rule 3 requires:
// rpc.New + SetContext yields a scheduler whose dispatcher is non-nil.
// RunNow for an UNKNOWN workflow id must fail with a workflow-resolution
// error (wfSchedDispatcher.Dispatch calls workflowsAPI.Get before
// RunWithOptions), not ErrNoDispatcherWired — the latter would mean
// DispatcherFunc never resolved a real Dispatcher.
func TestWfSchedDispatcher_ProductionPath_DispatcherIsWired(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.wfScheduler == nil {
		t.Fatal("New(c) with a real DB did not construct wfScheduler")
	}
	api.SetContext(context.Background())

	_, err := api.wfScheduler.RunNow(context.Background(), "zz-does-not-exist")
	if err == nil {
		t.Fatal("RunNow for an unknown workflow id returned nil error")
	}
	if errors.Is(err, wfsched.ErrNoDispatcherWired) {
		t.Fatalf("RunNow returned ErrNoDispatcherWired for a KNOWN-missing workflow id — "+
			"DispatcherFunc did not resolve a real Dispatcher: %v", err)
	}
}

// TestWfSchedDispatcher_RunNow_GoesThroughTheSameCedarGateAsManualRun is
// AC-003's "a denying Cedar gate makes the scheduled run fail" case,
// proving the dispatch goes through RunWithOptions (and therefore
// cedar.GateWorkflowRun) rather than around it. A bespoke dispatcher
// that called the engine directly, skipping the gate, would pass every
// other test in this file and still be a new bypass.
func TestWfSchedDispatcher_RunNow_GoesThroughTheSameCedarGateAsManualRun(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionWorkflowRun))
	ctx := context.Background()

	// Save is permitted by the embedded bundle; only workflow.run is denied.
	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wfSchedDispatcherTestYAML})
	if serr != nil {
		t.Fatalf("Save (should be permitted): %v", serr)
	}
	if api.wfScheduler == nil {
		t.Fatal("wfScheduler is nil")
	}
	api.SetContext(ctx)

	_, err := api.wfScheduler.RunNow(ctx, saved.ID)
	if err == nil {
		t.Fatal("RunNow succeeded under a `forbid workflow.run` policy — the scheduled/RunNow " +
			"dispatch path bypasses the Cedar gate the manual Run button uses")
	}
	if !errors.Is(err, workflowsview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of workflowsview.ErrCedarDenied", err)
	}
}

// TestWfSchedDispatcher_RunNow_DefaultPolicyStillPermits is the paired
// "both arms required" guard (tasks.md UNIT-2 tests): a deny-only test
// is satisfiable by denying everything.
func TestWfSchedDispatcher_RunNow_DefaultPolicyStillPermits(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wfSchedDispatcherTestYAML})
	if serr != nil {
		t.Fatalf("Save: %v", serr)
	}
	if api.wfScheduler == nil {
		t.Fatal("wfScheduler is nil")
	}
	api.SetContext(ctx)

	summary, err := api.wfScheduler.RunNow(ctx, saved.ID)
	if err != nil {
		t.Fatalf("RunNow refused on a default install: %v", err)
	}
	if summary.Status != "completed" {
		t.Errorf("summary.Status = %q, want completed", summary.Status)
	}
	if summary.RunID == "" {
		t.Error("summary.RunID is empty on a completed run")
	}
}

// TestWfSchedDispatcher_RequiredInputWithNoDefault_FailsLoudly is UNIT-2
// spec §5.3's decision: a scheduled or RunNow dispatch supplies no
// inputs (neither surface has a run form), so a workflow declaring a
// required input with no default cannot run unattended. It must fail
// loudly naming the gap rather than the engine silently proceeding with
// the input absent.
func TestWfSchedDispatcher_RequiredInputWithNoDefault_FailsLoudly(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	const yaml = `
id: zz-wfsched-required-input-probe
name: "wfsched required-input probe"
version: 1
inputs:
  - name: topic
    kind: string
    required: true
steps:
  - name: run_it
    kind: shell
    cmd: "echo"
    args: ["${input.topic}"]
`
	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
	if serr != nil {
		t.Fatalf("Save: %v", serr)
	}
	if api.wfScheduler == nil {
		t.Fatal("wfScheduler is nil")
	}
	api.SetContext(ctx)

	_, err := api.wfScheduler.RunNow(ctx, saved.ID)
	if err == nil {
		t.Fatal("RunNow succeeded on a workflow with a required input and no default — " +
			"a scheduled/RunNow dispatch supplies no inputs and must fail loudly instead")
	}
	if errors.Is(err, wfsched.ErrNoDispatcherWired) {
		t.Fatal("required-input failure must not be indistinguishable from a nil-dispatcher failure")
	}
	if !strings.Contains(err.Error(), "topic") {
		t.Errorf("err = %v; want it to name the missing input %q", err, "topic")
	}
}

// TestWfSchedDispatcher_RequiredInputWithDefault_StillRuns is the paired
// guard: a required input WITH a default is fine unattended — only the
// no-default case must refuse.
func TestWfSchedDispatcher_RequiredInputWithDefault_StillRuns(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	const yaml = `
id: zz-wfsched-required-input-default-probe
name: "wfsched required-input-with-default probe"
version: 1
inputs:
  - name: topic
    kind: string
    required: true
    default: "hello"
steps:
  - name: run_it
    kind: shell
    cmd: "echo"
    args: ["${input.topic}"]
`
	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
	if serr != nil {
		t.Fatalf("Save: %v", serr)
	}
	if api.wfScheduler == nil {
		t.Fatal("wfScheduler is nil")
	}
	api.SetContext(ctx)

	summary, err := api.wfScheduler.RunNow(ctx, saved.ID)
	if err != nil {
		t.Fatalf("RunNow refused a required input that HAS a default: %v", err)
	}
	if summary.Status != "completed" {
		t.Errorf("summary.Status = %q, want completed", summary.Status)
	}
}
