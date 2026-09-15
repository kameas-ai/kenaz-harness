package chat

// finding #105 — backend-error partial persist duplicates already-
// persisted moves.
//
// Before this fix, chat_runner.go's backend-error branch read
// sub.bridge.PartialState() (the WHOLE TURN's accumulated text) and
// handed it to PartialPersister.PersistPartial as a brand-new row. Every
// completed move of the turn is ALREADY its own persisted row
// (model-moves-transcript-01PMCH01 WP02), so on a turn that had already
// completed >=1 move before a later fire failed mid-stream, the partial
// row duplicated the earlier move's text — and the duplicate then fed
// the model's own words back to it as context on the very next turn.
//
// The Stop/cancel path (interrupt.go) was already fixed for exactly this
// shape: it persists bridge.PartialSegment() (the un-persisted tail)
// instead. This test proves the backend-error path now does the same.
//
// Real sqlite is used throughout (CLAUDE.md blind spot #2: a fixture
// built on session.NewMemoryStore(), or one that merely records the
// interface-call argument without ever reaching SQL, would not prove
// anything about what a reloaded session actually sees) — both the move
// journal's HistoryWriter and the PartialPersister are wired to the SAME
// real *session.Manager over a real sqlite database, mirroring the
// production adapters in core/rpc/api.go (llmHistoryWriter.AppendEntry
// and the partialPersister closure at api.go:6817-6834) rather than a
// bare recording fake.
//
// MUTATION EVIDENCE (documented; see the test body for how to run it):
// swapping the fixed call site back to sub.bridge.PartialState() makes
// this test fail on CONTENT — the persisted partial row's Content once
// again contains the first move's already-persisted text — not on some
// adjacent property like row count or ordering.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// ---- a two-fire registry: fire 1 completes (text + tool_use), fire 2
// streams new text and then fails mid-stream --------------------------

// dupProbeFailingStream is a corellm.Stream whose Events() channel
// yields the given deltas and whose Final() returns err — reproducing a
// provider connection drop AFTER some text already reached the surface
// (the shape long-turn-resilience-01KR3PRS WP03 exists for).
type dupProbeFailingStream struct {
	events chan corellm.StreamEvent
	err    error
}

func (s *dupProbeFailingStream) Events() <-chan corellm.StreamEvent { return s.events }
func (s *dupProbeFailingStream) Cancel() error                      { return nil }
func (s *dupProbeFailingStream) Final() (corellm.Response, error) {
	return corellm.Response{}, s.err
}

// dupProbeRegistry hands out exactly two fires: the first is a
// successful tool_use turn (its text becomes a persisted assistant_move
// the instant the tool call is recorded); the second streams a NEW
// segment of text and then dies in Final(), which is exactly the
// "backend-error after >=1 completed move" shape finding #105 describes.
type dupProbeRegistry struct {
	stubRegistry
	mu    sync.Mutex
	calls int
}

const (
	dupProbeFirstSegment  = "here is what I found in the first pass"
	dupProbeSecondSegment = "now let me check one more"
)

func (r *dupProbeRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	n := r.calls
	r.calls++
	r.mu.Unlock()

	switch n {
	case 0:
		ch := make(chan corellm.StreamEvent, 1)
		ch <- corellm.StreamEvent{Kind: corellm.StreamText, Text: dupProbeFirstSegment}
		close(ch)
		return &scriptedStream{
			events: ch,
			resp: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: dupProbeFirstSegment}},
				FinishReason: "tool_use",
				ToolCalls: []corellm.ToolUse{
					{ID: "tu-1", Name: "search__web", Input: []byte(`{"q":"first pass"}`)},
				},
			},
		}, nil
	case 1:
		ch := make(chan corellm.StreamEvent, 1)
		ch <- corellm.StreamEvent{Kind: corellm.StreamText, Text: dupProbeSecondSegment}
		close(ch)
		return &dupProbeFailingStream{
			events: ch,
			err:    errors.New("openrouter: stream read: transient provider error: connection reset by peer"),
		}, nil
	default:
		return nil, fmt.Errorf("dupProbeRegistry: unexpected fire %d", n)
	}
}

// sqliteHistoryWriter is a minimal stand-in for core/rpc/api.go's
// llmHistoryWriter, wired to a REAL *session.Manager over REAL sqlite.
// It exists here (rather than importing core/rpc, which would create an
// import cycle back into this package) so the test drives the same
// AppendTranscriptEntry seam production uses instead of a fake that
// merely records the call.
type sqliteHistoryWriter struct {
	mgr *session.Manager
}

func (w *sqliteHistoryWriter) AppendEntry(ctx context.Context, sessionID string,
	entry coreag.HistoryEntry) (string, error) {
	stored, err := w.mgr.AppendTranscriptEntry(ctx, sessionID, session.TranscriptEntry{
		Role:          session.Role(entry.Role),
		Content:       entry.Content,
		Kind:          session.MoveKind(entry.MoveKind),
		MoveIndex:     entry.MoveIndex,
		TurnSpanID:    entry.TurnSpanID,
		ModelToolArgs: entry.ModelToolArgs,
	})
	if err != nil {
		return "", err
	}
	return stored.ID, nil
}

// sqlitePartialPersister mirrors core/rpc/api.go's partialPersister
// closure (AppendMessage + MarkStreamingFailure) against the SAME real
// *session.Manager, so the row this test inspects is the actual
// production-shaped row, not a fake's recorded argument.
type sqlitePartialPersister struct {
	mgr *session.Manager
}

func (p *sqlitePartialPersister) PersistPartial(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
	stored, err := p.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleAssistant,
		Content: partialText,
	})
	if err != nil {
		return "", err
	}
	if merr := p.mgr.MarkStreamingFailure(ctx, sessionID, stored.ID, kind, recoverable); merr != nil {
		return stored.ID, merr
	}
	return stored.ID, nil
}

// TestChatRunner_BackendErrorPartialPersist_DoesNotDuplicateEarlierMoves
// is the finding #105 acceptance case.
//
// Turn shape: fire 1 streams dupProbeFirstSegment and calls a tool
// (kernelToolAdapter flushes fire 1's held text as a real, persisted
// assistant_move row the moment the tool_use is recorded). Fire 2 (after
// the tool result) streams dupProbeSecondSegment and then dies in
// Final(). driveRun observes reason=="backend-error" with >=1 completed
// move already on disk.
//
// Assertions:
//  1. The persisted partial row's Content is EXACTLY
//     dupProbeSecondSegment — the un-persisted tail — never the first
//     segment's text, concatenated or otherwise.
//  2. dupProbeFirstSegment still appears exactly once across the whole
//     session (as its own assistant_move row) — proving the fix didn't
//     just drop the first segment, it kept it where it already was and
//     stopped writing it a second time.
func TestChatRunner_BackendErrorPartialPersist_DoesNotDuplicateEarlierMoves(t *testing.T) {
	ctx := context.Background()

	// --- real sqlite, real session.Manager --------------------------------
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

	rec, err := mgr.Create(ctx, "finding-105 repro")
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}
	sessionID := rec.ID

	historyWriter := &sqliteHistoryWriter{mgr: mgr}
	persister := &sqlitePartialPersister{mgr: mgr}

	reg := &dupProbeRegistry{}
	pool := &scriptedPool{entries: []ToolEntry{{Server: "search", Name: "web"}}}
	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)

	runner, err := New(Config{
		Kernel:           coreag.NewKernel(),
		Registry:         reg,
		Pool:             pool,
		Broker:           broker,
		HistoryWriter:    historyWriter,
		History:          staticHistoryReader{},
		GraphLoader:      func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:         func() int { return 25 },
		PartialPersister: persister,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(ctx, "profile-1", sessionID, "", "find it, then check again"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Fatalf("expected the run to die with reason=backend-error, got %q (msg=%q)", closed.Reason, closed.Message)
	}
	if closed.PartialMessageID == "" {
		t.Fatalf("closed payload carries no PartialMessageID — PartialPersister was not invoked; nothing to assert")
	}

	msgs, err := mgr.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	var partialRow *session.Message
	firstSegmentOccurrences := 0
	for i := range msgs {
		m := &msgs[i]
		if m.ID == closed.PartialMessageID {
			partialRow = m
		}
		if strings.Contains(m.Content, dupProbeFirstSegment) {
			firstSegmentOccurrences++
		}
	}
	if partialRow == nil {
		t.Fatalf("PartialMessageID %q from the closed payload was not found in ListMessages(%q): %+v",
			closed.PartialMessageID, sessionID, msgs)
	}

	// --- the finding #105 assertion: no duplication -----------------------
	if partialRow.Content != dupProbeSecondSegment {
		t.Errorf("partial row Content = %q, want exactly %q (the un-persisted tail only)\n"+
			"if this contains %q, the fix regressed to whole-turn PartialState() "+
			"and duplicated the already-persisted first move",
			partialRow.Content, dupProbeSecondSegment, dupProbeFirstSegment)
	}
	if strings.Contains(partialRow.Content, dupProbeFirstSegment) {
		t.Errorf("partial row Content %q contains the FIRST segment's text (%q) — "+
			"finding #105 regression: the already-persisted move was duplicated into the new row",
			partialRow.Content, dupProbeFirstSegment)
	}

	// --- the fix must not have discarded the first segment either --------
	if firstSegmentOccurrences != 1 {
		t.Errorf("dupProbeFirstSegment appears in %d row(s) across the session, want exactly 1 "+
			"(its own persisted assistant_move) — got: %+v", firstSegmentOccurrences, msgs)
	}

	if partialRow.StreamingFailedAt == nil {
		t.Errorf("partial row has no StreamingFailedAt set; want the backend-error classification stamped")
	}
}
