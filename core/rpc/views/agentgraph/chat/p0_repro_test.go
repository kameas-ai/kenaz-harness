package chat

// chat-turn-integrity-01PMZ606 WP01 — the P0 reproduction.
//
// This file exists to become AC-001 once WP02+WP03 land. Run against
// unmodified HEAD (no production code in this mission has landed yet)
// it MUST fail on a row count: a healthy 60-second-shaped turn writes
// far more than (pre-turn + user + assistant) rows into
// session_messages, because runPeriodicFlush (partial_flush.go:66-69)
// calls the production PartialPersister — which INSERTs a fresh row
// every tick (core/rpc/api.go:4897-4910, core/session/manager.go:474-
// 486) — instead of updating a single checkpoint.
//
// Fixture requirements this test satisfies (spec.md §1.1.2, CLAUDE.md
// blind spot #2, tasks.md WP01):
//  1. Real sqlite (storagesqlite.Open + session.NewSQLStore), not
//     session.NewMemoryStore, which skips SQL encode/decode entirely
//     and is documented to hide this exact defect.
//  2. The REAL persister closure, reproduced verbatim from
//     core/rpc/api.go:4897-4910 (AppendMessage then
//     MarkStreamingFailure) — not fakePartialPersister
//     (partial_flush_test.go:14), which is the fixture that hid the
//     P0 for five releases. A fixture that leaves PartialPersister nil
//     never starts the goroutine (chat_runner.go:1221) and proves
//     nothing (correction C-1).
//  3. A short interval (~20ms) passed as runPeriodicFlush's fifth
//     parameter so >=5 ticks land in a fast test, without touching the
//     production partialFlushInterval constant (10s).
//
// Vacuity guard: this test also asserts, independently of the total
// row count, that at least 5 rows in the final ListMessages result
// carry a non-nil StreamingFailedAt — i.e. that the periodic-flush
// path actually reached the real database and stamped real streaming-
// failure columns. A fixture that never starts the flush goroutine
// (e.g. nil PartialPersister) would pass the row-count assertion only
// by accident; this guard forces the goroutine to have actually run.

import (
	"context"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestP0_HealthyTurnPollutesTranscript_AC001 is the WP01 reproduction.
// It drives a simulated "healthy 60-second turn" through the REAL
// runPeriodicFlush + REAL PartialPersister + REAL sqlite path and
// counts what lands in session_messages.
//
// Expected AFTER WP02+WP03 land: exactly 2 rows (one user prompt, one
// final assistant answer), zero with StreamingFailedAt set, and >=5
// upserts into the new stream_checkpoints table instead.
//
// Expected on unmodified HEAD (this run): 2 + N rows, where N is the
// number of periodic-flush ticks that observed new text (>=5 by
// construction), every one of them a growing prefix of the same
// answer, every one of them flagged as a streaming failure.
func TestP0_HealthyTurnPollutesTranscript_AC001(t *testing.T) {
	ctx := context.Background()

	// --- 1. Real sqlite, real session.Manager -------------------------
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	defer db.Close(context.Background())

	store := session.NewSQLStore(session.NewStorageDB(db))
	mgr := session.NewManager(store)

	rec, err := mgr.Create(ctx, "p0 repro session")
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}
	sessionID := rec.ID

	// The user's turn-opening message. Not part of the defect; it is
	// here so the expected post-fix count (user + assistant) is
	// meaningful and distinguishable from the checkpoint pollution.
	if _, err := mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleUser,
		Content: "please do the thing",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	// --- 2. The REAL production persister, reproduced verbatim from ---
	// core/rpc/api.go:4897-4910. This is deliberately NOT
	// fakePartialPersister — using the fake is the exact bypass that
	// hid this defect (spec.md §1.1.2 / CLAUDE.md blind spot #2).
	persister := PartialPersisterFunc(func(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
		stored, err := mgr.AppendMessage(ctx, sessionID, session.Message{
			Role:    session.RoleAssistant,
			Content: partialText,
		})
		if err != nil {
			return "", err
		}
		if merr := mgr.MarkStreamingFailure(ctx, sessionID, stored.ID, kind, recoverable); merr != nil {
			t.Logf("MarkStreamingFailure: %v", merr)
		}
		return stored.ID, nil
	})

	// --- 3. Drive runPeriodicFlush with a short interval over a bridge
	// whose accumulated text keeps growing, simulating a live stream. ---
	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-p0-repro", sessionID)

	const interval = 20 * time.Millisecond
	const ticksWanted = 6 // >=5 required by WP01/AC-001

	runCtx, cancelRun := context.WithCancel(ctx)

	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		runPeriodicFlush(runCtx, sessionID, bridge, persister, interval)
	}()

	// Grow the bridge's accumulated text faster than the flush
	// interval so every tick observes new content (partial_flush.go:59
	// skips a tick with no growth since the watermark).
	growthDone := make(chan struct{})
	go func() {
		defer close(growthDone)
		for i := 0; i < ticksWanted*4; i++ {
			bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "chunk "})
			time.Sleep(interval / 4)
		}
	}()

	// Let enough ticks land, then end the "turn" (reason == "completed"
	// in the real chat_runner: the run context is cancelled once the
	// kernel finishes, which is what stops the flush goroutine today —
	// nothing currently deletes the checkpoint on a clean close).
	time.Sleep(interval * time.Duration(ticksWanted+2))
	cancelRun()
	flushWG.Wait()
	<-growthDone

	// The "different writer": on a clean ("completed") close, the real
	// answer is written by SessionWriteNode elsewhere in the kernel
	// pipeline (chat_runner.go:1433-1435), not by the flush persister.
	// Reproduce that single, correct write here.
	finalText := "chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk " +
		"chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk"
	if _, err := mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleAssistant,
		Content: finalText,
	}); err != nil {
		t.Fatalf("append final assistant message: %v", err)
	}

	// --- 4. The assertion: an exact row count. -------------------------
	msgs, err := mgr.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	const wantRows = 2 // pre-turn(0) + user(1) + final assistant(1)

	var checkpointRows int
	for _, m := range msgs {
		if m.StreamingFailedAt != nil {
			checkpointRows++
		}
	}

	t.Logf("P0 repro: ListMessages returned %d rows (%d carry StreamingFailedAt); want %d",
		len(msgs), checkpointRows, wantRows)

	// Vacuity guard: prove the flush goroutine actually reached the
	// real database, independent of whatever the total count turns out
	// to be. Without this, a fixture that never starts the goroutine
	// (e.g. a nil PartialPersister, per C-1) would trivially "pass" a
	// bare row-count assertion for the wrong reason.
	if checkpointRows < 5 {
		t.Fatalf("vacuity guard failed: only %d rows carry StreamingFailedAt (want >=5) — "+
			"the periodic-flush path did not demonstrably run against the real database",
			checkpointRows)
	}

	// The actual AC-001 assertion. This is expected to fail on
	// unmodified HEAD: len(msgs) will be wantRows + checkpointRows,
	// not wantRows, because every periodic-flush tick INSERTs a fresh
	// row (core/session/manager.go:474-486 mints a new id; there is no
	// UPDATE path) instead of upserting a checkpoint.
	if len(msgs) != wantRows {
		t.Errorf("AC-001: len(ListMessages) = %d, want exactly %d "+
			"(pre-turn=0 + user=1 + assistant=1) — got %d extra row(s), "+
			"all periodic-flush checkpoints leaked into the transcript",
			len(msgs), wantRows, len(msgs)-wantRows)
	}

	// Also required by AC-001: no row should carry a non-nil
	// StreamingFailedAt once the fix lands (checkpoints move to their
	// own table). Recorded here so the failure output pins the
	// row-count reason, not just the count.
	if checkpointRows != 0 {
		t.Errorf("AC-001: %d row(s) carry a non-nil StreamingFailedAt after a turn that "+
			"ended reason==\"completed\" — want 0 (checkpoints must not live in "+
			"session_messages)", checkpointRows)
	}
}
