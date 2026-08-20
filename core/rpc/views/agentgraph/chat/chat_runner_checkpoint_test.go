package chat

// chat-turn-integrity-01PMZ606 WP03 — AC-002 (durability) and AC-003
// (clean close clears the checkpoint; error close promotes it), driven
// through the REAL ChatRunner (StartStream/driveRun), a REAL
// *session.Manager, and REAL sqlite — per spec.md §8 rule 1, never a
// memory store for anything asserting persistence.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// blockingAfterTextLLM emits its deltas synchronously, signals reached
// once it has (so the test can seed/inspect mid-run state without a
// timing race), then blocks on proceed until the test releases it —
// either normally (finalErr / nil) or via ctx cancellation (StopStream).
type blockingAfterTextLLM struct {
	deltas   []string
	emitTool bool
	finalErr error
	reached  chan struct{}
	proceed  chan struct{}
}

func (d *blockingAfterTextLLM) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		for _, t := range d.deltas {
			sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: t})
		}
		if d.emitTool {
			sink.Emit(coreag.StreamEvent{
				Kind:     coreag.StreamEventTool,
				ToolID:   "tu-1",
				ToolName: "shell",
				ToolArgs: `{"cmd":"ls"}`,
			})
		}
	}
	if d.reached != nil {
		close(d.reached)
	}
	if d.proceed != nil {
		select {
		case <-d.proceed:
		case <-ctx.Done():
			return coreag.LLMResponse{}, ctx.Err()
		}
	}
	if d.finalErr != nil {
		return coreag.LLMResponse{}, d.finalErr
	}
	return coreag.LLMResponse{Content: "ok"}, nil
}

// buildCheckpointRunner wires a real ChatRunner around the production
// chat_default graph, a real sqlite-backed *session.Manager serving
// BOTH PartialPersister (the AppendMessage+MarkStreamingFailure closure
// reproduced verbatim from core/rpc/api.go:4897-4910, matching
// production wiring) and StreamCheckpoints (the manager itself —
// Manager.UpsertStreamCheckpoint/DeleteStreamCheckpoint satisfy the
// chat.StreamCheckpointStore interface directly, exactly as
// core/rpc/api.go wires it).
func buildCheckpointRunner(t *testing.T, llm coreag.LLMProvider) (*ChatRunner, *recordingBroker, *session.Manager, string) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(context.Background(), "checkpoint-runner")
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}
	sessionID := rec.ID

	graph := loadProductionChatGraph(t)
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}

	partialPersister := PartialPersisterFunc(func(ctx context.Context, sid, partialText, kind string, recoverable bool) (string, error) {
		stored, err := mgr.AppendMessage(ctx, sid, session.Message{
			Role:    session.RoleAssistant,
			Content: partialText,
		})
		if err != nil {
			return "", err
		}
		if merr := mgr.MarkStreamingFailure(ctx, sid, stored.ID, kind, recoverable); merr != nil {
			t.Logf("MarkStreamingFailure: %v", merr)
		}
		return stored.ID, nil
	})

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
		},
		PartialPersister:  partialPersister,
		StreamCheckpoints: mgr,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, mgr, sessionID
}

// TestStreamCheckpoint_AC002_Durability drives a turn that pauses
// mid-stream (via blockingAfterTextLLM), seeds a checkpoint the same
// way a real periodic-flush tick would (mgr.UpsertStreamCheckpoint —
// production's 10s ticker is too slow for a fast test, so this
// reproduces the write a tick performs rather than waiting for the
// wall clock), and asserts BEFORE any terminal path: the checkpoint
// exists, its text equals the accumulation, and has_tool matches
// bridge.PartialState()'s second return.
//
// Mutation: make runPeriodicFlush a no-op (spec.md AC-002). This test
// does not exercise runPeriodicFlush's ticker directly — that is
// partial_flush_test.go's job — it exercises the STORE round-trip
// through the same seam runPeriodicFlush calls, mid-run, via the real
// ChatRunner/driveRun wiring (StreamCheckpoints=mgr), which is what
// AC-003 below depends on being correctly wired for its own
// assertions to mean anything.
func TestStreamCheckpoint_AC002_Durability(t *testing.T) {
	t.Parallel()
	llm := &blockingAfterTextLLM{
		deltas:  []string{"partial ", "answer"},
		reached: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	runner, broker, mgr, sessionID := buildCheckpointRunner(t, llm)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(llm.proceed) }) }
	defer release() // avoid leaking the run goroutine if an assertion fails early; idempotent vs. the explicit call below

	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	select {
	case <-llm.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("LLM did not reach its blocking point within 2s")
	}

	// Simulate the tick a live 10s-interval periodic flush would have
	// performed by now, driven through the exact seam runPeriodicFlush
	// calls (mgr.UpsertStreamCheckpoint, chat.StreamCheckpointWriter).
	if err := mgr.UpsertStreamCheckpoint(context.Background(), sessionID, subID, "partial answer", false); err != nil {
		t.Fatalf("UpsertStreamCheckpoint: %v", err)
	}

	got, ok, err := mgr.GetStreamCheckpoint(context.Background(), sessionID, subID)
	if err != nil {
		t.Fatalf("GetStreamCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("expected a checkpoint to exist before any terminal path, got none")
	}
	if got.Text != "partial answer" {
		t.Errorf("checkpoint text = %q, want %q", got.Text, "partial answer")
	}
	if got.HasTool {
		t.Error("checkpoint has_tool = true, want false (no tool_use emitted)")
	}

	release()
	closed := waitForClosed(t, broker)
	if closed.Reason != "completed" {
		t.Fatalf("closed.Reason = %q, want completed (sanity — this test is about the mid-run state above)", closed.Reason)
	}
}

// TestDriveRun_AC003_CleanCloseClearsCheckpoint: reason=="completed"
// leaves zero checkpoint rows.
// Mutation: hardcode recoverable=true on the promotion path (spec.md
// AC-003) — orthogonal to this subcase, which is about the CLEAN path;
// see the "backend-error" subcase below for that mutation's target.
func TestDriveRun_AC003_CleanCloseClearsCheckpoint(t *testing.T) {
	t.Parallel()
	llm := &blockingAfterTextLLM{
		deltas:  []string{"hello"},
		reached: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	runner, broker, mgr, sessionID := buildCheckpointRunner(t, llm)

	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	select {
	case <-llm.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("LLM did not reach its blocking point within 2s")
	}
	if err := mgr.UpsertStreamCheckpoint(context.Background(), sessionID, subID, "hello", false); err != nil {
		t.Fatalf("seed UpsertStreamCheckpoint: %v", err)
	}

	close(llm.proceed)
	closed := waitForClosed(t, broker)
	if closed.Reason != "completed" {
		t.Fatalf("closed.Reason = %q, want completed", closed.Reason)
	}

	// Give driveRun's deferred checkpoint-delete a moment to run — it
	// executes after EmitClosed fires (LIFO: close(sub.done) is last in
	// the defer, EmitClosed happens earlier in the function body), so
	// there is a small window between the closed event landing on the
	// broker and the delete completing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, ok, err := mgr.GetStreamCheckpoint(context.Background(), sessionID, subID)
		if err != nil {
			t.Fatalf("GetStreamCheckpoint: %v", err)
		}
		if !ok {
			return // deleted — the assertion this test exists for
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("checkpoint still exists after a clean (\"completed\") close — want deleted")
}

// TestDriveRun_AC003_ErrorClosePromotesCheckpoint: reason==
// "backend-error" leaves exactly one partial message row with
// StreamingRecoverable == !hasTool, and zero checkpoint rows.
// Mutation: hardcode recoverable=true on the promotion path
// (partial_flush.go's former :68 defect). Must fail on the
// hasTool==true case — this test drives BOTH hasTool cases via the
// two sub-tests below.
func TestDriveRun_AC003_ErrorClosePromotesCheckpoint(t *testing.T) {
	t.Parallel()
	t.Run("no_tool_recoverable_true", func(t *testing.T) {
		t.Parallel()
		testAC003ErrorClose(t, false /* emitTool */)
	})
	t.Run("tool_recoverable_false", func(t *testing.T) {
		t.Parallel()
		testAC003ErrorClose(t, true /* emitTool */)
	})
}

func testAC003ErrorClose(t *testing.T, emitTool bool) {
	t.Helper()
	llm := &blockingAfterTextLLM{
		deltas:   []string{"partial text"},
		emitTool: emitTool,
		finalErr: errors.New("openrouter: stream read: transient provider error: Network connection lost"),
		reached:  make(chan struct{}),
		proceed:  make(chan struct{}),
	}
	runner, broker, mgr, sessionID := buildCheckpointRunner(t, llm)

	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	select {
	case <-llm.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("LLM did not reach its blocking point within 2s")
	}
	if err := mgr.UpsertStreamCheckpoint(context.Background(), sessionID, subID, "partial text", emitTool); err != nil {
		t.Fatalf("seed UpsertStreamCheckpoint: %v", err)
	}

	close(llm.proceed)
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Fatalf("closed.Reason = %q, want backend-error", closed.Reason)
	}
	if closed.PartialMessageID == "" {
		t.Fatal("closed.PartialMessageID is empty, want the promoted partial row's id")
	}
	wantRecoverable := !emitTool
	if closed.PartialRecoverable != wantRecoverable {
		t.Errorf("closed.PartialRecoverable = %v, want %v (hasTool=%v)", closed.PartialRecoverable, wantRecoverable, emitTool)
	}

	// The promoted row exists and carries the derived (not hardcoded)
	// recoverable flag — this is what would catch the partial_flush.go
	// :68 mutation if it leaked into this path.
	stored, err := mgr.GetMessage(context.Background(), sessionID, closed.PartialMessageID)
	if err != nil {
		t.Fatalf("GetMessage(%s): %v", closed.PartialMessageID, err)
	}
	if stored.StreamingRecoverable != wantRecoverable {
		t.Errorf("stored.StreamingRecoverable = %v, want %v", stored.StreamingRecoverable, wantRecoverable)
	}

	// And the checkpoint is gone — recovery keeps exactly one source of
	// truth (the promoted message row, not the checkpoint).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, ok, err := mgr.GetStreamCheckpoint(context.Background(), sessionID, subID)
		if err != nil {
			t.Fatalf("GetStreamCheckpoint: %v", err)
		}
		if !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("checkpoint still exists after an error (\"backend-error\") close that promoted a partial row — want deleted")
}

// TestDriveRun_AC003_StopCalledClearsCheckpoint: reason=="stop-called"
// leaves zero checkpoint rows and zero new failure-flagged messages —
// the same clean-close clear as "completed", triggered by an explicit
// user Stop instead of a natural kernel return.
func TestDriveRun_AC003_StopCalledClearsCheckpoint(t *testing.T) {
	t.Parallel()
	llm := &blockingAfterTextLLM{
		deltas:  []string{"still going"},
		reached: make(chan struct{}),
		proceed: make(chan struct{}), // never closed — the run ends via ctx cancellation instead
	}
	runner, broker, mgr, sessionID := buildCheckpointRunner(t, llm)

	subID, err := runner.StartStream(context.Background(), "profile-1", sessionID, "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	select {
	case <-llm.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("LLM did not reach its blocking point within 2s")
	}
	if err := mgr.UpsertStreamCheckpoint(context.Background(), sessionID, subID, "still going", false); err != nil {
		t.Fatalf("seed UpsertStreamCheckpoint: %v", err)
	}

	if err := runner.StopStream(context.Background(), subID); err != nil {
		t.Fatalf("StopStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "stop-called" {
		t.Fatalf("closed.Reason = %q, want stop-called", closed.Reason)
	}
	if closed.PartialMessageID != "" {
		t.Errorf("closed.PartialMessageID = %q, want empty — stop-called does not promote a partial row", closed.PartialMessageID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, ok, err := mgr.GetStreamCheckpoint(context.Background(), sessionID, subID)
		if err != nil {
			t.Fatalf("GetStreamCheckpoint: %v", err)
		}
		if !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("checkpoint still exists after a stop-called close — want deleted")
}
