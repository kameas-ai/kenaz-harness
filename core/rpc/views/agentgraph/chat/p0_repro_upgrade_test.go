package chat

// chat-turn-integrity-01PMZ606 WP03 — AC-004: the P0 fix must hold on a
// database a PREVIOUS release produced, not just on a fresh empty one
// (CLAUDE.md blind spot #3: "a migration that has never run against
// populated tables has never been tested").
//
// This is TestP0_HealthyTurnPollutesTranscript_AC001's own logic,
// re-run against a database booted from
// core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql instead of a
// freshly-opened directory. It drives the REAL runPeriodicFlush and
// the real *session.Manager checkpoint writer — NOT a hand-rolled
// simulation via direct Store calls — so that reverting WP03
// (partial_flush.go / chat_runner.go / core/rpc/api.go back to calling
// PartialPersister on every tick) makes THIS test fail too, not just
// AC-001. A version of this test that only exercised
// Manager.UpsertStreamCheckpoint directly would keep passing after
// such a revert, because that store method is untouched by WP03 — it
// is the WP03 CALL SITE (runPeriodicFlush) that regresses, so the test
// must drive that call site.
//
// Falsifiability (spec.md §10, tasks.md WP03): "revert WP03 and delete
// AC-001. The upgrade-path assertion must STILL fail. If it passes, it
// is starting from an empty database and testing nothing." Per test
// doctrine rule 2 (spec.md §8), this reuses upgradesnap.Materialize —
// the same materialiser TestUpgradePath uses — rather than hand-rolling
// a second one.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// v0.64.0DumpPath locates the upgrade-path snapshot this mission's
// AC-004 must boot from. Relative to this package
// (core/rpc/views/agentgraph/chat), core/storage/sqlite/testdata is
// four directories up.
const v0640DumpRelPath = "../../../../storage/sqlite/testdata/upgrade/v0.64.0/dump.sql"

// TestP0_HealthyTurnPollutesTranscript_AC004_UpgradePath is AC-004. See
// the file doc comment for why it drives runPeriodicFlush directly
// (the real WP03 call site) rather than the underlying store methods.
func TestP0_HealthyTurnPollutesTranscript_AC004_UpgradePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(v0640DumpRelPath)
	if err != nil {
		t.Fatalf("read v0.64.0 dump.sql: %v", err)
	}

	rawPath := filepath.Join(dir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(rawPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.64.0 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open on the v0.64.0 snapshot failed: %v", err)
	}
	defer func() { _ = db.Close(context.Background()) }()

	pending, err := db.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() after Open = %d entries, want 0 (migration sessions/0336 must have applied)", len(pending))
	}

	store := session.NewSQLStore(session.NewStorageDB(db))
	mgr := session.NewManager(store)

	rec, err := mgr.Create(ctx, "AC-004 upgrade-path turn")
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}
	sessionID := rec.ID

	if _, err := mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleUser,
		Content: "please do the thing",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	// The REAL checkpoint writer + REAL runPeriodicFlush — the exact
	// production call site WP03 changed. mgr satisfies
	// chat.StreamCheckpointWriter directly (no adapter closure), same
	// as core/rpc/api.go's production wiring.
	writer := &countingCheckpointWriter{mgr: mgr}

	broker := &recordingBroker{}
	subID := "sub-ac004-upgrade"
	bridge := NewStreamBridge(broker, subID, sessionID)

	const interval = 20 * time.Millisecond
	const ticksWanted = 6

	runCtx, cancelRun := context.WithCancel(ctx)
	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		runPeriodicFlush(runCtx, sessionID, subID, bridge, writer, interval)
	}()

	growthDone := make(chan struct{})
	go func() {
		defer close(growthDone)
		for i := 0; i < ticksWanted*4; i++ {
			bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "chunk "})
			time.Sleep(interval / 4)
		}
	}()

	time.Sleep(interval * time.Duration(ticksWanted+2))
	cancelRun()
	flushWG.Wait()
	<-growthDone

	finalText := "chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk " +
		"chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk chunk"
	if _, err := mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleAssistant,
		Content: finalText,
	}); err != nil {
		t.Fatalf("append final assistant message: %v", err)
	}
	// Clean-close clear, reproduced here the same way driveRun's
	// deferred block does it in production.
	if err := mgr.DeleteStreamCheckpoint(ctx, sessionID, subID); err != nil {
		t.Fatalf("DeleteStreamCheckpoint: %v", err)
	}

	msgs, err := mgr.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	const wantRows = 2 // user=1 + final assistant=1

	var streamingFailedRows int
	for _, m := range msgs {
		if m.StreamingFailedAt != nil {
			streamingFailedRows++
		}
	}

	checkpointUpserts := writer.n.Load()
	t.Logf("AC-004 (upgrade path): ListMessages returned %d rows (%d carry StreamingFailedAt); %d checkpoint upserts landed; want %d rows",
		len(msgs), streamingFailedRows, checkpointUpserts, wantRows)

	if checkpointUpserts < 5 {
		t.Fatalf("vacuity guard failed: only %d UpsertStreamCheckpoint calls landed against the upgraded database (want >=5)", checkpointUpserts)
	}

	if len(msgs) != wantRows {
		t.Errorf("AC-004: len(ListMessages) on the upgraded database = %d, want exactly %d "+
			"(user=1 + assistant=1) — got %d extra row(s)",
			len(msgs), wantRows, len(msgs)-wantRows)
	}
	if streamingFailedRows != 0 {
		t.Errorf("AC-004: %d row(s) carry a non-nil StreamingFailedAt on the upgraded database after a "+
			"turn that ended cleanly — want 0", streamingFailedRows)
	}

	if _, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, subID); err != nil {
		t.Fatalf("GetStreamCheckpoint after delete: %v", err)
	} else if ok {
		t.Error("AC-004: checkpoint still present after clean close on the upgraded database — want deleted")
	}
}
