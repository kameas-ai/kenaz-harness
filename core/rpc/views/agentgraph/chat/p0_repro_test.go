package chat

// chat-turn-integrity-01PMZ606 WP01+WP03 — the P0 reproduction, now
// GREEN.
//
// This file was written in WP01 against unmodified HEAD, where it
// failed on a row count: a healthy 60-second-shaped turn wrote far more
// than (pre-turn + user + assistant) rows into session_messages,
// because runPeriodicFlush called the production PartialPersister —
// which INSERTed a fresh row every tick (core/rpc/api.go:4897-4910,
// core/session/manager.go:474-486) — instead of upserting a single
// checkpoint. The verbatim red output is recorded in
// kitty-specs/chat-turn-integrity-01PMZ606/research/p0-chain.md.
//
// WP02 (stream_checkpoints table + Store/Manager methods) and WP03
// (runPeriodicFlush writes a checkpoint via StreamCheckpointWriter
// instead of a PartialPersister) fixed the defect; this file was
// updated in WP03 to drive the new seam and now asserts the fixed
// shape.
//
// Fixture requirements this test still satisfies (spec.md §1.1.2,
// CLAUDE.md blind spot #2, tasks.md WP01/WP03):
//  1. Real sqlite (storagesqlite.Open + session.NewSQLStore), not
//     session.NewMemoryStore, which skips SQL encode/decode entirely
//     and is documented to hide this exact defect.
//  2. The REAL *session.Manager as the checkpoint writer (Manager's
//     UpsertStreamCheckpoint method satisfies StreamCheckpointWriter
//     directly — no adapter closure needed, unlike the old
//     PartialPersister two-call shape) — not fakeStreamCheckpointWriter
//     (partial_flush_test.go), which is the fixture class that hid the
//     P0 for five releases when it stood in for the production
//     closure. A fixture that leaves StreamCheckpoints nil never starts
//     the goroutine (chat_runner.go) and proves nothing (correction
//     C-1).
//  3. A short interval (~20ms) passed as runPeriodicFlush's interval
//     parameter so >=5 ticks land in a fast test, without touching the
//     production partialFlushInterval constant (10s).
//
// Vacuity guard: this test also asserts, independently of the final
// row count, that at least 5 UpsertStreamCheckpoint calls landed
// against the real database — i.e. that the periodic-flush path
// actually ran, not merely that a fixture that never started the
// goroutine happened to produce the right row count by construction. A
// counting wrapper around the real Manager (not a stub — every call
// still reaches real sqlite) makes this possible without reintroducing
// the fakeStreamCheckpointWriter-in-place-of-production-seam bypass.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// countingCheckpointWriter forwards every call to the real
// *session.Manager (a real sqlite UPSERT lands on every call — this is
// a spy, not a stub) while counting successful calls, so the vacuity
// guard can assert the flush loop actually ran without asserting on a
// fake's return value in place of the real seam's work (test doctrine
// rule 3, spec.md §8).
type countingCheckpointWriter struct {
	mgr *session.Manager
	n   atomic.Int64
}

func (w *countingCheckpointWriter) UpsertStreamCheckpoint(ctx context.Context, sessionID, subID, text string, hasTool bool) error {
	if err := w.mgr.UpsertStreamCheckpoint(ctx, sessionID, subID, text, hasTool); err != nil {
		return err
	}
	w.n.Add(1)
	return nil
}

// TestP0_HealthyTurnPollutesTranscript_AC001 is the WP01 reproduction,
// now green under WP03. It drives a simulated "healthy 60-second turn"
// through the REAL runPeriodicFlush + REAL *session.Manager checkpoint
// writer + REAL sqlite path and counts what lands in session_messages.
//
// Expected (post-fix, the state this test now pins): exactly 2 rows
// (one user prompt, one final assistant answer), zero with
// StreamingFailedAt set, and >=5 upserts into the new
// stream_checkpoints table instead.
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
	// meaningful and distinguishable from the checkpoint pollution this
	// test used to catch.
	if _, err := mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleUser,
		Content: "please do the thing",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	// --- 2. The REAL checkpoint writer: *session.Manager wrapped only
	// to count calls, wired through to real sqlite on every call. This
	// is deliberately NOT fakeStreamCheckpointWriter — using a fake in
	// its place is the exact bypass that hid the pre-fix defect
	// (spec.md §1.1.2 / CLAUDE.md blind spot #2). ---
	writer := &countingCheckpointWriter{mgr: mgr}

	// --- 3. Drive runPeriodicFlush with a short interval over a bridge
	// whose accumulated text keeps growing, simulating a live stream. ---
	broker := &recordingBroker{}
	subID := "sub-p0-repro"
	bridge := NewStreamBridge(broker, subID, sessionID)

	const interval = 20 * time.Millisecond
	const ticksWanted = 6 // >=5 required by WP01/AC-001

	runCtx, cancelRun := context.WithCancel(ctx)

	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		runPeriodicFlush(runCtx, sessionID, subID, bridge, writer, interval)
	}()

	// Grow the bridge's accumulated text faster than the flush
	// interval so every tick observes new content (partial_flush.go
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
	// kernel finishes, which is what stops the flush goroutine). This
	// test drives runPeriodicFlush directly rather than the full
	// ChatRunner, so it does not exercise driveRun's clean-close
	// checkpoint delete (AC-002/AC-003 cover that, against the real
	// driveRun path) — AC-001 is specifically about what lands in
	// session_messages, which this reproduces precisely.
	time.Sleep(interval * time.Duration(ticksWanted+2))
	cancelRun()
	flushWG.Wait()
	<-growthDone

	// The "different writer": on a clean ("completed") close, the real
	// answer is written by SessionWriteNode elsewhere in the kernel
	// pipeline (chat_runner.go), not by the flush writer. Reproduce
	// that single, correct write here.
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

	var streamingFailedRows int
	for _, m := range msgs {
		if m.StreamingFailedAt != nil {
			streamingFailedRows++
		}
	}

	checkpointUpserts := writer.n.Load()
	t.Logf("P0 repro: ListMessages returned %d rows (%d carry StreamingFailedAt); %d checkpoint upserts landed; want %d rows",
		len(msgs), streamingFailedRows, checkpointUpserts, wantRows)

	// Vacuity guard: prove the flush goroutine actually reached the
	// real database, independent of whatever the row count turns out to
	// be. Without this, a fixture that never starts the goroutine (e.g.
	// a nil StreamCheckpoints, per C-1) would trivially "pass" a bare
	// row-count assertion for the wrong reason.
	if checkpointUpserts < 5 {
		t.Fatalf("vacuity guard failed: only %d UpsertStreamCheckpoint calls landed (want >=5) — "+
			"the periodic-flush path did not demonstrably run against the real database",
			checkpointUpserts)
	}

	// AC-001: len(ListMessages) is an EXACT integer — pre-turn + user +
	// assistant — not a bound. Before WP02+WP03, this failed because
	// every periodic-flush tick INSERTed a fresh row
	// (core/session/manager.go:474-486 mints a new id; there was no
	// UPDATE path) instead of upserting a checkpoint in its own table.
	if len(msgs) != wantRows {
		t.Errorf("AC-001: len(ListMessages) = %d, want exactly %d "+
			"(pre-turn=0 + user=1 + assistant=1) — got %d extra row(s)",
			len(msgs), wantRows, len(msgs)-wantRows)
	}

	// Also required by AC-001: no row carries a non-nil
	// StreamingFailedAt, because checkpoints now live in their own
	// table and never touch session_messages on a healthy turn.
	if streamingFailedRows != 0 {
		t.Errorf("AC-001: %d row(s) carry a non-nil StreamingFailedAt after a turn that "+
			"ended reason==\"completed\" — want 0 (checkpoints must not live in "+
			"session_messages)", streamingFailedRows)
	}
}
