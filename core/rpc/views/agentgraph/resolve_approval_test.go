package agentgraph_test

// resolve_approval_test.go — approval-node-01PMZC12 UNIT-3/UNIT-4/UNIT-5
// RPC-surface coverage: Graph_ResolveApproval end to end (approve and
// reject complete the run), the cross-kind refusals (Resume vs
// ResolveApproval must each refuse the other's pending kind), the
// Cedar gate (deny blocks, allow does not), the no-watcher fail-closed
// timeout (AC-05), and auto_approve_window_seconds (AC-06).
//
// Per spec.md §9 rule 1, assertions read the event stream / audit
// record, not only res.Outputs — a test that only checks the port
// passes with a fabricated event still present.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cedar "github.com/cedar-policy/cedar-go"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	policycedar "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func unmarshalPayload(raw json.RawMessage, out any) error {
	return json.Unmarshal(raw, out)
}

// fakeCedarGate is a race-safe cedar.Gate stub. deny controls whether
// Evaluate returns Deny; every call is recorded so a test can assert
// the gate was actually consulted (or, for the no-watcher/auto-approve
// paths, that it was NOT).
type fakeCedarGate struct {
	mu      sync.Mutex
	deny    bool
	actions []string
}

func (g *fakeCedarGate) Evaluate(_ context.Context, principal cedar.EntityUID, action string, resource cedar.EntityUID, _ map[cedar.String]cedar.Value) policycedar.Decision {
	g.mu.Lock()
	g.actions = append(g.actions, action)
	deny := g.deny
	g.mu.Unlock()
	// Deny ONLY the action under test. Integration note: this fake
	// originally denied every action, which was correct against the
	// base it was written on. On the merged branch,
	// model-authored-graphs-01PMGA01 added a graph.run gate that
	// StartRun now consults — so a deny-everything fake blocked the run
	// before the approval node ever fired, and the test failed for a
	// reason that had nothing to do with approval resolution.
	//
	// Narrowing to ActionApprovalResolve keeps the assertion about the
	// thing the test names. Denying everything would also have hidden
	// the opposite defect: a resolution that succeeds because the run
	// never got far enough to need one.
	outcome := policycedar.Allow
	if deny && action == policycedar.ActionApprovalResolve {
		outcome = policycedar.Deny
	}
	return policycedar.Decision{
		Outcome:   outcome,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "fakeCedarGate",
	}
}

func (g *fakeCedarGate) calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.actions))
	copy(out, g.actions)
	return out
}

// fakeAuditEmitter is a race-safe contextaudit.Emitter stub.
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []contextaudit.Event
}

func (e *fakeAuditEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
	return nil
}

func (e *fakeAuditEmitter) snapshot() []contextaudit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contextaudit.Event, len(e.events))
	copy(out, e.events)
	return out
}

const resolveApprovalGraphYAML = `spec_version: "1"
id: resolve_approval_rpc
entrypoints: [a]
nodes:
  - id: a
    kind: approval
    attrs:
      approver_role: user
      prompt: "Ship it?"
`

// startPausedApprovalRun saves resolveApprovalGraphYAML and starts a
// run, polling GetRunStatus until it parks on the approval node (or the
// deadline elapses, in which case the test fails).
func startPausedApprovalRun(t *testing.T, a *graphview.Impl) (runID string, st graphview.RunStatus) {
	t.Helper()
	ctx := context.Background()
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "resolve_approval_rpc", YAML: resolveApprovalGraphYAML}, "user"); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "resolve_approval_rpc"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err = a.GetRunStatus(ctx, resp.RunID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State == graphview.RunStatePaused {
			return resp.RunID, st
		}
		if st.State == graphview.RunStateFailed {
			t.Fatalf("run failed pre-pause: %s", st.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not pause; state=%q", st.State)
	return "", graphview.RunStatus{}
}

func waitForTerminal(t *testing.T, a *graphview.Impl, runID string, timeout time.Duration) graphview.RunStatus {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	var st graphview.RunStatus
	for time.Now().Before(deadline) {
		var err error
		st, err = a.GetRunStatus(ctx, runID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State == graphview.RunStateCompleted || st.State == graphview.RunStateFailed {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q did not reach a terminal state within %s; last state=%q", runID, timeout, st.State)
	return st
}

// TestResolveApproval_ApproveCompletesRun is AC-02 at the RPC surface:
// resolving with approved:true completes the run.
func TestResolveApproval_ApproveCompletesRun(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	runID, st := startPausedApprovalRun(t, a)
	if st.PendingApproval == nil || st.PendingApproval.NodeID != "a" {
		t.Fatalf("pending approval = %+v", st.PendingApproval)
	}
	if st.PendingAsk != nil {
		t.Errorf("PendingAsk must stay nil for an approval pause; got %+v", st.PendingAsk)
	}
	if err := a.ResolveApproval(context.Background(), runID, "a", true, "looks good"); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	final := waitForTerminal(t, a, runID, 2*time.Second)
	if final.State != graphview.RunStateCompleted {
		t.Fatalf("post-resolve state=%q err=%q", final.State, final.Error)
	}
}

// TestResolveApproval_RejectCompletesRun is AC-03 at the RPC surface:
// resolving with approved:false also completes the run (the rejected
// port has nothing downstream in this graph, which is the point — a
// terminal rejection must not hang the run).
func TestResolveApproval_RejectCompletesRun(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	runID, _ := startPausedApprovalRun(t, a)
	if err := a.ResolveApproval(context.Background(), runID, "a", false, "not ready"); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	final := waitForTerminal(t, a, runID, 2*time.Second)
	if final.State != graphview.RunStateCompleted {
		t.Fatalf("post-resolve state=%q err=%q", final.State, final.Error)
	}
}

// TestResolveApproval_RefusesCrossKind is UNIT-3's "both arms" test:
// resolving an approval through Graph_Resume is refused, and resolving
// an ask through Graph_ResolveApproval is refused. Without both arms
// the pending-kind discriminator is decorative.
func TestResolveApproval_RefusesCrossKind(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()

	runID, _ := startPausedApprovalRun(t, a)
	if err := a.Resume(ctx, runID, "approved"); err == nil {
		t.Errorf("Resume on a pending APPROVAL must be refused, got nil error")
	} else if !strings.Contains(err.Error(), "pending approval") {
		t.Errorf("Resume refusal message = %q, want it to name the mismatch", err.Error())
	}
	// The run must still be resolvable through the correct verb
	// afterward — the refused Resume call must not have consumed or
	// corrupted the pending decision.
	if err := a.ResolveApproval(ctx, runID, "a", true, ""); err != nil {
		t.Fatalf("ResolveApproval after refused Resume: %v", err)
	}
	waitForTerminal(t, a, runID, 2*time.Second)

	askYAML := `spec_version: "1"
id: ask_cross_kind
entrypoints: [q]
nodes:
  - id: q
    kind: ask
    attrs:
      question: "Name?"
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "ask_cross_kind", YAML: askYAML}, "user"); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "ask_cross_kind"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var st graphview.RunStatus
	for time.Now().Before(deadline) {
		st, err = a.GetRunStatus(ctx, resp.RunID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State == graphview.RunStatePaused {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.State != graphview.RunStatePaused {
		t.Fatalf("ask run did not pause; state=%q", st.State)
	}
	if err := a.ResolveApproval(ctx, resp.RunID, "q", true, ""); err == nil {
		t.Errorf("ResolveApproval on a pending ASK must be refused, got nil error")
	} else if !strings.Contains(err.Error(), "pending ask") {
		t.Errorf("ResolveApproval refusal message = %q, want it to name the mismatch", err.Error())
	}
}

// TestResolveApproval_CedarDenyBlocksResolution: a Cedar deny must
// block the resolution — the run stays paused, no port is written, and
// re-resolving after the gate is opened still works (the denied call
// must not have consumed the pending decision).
func TestResolveApproval_CedarDenyBlocksResolution(t *testing.T) {
	t.Parallel()
	gate := &fakeCedarGate{deny: true}
	audit := &fakeAuditEmitter{}
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(t.TempDir()),
		graphview.WithCedarGate(gate),
		graphview.WithAuditEmitter(audit),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	runID, _ := startPausedApprovalRun(t, a)

	if err := a.ResolveApproval(context.Background(), runID, "a", true, "trying anyway"); err == nil {
		t.Fatalf("ResolveApproval must be refused by a Cedar deny")
	}
	// Membership, not position. This asserted calls[0] originally,
	// which held on the base it was written against. On the merged
	// branch model-authored-graphs-01PMGA01's graph.run gate is
	// consulted first by StartRun, so index 0 is "graph.run" and the
	// position check failed while the property it meant to test —
	// "the resolve verb consulted the gate" — was true all along.
	if !slices.Contains(gate.calls(), policycedar.ActionApprovalResolve) {
		t.Errorf("gate.calls() = %v, want it to contain %q", gate.calls(), policycedar.ActionApprovalResolve)
	}
	// Still paused — the denied call must not have mutated state.
	st, err := a.GetRunStatus(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRunStatus: %v", err)
	}
	if st.State != graphview.RunStatePaused || st.PendingApproval == nil {
		t.Fatalf("expected the run to still be paused with a pending approval after a Cedar deny; got state=%q pending=%+v", st.State, st.PendingApproval)
	}
	if events := audit.snapshot(); len(events) != 0 {
		t.Errorf("a denied resolution must not be audited as resolved; got %d events", len(events))
	}

	// Open the gate and confirm the SAME pending decision still resolves.
	gate.mu.Lock()
	gate.deny = false
	gate.mu.Unlock()
	if err := a.ResolveApproval(context.Background(), runID, "a", true, "now allowed"); err != nil {
		t.Fatalf("ResolveApproval after opening the gate: %v", err)
	}
	waitForTerminal(t, a, runID, 2*time.Second)
	if events := audit.snapshot(); len(events) != 1 {
		t.Fatalf("expected exactly one audit record for the allowed resolution; got %d", len(events))
	} else if events[0].Kind != contextaudit.KindApprovalResolved {
		t.Errorf("audit kind = %q, want %q", events[0].Kind, contextaudit.KindApprovalResolved)
	}
}

// TestApproval_NoWatcherFailsClosedWithinBoundedTime is AC-05: a run
// with nobody resolving its approval resolves to rejected within a
// bounded time, with a non-generic reason, and does not park forever.
// Falsification: setting WithApprovalTimeout(0) below reproduces the
// pre-UNIT-4 behaviour and this test times out waiting for a terminal
// state rather than failing an assertion — exactly the "must hang or
// time out, not pass" shape spec.md AC-05 requires of its mutation.
func TestApproval_NoWatcherFailsClosedWithinBoundedTime(t *testing.T) {
	t.Parallel()
	audit := &fakeAuditEmitter{}
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(t.TempDir()),
		graphview.WithApprovalTimeout(30*time.Millisecond),
		graphview.WithAuditEmitter(audit),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	runID, _ := startPausedApprovalRun(t, a)

	final := waitForTerminal(t, a, runID, 2*time.Second)
	if final.State != graphview.RunStateCompleted {
		t.Fatalf("no-watcher run: state=%q err=%q, want it to resolve (fail-closed) and complete", final.State, final.Error)
	}

	events := audit.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected exactly one audit record for the timeout resolution; got %d", len(events))
	}
	var payload contextaudit.ApprovalResolvedPayload
	if err := unmarshalPayload(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal audit payload: %v", err)
	}
	if payload.Approved {
		t.Errorf("no-watcher resolution must be a REJECT, got Approved=true")
	}
	if !payload.Auto {
		t.Errorf("no-watcher resolution must record auto=true")
	}
	if payload.Approver != "" {
		t.Errorf("no-watcher resolution must not name an approver; got %q", payload.Approver)
	}
	if strings.TrimSpace(payload.Reason) == "" {
		t.Errorf("no-watcher resolution must record a non-empty reason")
	}
	if payload.Reason == "rejected" || payload.Reason == "no" {
		t.Errorf("no-watcher reason %q reads as generic, not a real explanation", payload.Reason)
	}
}

// TestApproval_AutoApproveWindowSeconds is AC-06: window=0 must not
// auto-approve within a bounded check window (it "parks indefinitely"
// relative to auto-approval — the no-watcher axis is disabled here via
// WithApprovalTimeout(0) so the two axes are not conflated), and a
// positive window auto-approves within a bounded time and records
// auto:true.
func TestApproval_AutoApproveWindowSeconds(t *testing.T) {
	t.Parallel()

	t.Run("window=0 does not auto-approve", func(t *testing.T) {
		t.Parallel()
		mgr, err := graphview.NewManager(
			graphview.WithDataDir(t.TempDir()),
			graphview.WithApprovalTimeout(0), // isolate: no no-watcher fallback
		)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		a := graphview.New(mgr)
		yaml := `spec_version: "1"
id: auto_window_zero
entrypoints: [a]
nodes:
  - id: a
    kind: approval
    attrs:
      approver_role: user
      prompt: "Ship it?"
      auto_approve_window_seconds: 0
`
		ctx := context.Background()
		if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "auto_window_zero", YAML: yaml}, "user"); err != nil {
			t.Fatalf("SaveGraph: %v", err)
		}
		resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "auto_window_zero"})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		deadline := time.Now().Add(2 * time.Second)
		var st graphview.RunStatus
		for time.Now().Before(deadline) {
			st, err = a.GetRunStatus(ctx, resp.RunID)
			if err != nil {
				t.Fatalf("GetRunStatus: %v", err)
			}
			if st.State == graphview.RunStatePaused {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if st.State != graphview.RunStatePaused {
			t.Fatalf("did not pause; state=%q", st.State)
		}
		// Bounded wait, well past any timer that SHOULD have fired if
		// window=0 were mishandled as "auto-approve immediately".
		time.Sleep(150 * time.Millisecond)
		st, err = a.GetRunStatus(ctx, resp.RunID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State != graphview.RunStatePaused {
			t.Fatalf("window=0 must not auto-approve; state=%q", st.State)
		}
	})

	t.Run("positive window auto-approves and records auto:true", func(t *testing.T) {
		t.Parallel()
		audit := &fakeAuditEmitter{}
		mgr, err := graphview.NewManager(
			graphview.WithDataDir(t.TempDir()),
			graphview.WithApprovalTimeout(0), // isolate: no no-watcher fallback
			graphview.WithAuditEmitter(audit),
		)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		a := graphview.New(mgr)
		yaml := `spec_version: "1"
id: auto_window_positive
entrypoints: [a]
nodes:
  - id: a
    kind: approval
    attrs:
      approver_role: user
      prompt: "Ship it?"
      auto_approve_window_seconds: 1
`
		ctx := context.Background()
		if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "auto_window_positive", YAML: yaml}, "user"); err != nil {
			t.Fatalf("SaveGraph: %v", err)
		}
		resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "auto_window_positive"})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		final := waitForTerminal(t, a, resp.RunID, 3*time.Second)
		if final.State != graphview.RunStateCompleted {
			t.Fatalf("auto-approve window: state=%q err=%q", final.State, final.Error)
		}
		events := audit.snapshot()
		if len(events) != 1 {
			t.Fatalf("expected exactly one audit record; got %d", len(events))
		}
		var payload contextaudit.ApprovalResolvedPayload
		if err := unmarshalPayload(events[0].Payload, &payload); err != nil {
			t.Fatalf("unmarshal audit payload: %v", err)
		}
		if !payload.Approved {
			t.Errorf("positive window must auto-APPROVE, got Approved=false")
		}
		if !payload.Auto {
			t.Errorf("positive window resolution must record auto=true")
		}
	})
}
