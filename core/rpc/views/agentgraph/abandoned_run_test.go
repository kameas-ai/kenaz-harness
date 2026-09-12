package agentgraph_test

// abandoned_run_test.go — approval-node-01PMZC12 E-002's "abandoned"
// fallback for G7 ("a run halted under one release resumes under the
// next"). UNIT-6 (durable pause: rebuild a resumable *coreag.Env from
// the event log at boot) is cut — see spec.md §5.5, §13 E-002. This is
// the cheaper alternative the spec explicitly sanctions instead:
// GetRunStatus/GetRunTrace must report a paused-then-orphaned run as
// RunStateAbandoned, with a recorded reason, rather than "not found"
// forever. "Not found" is silence about a decision a durable trail
// says a human was asked and never answered — spec.md §5.5 calls that
// the one unacceptable outcome.
//
// Per CLAUDE.md's testing-rules discipline (spec.md §9 rule 1 for this
// mission specifically): assert on the event stream, not only on the
// status struct — a check that only reads RunStatus.State would pass
// even if the abandonment were never recorded durably.

import (
	"context"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestRehydrateAbandonedRuns_MarksOrphanedPauseAbandoned is the core
// property: a run paused by an EARLIER Manager (simulating a process
// that parked on an approval and then exited) is reported as
// RunStateAbandoned — not "not found" — by a fresh Manager constructed
// over the same durable EventLog, with a non-generic reason and an
// EventRunAbandoned row appended to the trail.
func TestRehydrateAbandonedRuns_MarksOrphanedPauseAbandoned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// "Process 1": a bare EventLog gets the exact event pair
	// approvalExecutor.Execute writes on its first fire (UNIT-2) —
	// approval_pending, then the kernel's kind-agnostic run_paused.
	// Deliberately NOT going through a Manager/kernel run here: the
	// property under test is boot-time reconciliation over a durable
	// trail, and a shared in-memory EventLog is the same kind of
	// "surviving state" a SQL-backed one would be across a real
	// restart (this mission's own upgrade-path test,
	// TestUpgradePath_PausedRunIsAbandonedAcrossRestart, covers the
	// real-sqlite/real-restart axis; this test covers the
	// reconciliation LOGIC in isolation).
	log := coreag.NewMemoryEventLog()
	const runID = "run-abandoned-probe"
	const nodeID = "a"
	var batch coreag.EventBatch
	if err := batch.AppendKind(runID, "", coreag.EventRunStart, map[string]any{
		"graph_id": "resolve_approval_rpc",
	}); err != nil {
		t.Fatalf("AppendKind run_start: %v", err)
	}
	if err := batch.AppendKind(runID, nodeID, coreag.EventApprovalPending, map[string]any{
		"prompt":        "Ship it?",
		"approver_role": "user",
	}); err != nil {
		t.Fatalf("AppendKind approval_pending: %v", err)
	}
	if err := batch.AppendKind(runID, "", coreag.EventRunPaused, map[string]any{
		"reason": "approval: Ship it?",
	}); err != nil {
		t.Fatalf("AppendKind run_paused: %v", err)
	}
	if _, err := log.Append(batch); err != nil {
		t.Fatalf("Append pause events: %v", err)
	}

	// "Process 2": a FRESH Manager over the SAME log. NewManager's
	// runs map starts empty regardless — this is exactly what a real
	// restart looks like from the Manager's point of view.
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(t.TempDir()),
		graphview.WithEventLog(log),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	impl := graphview.New(mgr)

	st, err := impl.GetRunStatus(ctx, runID)
	if err != nil {
		t.Fatalf("GetRunStatus must report the run, not fail as not-found: %v", err)
	}
	if st.State != graphview.RunStateAbandoned {
		t.Fatalf("state = %q, want %q", st.State, graphview.RunStateAbandoned)
	}
	if st.GraphID != "resolve_approval_rpc" {
		t.Errorf("graphId = %q, want it recovered from run_start", st.GraphID)
	}
	if strings.TrimSpace(st.Error) == "" {
		t.Errorf("abandoned run must record a non-empty reason in Error")
	}
	if st.Error == "abandoned" || st.Error == "lost" {
		t.Errorf("abandoned reason %q reads as generic, not a real explanation", st.Error)
	}
	if st.PendingApproval != nil || st.PendingAsk != nil {
		t.Errorf("an abandoned run must not still advertise a resolvable pending decision; got approval=%+v ask=%+v",
			st.PendingApproval, st.PendingAsk)
	}

	// The event stream, not just the status struct: the trail must
	// carry a durable EventRunAbandoned row recording why, ending on
	// that event.
	trace, err := impl.GetRunTrace(ctx, runID, 0)
	if err != nil {
		t.Fatalf("GetRunTrace: %v", err)
	}
	if len(trace) == 0 {
		t.Fatal("GetRunTrace returned no events for an abandoned run")
	}
	last := trace[len(trace)-1]
	if last.Kind != string(coreag.EventRunAbandoned) {
		t.Fatalf("last trace event kind = %q, want %q", last.Kind, coreag.EventRunAbandoned)
	}
	if !strings.Contains(last.Payload, "restart") {
		t.Errorf("run_abandoned payload = %q, want it to name the restart as the cause", last.Payload)
	}

	// Falsifiable in the direction that matters: resolving an
	// abandoned run must be refused, not silently succeed against a
	// pending decision that no longer exists.
	if err := impl.ResolveApproval(ctx, runID, nodeID, true, "too late"); err == nil {
		t.Errorf("ResolveApproval on an abandoned run must be refused")
	}
}

// TestRehydrateAbandonedRuns_IdempotentAcrossRepeatedBoots: a run
// already marked abandoned by an earlier reconciliation pass must be
// registered again (so it is still observable) WITHOUT appending a
// second EventRunAbandoned row — abandonment is a one-time durable
// fact, not a counter that grows on every subsequent boot.
func TestRehydrateAbandonedRuns_IdempotentAcrossRepeatedBoots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := coreag.NewMemoryEventLog()
	const runID = "run-abandoned-idempotent"

	var batch coreag.EventBatch
	if err := batch.AppendKind(runID, "a", coreag.EventApprovalPending, map[string]any{"prompt": "Ship it?"}); err != nil {
		t.Fatalf("AppendKind: %v", err)
	}
	if err := batch.AppendKind(runID, "", coreag.EventRunPaused, map[string]any{"reason": "approval: Ship it?"}); err != nil {
		t.Fatalf("AppendKind: %v", err)
	}
	if _, err := log.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Boot 1: reconciles and appends EventRunAbandoned.
	if _, err := graphview.NewManager(graphview.WithDataDir(t.TempDir()), graphview.WithEventLog(log)); err != nil {
		t.Fatalf("NewManager (boot 1): %v", err)
	}
	// Boot 2: reconciles the SAME run again (a second restart before
	// anyone acted on the abandonment).
	mgr2, err := graphview.NewManager(graphview.WithDataDir(t.TempDir()), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager (boot 2): %v", err)
	}

	st, err := graphview.New(mgr2).GetRunStatus(ctx, runID)
	if err != nil {
		t.Fatalf("GetRunStatus after second boot: %v", err)
	}
	if st.State != graphview.RunStateAbandoned {
		t.Fatalf("state after second boot = %q, want %q", st.State, graphview.RunStateAbandoned)
	}

	var abandonedCount int
	if err := log.Replay(runID, func(ev coreag.Event) error {
		if ev.Kind == coreag.EventRunAbandoned {
			abandonedCount++
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if abandonedCount != 1 {
		t.Fatalf("EventRunAbandoned appended %d times across two boots, want exactly 1", abandonedCount)
	}
}
