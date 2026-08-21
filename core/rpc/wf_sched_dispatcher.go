package rpc

// wf_sched_dispatcher.go — automation-actually-runs-01PMZ404 UNIT-2.
//
// wfSchedDispatcher is the production implementation of
// wfsched.Dispatcher. Before this unit, wfsched.Config.Dispatcher (and,
// as of UNIT-1, its successor DispatcherFunc) was never assigned in
// production, so every enabled workflow schedule and every "Run now"
// click on the Scheduled Inbox failed with ErrNoDispatcherWired — the
// honest failure UNIT-1 introduced in place of the manufactured
// "completed" summary the scheduler used to fabricate.
//
// Dispatch drives the SAME live engine the manual Run button uses —
// workflowsview.API.RunWithOptions, which resolves the workflow, runs
// cedar.GateWorkflowRun, and calls the engine — so a scheduled or
// RunNow-triggered run is gated exactly like a manual one. No new
// bypass (spec D-5).
//
// Construction order: core/rpc/api.go constructs the CronScheduler
// (and therefore this type, via wfsched.Config.DispatcherFunc) before
// a.workflowsAPI is assigned, 77 lines later in the same function. The
// DispatcherFunc closure captures *API (already allocated) rather than
// a.workflowsAPI directly, and Dispatch reads a.workflowsAPI lazily —
// by the time any fire can happen, Start() has been called (from
// SetContext, well after construction finishes), so a.workflowsAPI is
// guaranteed to be set.

import (
	"context"
	"fmt"
	"strings"

	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// wfSchedDispatcher implements wfsched.Dispatcher over the API's
// workflowsAPI.
type wfSchedDispatcher struct {
	api *API
}

// Dispatch implements wfsched.Dispatcher. scheduled distinguishes a cron
// tick (true) from a human-clicked "Run now" (false, via
// CronScheduler.RunNow) — both arrive here with zero inputs, since
// neither surface collects a run form.
func (d *wfSchedDispatcher) Dispatch(ctx context.Context, workflowID string, scheduled bool) (string, error) {
	if d == nil || d.api == nil || d.api.workflowsAPI == nil {
		return "", fmt.Errorf("workflow dispatcher: workflows API not constructed")
	}

	// A scheduled or RunNow dispatch supplies no inputs — neither surface
	// has a run form. A workflow declaring a required input with no
	// default cannot run unattended; fail loudly naming the gap rather
	// than letting the engine silently proceed with the input absent
	// (spec §5.3 / UNIT-2: "it may not silently substitute defaults").
	wf, err := d.api.workflowsAPI.Get(ctx, workflowID)
	if err != nil {
		return "", fmt.Errorf("workflow dispatcher: resolve workflow: %w", err)
	}
	var missing []string
	for _, in := range wf.Inputs {
		if in.Required && in.Default == "" {
			missing = append(missing, in.Name)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"workflow %q requires input(s) [%s] with no default; scheduled and Run-now dispatches supply no inputs and cannot run it unattended",
			workflowID, strings.Join(missing, ", "),
		)
	}

	res, err := d.api.workflowsAPI.RunWithOptions(ctx, workflowsview.RunRequest{ID: workflowID})
	if err != nil {
		return "", err
	}
	if res.Err != "" {
		return res.RunID, fmt.Errorf("%s", res.Err)
	}
	return res.RunID, nil
}
