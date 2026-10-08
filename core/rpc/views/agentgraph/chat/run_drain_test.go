package chat

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestChatRunner_DrainWaitsForRunExit_ShutdownCancels pins the item-6 fix
// (CI run 37705330106): Drain does not return while a run is in flight,
// Shutdown cancels the run and returns only after its exit path —
// including the stream-checkpoint delete — has finished, so the owner
// may close storage immediately afterwards.
// Mutation: drop `defer r.runs.done()`'s registration-first position (or
// the r.runs.add() in StartStream) and the post-Shutdown assertions fail.
func TestChatRunner_DrainWaitsForRunExit_ShutdownCancels(t *testing.T) {
	t.Parallel()
	llm := &blockingAfterTextLLM{
		deltas:  []string{"partial text"},
		reached: make(chan struct{}),
		proceed: make(chan struct{}), // never closed: the run blocks until cancelled
	}
	runner, _, mgr, sessionID := buildCheckpointRunner(t, llm)

	if err := runner.Drain(context.Background()); err != nil {
		t.Fatalf("Drain with no runs: %v", err)
	}
	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", testTurn("hi"))
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	select {
	case <-llm.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("LLM did not reach its blocking point")
	}
	if err := mgr.UpsertStreamCheckpoint(context.Background(), sessionID, subID, "partial text", false); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = runner.Drain(short)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain returned %v while a run was in flight, want DeadlineExceeded", err)
	}

	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := runner.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// The exit path has fully run: no live sub, checkpoint deleted.
	runner.mu.Lock()
	live := len(runner.subs)
	runner.mu.Unlock()
	if live != 0 {
		t.Fatalf("%d subs still registered after Shutdown", live)
	}
	if _, ok, err := mgr.GetStreamCheckpoint(context.Background(), sessionID, subID); err != nil || ok {
		t.Fatalf("checkpoint after Shutdown: ok=%v err=%v — want deleted before Shutdown returns", ok, err)
	}
	if err := runner.Shutdown(sctx); err != nil { // idempotent
		t.Fatalf("second Shutdown: %v", err)
	}
	var nilRunner *ChatRunner
	if err := nilRunner.Drain(sctx); err != nil {
		t.Fatalf("nil Drain: %v", err)
	}
}
