package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// TestSubagentPauseRegistry_PauseResumeIdempotency pins the
// changed-bool contract core/rpc/views/branches.API.PauseSubagent /
// ResumeSubagent rely on to decide whether to write a second audit
// record (subagent-control-and-background-tasks-01PMZB11 UNIT-8,
// AC-09's "one audit record, not two").
func TestSubagentPauseRegistry_PauseResumeIdempotency(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	const sid = "child-session-1"

	if r.IsPaused(sid) {
		t.Fatal("new registry must not report paused for an unknown session")
	}
	if changed := r.Pause(sid); !changed {
		t.Fatal("first Pause must report changed=true")
	}
	if !r.IsPaused(sid) {
		t.Fatal("IsPaused must be true after Pause")
	}
	if changed := r.Pause(sid); changed {
		t.Error("second Pause while already paused must report changed=false")
	}
	if changed := r.Resume(sid); !changed {
		t.Fatal("first Resume must report changed=true")
	}
	if r.IsPaused(sid) {
		t.Fatal("IsPaused must be false after Resume")
	}
	if changed := r.Resume(sid); changed {
		t.Error("second Resume while not paused must report changed=false")
	}
}

// TestSubagentPauseRegistry_NilSafe mirrors SubagentBudgetRegistry's
// nil-receiver-safety contract, which StartStream's unconditional
// wiring (sessionTurnPauseGate{reg: r.cfg.SubagentPause, ...}) depends
// on: a nil registry must never panic and must always behave as
// "never pauses".
func TestSubagentPauseRegistry_NilSafe(t *testing.T) {
	t.Parallel()
	var r *SubagentPauseRegistry
	if r.IsPaused("x") {
		t.Error("nil registry IsPaused must be false")
	}
	if changed := r.Pause("x"); changed {
		t.Error("nil registry Pause must report changed=false")
	}
	if changed := r.Resume("x"); changed {
		t.Error("nil registry Resume must report changed=false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx, "x"); err != nil {
		t.Errorf("nil registry Wait must return nil immediately, got %v", err)
	}
}

// TestSubagentPauseRegistry_EmptySessionIDNoOp mirrors
// SubagentBudgetRegistry's empty-key guard.
func TestSubagentPauseRegistry_EmptySessionIDNoOp(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	if changed := r.Pause(""); changed {
		t.Error("Pause(\"\") must report changed=false")
	}
	if r.IsPaused("") {
		t.Error("IsPaused(\"\") must be false")
	}
}

// TestSubagentPauseRegistry_WaitBlocksUntilResume is the registry-level
// half of AC-10's proof (the loop-executor half lives in
// core/agentgraph/exec_control_test.go): Wait must actually block while
// paused and unblock the instant Resume is called, with no polling
// delay.
func TestSubagentPauseRegistry_WaitBlocksUntilResume(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	const sid = "child-session-2"
	r.Pause(sid)

	done := make(chan error, 1)
	go func() { done <- r.Wait(context.Background(), sid) }()

	select {
	case <-done:
		t.Fatal("Wait returned before Resume was called")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked.
	}

	r.Resume(sid)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Wait returned %v after Resume, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Resume")
	}
}

// TestSubagentPauseRegistry_WaitUnblocksOnCtxCancel covers the Abort
// interaction: a cancelled ctx must unblock a parked Wait rather than
// hang forever (E-002: "mid-turn cost is not bounded by Pause ... that
// is Abort ... which is also what unblocks a Wait call").
func TestSubagentPauseRegistry_WaitUnblocksOnCtxCancel(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	const sid = "child-session-3"
	r.Pause(sid)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Wait(ctx, sid) }()

	select {
	case <-done:
		t.Fatal("Wait returned before ctx was cancelled")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Wait err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx cancellation did not unblock Wait")
	}

	// The registry itself is unaffected by ctx cancellation — the
	// session is still recorded as paused until Resume is called.
	if !r.IsPaused(sid) {
		t.Error("ctx cancellation must not implicitly resume the session")
	}
}

// TestSubagentPauseRegistry_WaitNoOpWhenNeverPaused pins the default:
// every interactive session's child never has an entry, so Wait never
// blocks.
func TestSubagentPauseRegistry_WaitNoOpWhenNeverPaused(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx, "never-paused"); err != nil {
		t.Errorf("Wait on an unpaused session must return nil immediately, got %v", err)
	}
}

// TestChatRunner_DriveRun_ReleasesPauseEntryOnAbort covers the
// registry-entry leak found in review of this PR: a sub-agent paused
// via PauseSubagent and then ABORTED rather than resumed used to leave
// its entry in SubagentPauseRegistry.paused forever — Wait returns
// ctx.Err() and the run's loop exits, but nothing ever called Resume
// to release the entry. driveRun's cleanup defer must release it
// unconditionally, mirroring how r.subs is already deleted there.
//
// minimalChatGraph is a single AskNode, so this test does not drive
// the run through a real Loop-node TurnPause consult; instead it arms
// the registry directly (the same state a genuine PauseSubagent RPC
// call produces while a run is in-flight), then aborts the run via
// StopStream and asserts the registry's own observable state — never
// a status field — shows the entry gone.
func TestChatRunner_DriveRun_ReleasesPauseEntryOnAbort(t *testing.T) {
	t.Parallel()
	const sessionID = "paused-then-aborted-session"

	reg := NewSubagentPauseRegistry()
	if changed := reg.Pause(sessionID); !changed {
		t.Fatal("Pause must report changed=true for a fresh session")
	}

	broker := &recordingBroker{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		SubagentPause: reg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	// Abort rather than resume — the leak's trigger condition.
	// StopStream cancels the run and blocks on <-sub.done, so by the
	// time it returns, driveRun's cleanup defer (where the fix lives)
	// has already run synchronously.
	if err := runner.StopStream(context.Background(), subID); err != nil {
		t.Fatalf("StopStream: %v", err)
	}

	if reg.IsPaused(sessionID) {
		t.Error("registry entry must be released when driveRun exits, even on abort-without-resume")
	}
	if changed := reg.Resume(sessionID); changed {
		t.Error("a subsequent Resume must report changed=false — the entry should already be gone")
	}
}

// TestSessionTurnPauseGate_AdaptsRegistryToEnvSeam checks the
// coreag.TurnPauseGate adapter StartStream installs on every Env.
func TestSessionTurnPauseGate_AdaptsRegistryToEnvSeam(t *testing.T) {
	t.Parallel()
	r := NewSubagentPauseRegistry()
	const sid = "child-session-4"
	gate := sessionTurnPauseGate{reg: r, sessionID: sid}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.Wait(ctx); err != nil {
		t.Fatalf("gate.Wait before any Pause = %v, want nil", err)
	}

	r.Pause(sid)
	done := make(chan error, 1)
	go func() { done <- gate.Wait(context.Background()) }()
	select {
	case <-done:
		t.Fatal("gate.Wait returned before Resume")
	case <-time.After(50 * time.Millisecond):
	}
	r.Resume(sid)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("gate.Wait after Resume = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate.Wait did not unblock after Resume")
	}
}
