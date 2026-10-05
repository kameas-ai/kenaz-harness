package agentgraph_test

// agentgraph-settings-linkage-01DOGF0D delta review NEW-1: two legitimate
// paths write a SECOND run_start under one run id —
//   - Kernel.Resume re-enters Run after an ask/approval pause;
//   - the chat runner's overflow-recovery redrive calls Kernel.Run again
//     with the same env (chat_runner.go, attemptOverflowRecovery).
// Both are one run continuing. An earlier fix refused every run id with
// more than one run_start as "reused", which rejected every resumed and
// every redriven run, and statusFromLog kept the first attempt's
// node_error so a live redrive read "failed" (and RunView stopped
// polling). These drive the REAL kernel on the SQL event log.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func TestResumedRun_MaterializesAsOneContinuingRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := openSQLEventLog(t)
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()
	yaml := `spec_version: "1"
id: ask_resume
entrypoints: [q]
nodes:
  - id: q
    kind: ask
    attrs:
      question: "What is your name?"
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "ask_resume", YAML: yaml}, "user"); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "ask_resume"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitState(t, a, resp.RunID, graphview.RunStatePaused)
	if err := a.Resume(ctx, resp.RunID, "Alice"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitState(t, a, resp.RunID, graphview.RunStateCompleted)

	if n := countStarts(t, log, resp.RunID); n < 2 {
		t.Fatalf("resumed run has %d run_start events — the fixture no longer exercises the resume shape", n)
	}
	if _, err := a.MaterializeRun(ctx, resp.RunID); err != nil {
		t.Fatalf("MaterializeRun(resumed run): %v — a resume is a continuation, not run-id reuse", err)
	}
	// After a restart (fresh manager, same log + library) the status is
	// read from the log and reflects the resumed completion.
	fresh, err := graphview.NewManager(graphview.WithDataDir(dir), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager (restart): %v", err)
	}
	st, err := graphview.New(fresh).GetRunStatus(ctx, resp.RunID)
	if err != nil || st.State != graphview.RunStateCompleted {
		t.Fatalf("status after restart = %+v, %v; want completed", st, err)
	}
}

// flakyTransform fails node "second" on its first fire and blocks it on
// the next one until released, so the test can observe the redrive while
// it is in flight.
type flakyTransform struct {
	fires   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *flakyTransform) Kind() coreag.NodeKind { return coreag.NodeKindTransform }

func (f *flakyTransform) Execute(ctx context.Context, _ *coreag.Env, node *coreag.Node, _ coreag.PortValues) (coreag.Result, error) {
	if node.ID == "second" {
		if f.fires.Add(1) == 1 {
			return coreag.Result{}, errors.New("context_length_exceeded (simulated overflow)")
		}
		f.once.Do(func() { close(f.entered) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return coreag.Result{}, ctx.Err()
		}
	}
	return coreag.Result{Outputs: coreag.PortValues{"out": "x"}}, nil
}

func TestOverflowRedrive_StatusRunningThenCompleted_AndMaterializes(t *testing.T) {
	t.Parallel()
	log := openSQLEventLog(t)
	ex := &flakyTransform{entered: make(chan struct{}), release: make(chan struct{})}
	k := coreag.NewKernel(coreag.WithEventLog(log), coreag.WithExecutor(ex))
	mgr, err := graphview.NewManager(graphview.WithKernel(k), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	const runID = "chat-01J00000000000000REDRIVE1"
	g := materializeRPCGraph()
	env := &coreag.Env{RunID: runID, SessionID: "sess-1", Graph: &g}
	mgr.TrackExternalRun(runID, g) // what StartStream does

	// Attempt 1 dies on the overflow, exactly as the chat runner sees it.
	if err := k.Run(ctx, env); err == nil {
		t.Fatal("attempt 1 should fail")
	}
	// The redrive: the SAME env, Kernel.Run again (chat_runner.go).
	done := make(chan error, 1)
	go func() { done <- k.Run(ctx, env) }()
	select {
	case <-ex.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("redrive never re-fired the failed node")
	}
	st, err := a.GetRunStatus(ctx, runID)
	if err != nil {
		t.Fatalf("GetRunStatus mid-redrive: %v", err)
	}
	if st.State != graphview.RunStateRunning {
		t.Errorf("mid-redrive status = %q (err %q), want running — the first attempt's node_error is not this attempt's outcome", st.State, st.Error)
	}
	close(ex.release)
	if err := <-done; err != nil {
		t.Fatalf("redrive: %v", err)
	}
	st, err = a.GetRunStatus(ctx, runID)
	if err != nil || st.State != graphview.RunStateCompleted {
		t.Fatalf("post-redrive status = %+v, %v; want completed", st, err)
	}
	if n := countStarts(t, log, runID); n != 2 {
		t.Fatalf("redriven run has %d run_start events, want 2", n)
	}
	if _, err := a.MaterializeRun(ctx, runID); err != nil {
		t.Fatalf("MaterializeRun(redriven run): %v — a redrive is a continuation, not run-id reuse", err)
	}
}

func waitState(t *testing.T, a graphview.API, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var st graphview.RunStatus
	for time.Now().Before(deadline) {
		st, _ = a.GetRunStatus(context.Background(), runID)
		if st.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s state = %q (err %q), want %q", runID, st.State, st.Error, want)
}

func countStarts(t *testing.T, log coreag.EventLog, runID string) int {
	t.Helper()
	n := 0
	if err := log.Replay(runID, func(ev coreag.Event) error {
		if ev.Kind == coreag.EventRunStart {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return n
}
