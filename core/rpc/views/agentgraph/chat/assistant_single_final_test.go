package chat

// chat-single-writer-01DOGF0G WP04 — the assistant-side twin of dogfood
// F12: "a kind-less assistant row duplicating the turn's final
// assistant_move, rendering as an extra assistant bubble".
//
// ATTRIBUTION. None of spec §2.6's three candidates is the writer. The
// dev-profile rows (docs/missions/chat-single-writer.md §1.2) are all
// kind-less rows carrying streaming_failed_at — the signature of the
// backend-error PartialPersister path (driveRun -> api.go partialPersister:
// AppendMessage + MarkStreamingFailure). That path persists
// bridge.PartialSegment(), "the text streamed since the last move
// boundary" — but a segment the move journal already OWNS is not
// un-persisted:
//
//   - parked: the last chat fire completed (RecordAssistantMove parked its
//     text) and a LATER call failed — on the routed graph that is
//     exit_gate's verdict call, which always runs after the last chat
//     fire. PersistPartial wrote the parked text as a kind-less failed row,
//     then driveRun's deferred journal.Finish flushed the same text as an
//     assistant_move: two rows, 2 ms apart (dev row 500adbdf 21 -> 22).
//   - absorbed: session_write already wrote the text as the `final`, and
//     the run then failed: the segment boundary had not moved, so the
//     whole final was re-persisted after it as a kind-less failed twin
//     (dev rows 500adbdf 6 -> 7, 49ff55cc 4 -> 5 and 17 -> 18).
//
// The fix: the journal reports what it owns (turnJournal.UnpersistedTail)
// and the backend-error path persists only the tail it neither wrote nor
// holds. RecordPartial (the Stop path) applies the same rule to an
// absorbed final.
//
// Real sqlite throughout (CLAUDE.md blind spot #2), reusing the
// production-shaped sqliteHistoryWriter / sqlitePartialPersister from
// partial_persist_dedup_test.go.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

const singleFinalAnswer = "the answer is 42, and here is why it holds"

// exitGateFailsRegistry: fire 0 is the chat model's complete, healthy
// answer; fire 1 — exit_gate's verdict call on the routed graph — fails.
type exitGateFailsRegistry struct {
	stubRegistry
	mu    sync.Mutex
	calls int
}

func (r *exitGateFailsRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	n := r.calls
	r.calls++
	r.mu.Unlock()
	switch n {
	case 0:
		ch := make(chan corellm.StreamEvent, 1)
		ch <- corellm.StreamEvent{Kind: corellm.StreamText, Text: singleFinalAnswer}
		close(ch)
		return &scriptedStream{
			events: ch,
			resp: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: singleFinalAnswer}},
				FinishReason: "stop",
			},
		}, nil
	default:
		return nil, fmt.Errorf("exit gate: %w",
			errors.New("openrouter: stream read: transient provider error: connection reset by peer"))
	}
}

func newSingleFinalSQLite(t *testing.T) *session.Manager {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
}

// TestChatRunner_BackendErrorAfterCompletedFire_WritesTheAnswerOnce is the
// "parked" shape: the answer streamed and completed, then exit_gate's call
// failed. The answer must land as exactly ONE assistant row, never as a
// kind-less failed twin of its own assistant_move.
func TestChatRunner_BackendErrorAfterCompletedFire_WritesTheAnswerOnce(t *testing.T) {
	ctx := context.Background()
	mgr := newSingleFinalSQLite(t)
	rec, err := mgr.Create(ctx, "wp04 parked shape")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The caller persists the user turn (single writer).
	userRow, err := mgr.AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleUser, Content: "what is the answer?"})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	broker := &recordingBroker{}
	graph := loadRoutedChatGraph(t)
	runner, err := New(Config{
		Kernel:           coreag.NewKernel(),
		Registry:         &exitGateFailsRegistry{},
		Pool:             &scriptedPool{},
		Broker:           broker,
		HistoryWriter:    &sqliteHistoryWriter{mgr: mgr},
		History:          staticHistoryReader{},
		GraphLoader:      func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:         func() int { return 25 },
		PartialPersister: &sqlitePartialPersister{mgr: mgr},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	drainRunnerOnCleanup(t, runner)
	if _, err := runner.StartStream(ctx, "profile-1", rec.ID, "",
		UserTurn{MessageID: userRow.ID, Text: userRow.Content, Announce: true}); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Fatalf("expected the exit gate failure to end the run with backend-error, got %q (%q)", closed.Reason, closed.Message)
	}
	// driveRun's deferred journal.Finish runs after the closed emit; give
	// it the same drain the other driveRun tests use.
	waitForRunDrained(t, runner)

	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	assertOneAssistantRowPerAnswer(t, msgs, singleFinalAnswer, userRow.ID)
}

// assertOneAssistantRowPerAnswer: exactly one assistant row carries the
// answer, it is a move of the turn (never a kind-less twin), and no
// kind-less assistant row repeats any move's text.
func assertOneAssistantRowPerAnswer(t *testing.T, msgs []session.Message, answer, spanID string) {
	t.Helper()
	var carrying []session.Message
	for _, m := range msgs {
		if m.Role == session.RoleAssistant && m.Content == answer {
			carrying = append(carrying, m)
		}
	}
	if len(carrying) != 1 {
		var shapes []string
		for _, m := range carrying {
			shapes = append(shapes, fmt.Sprintf("{kind=%q span=%q failed=%v}", m.MoveKind(), m.TurnSpanID(), m.StreamingFailedAt != nil))
		}
		t.Fatalf("assistant rows carrying the answer = %d %v, want exactly 1 — a kind-less twin renders as an extra bubble", len(carrying), shapes)
	}
	if carrying[0].MoveKind() == "" {
		t.Errorf("the answer's only row is kind-less; want it recorded as a move of the turn")
	}
	if got := carrying[0].TurnSpanID(); got != spanID {
		t.Errorf("the answer's row has turn_span_id %q, want the user row %q", got, spanID)
	}
}

// waitForRunDrained blocks until the runner has no live subscriptions —
// i.e. driveRun's deferred cleanup (journal.Finish included) has run.
func waitForRunDrained(t *testing.T, r *ChatRunner) {
	t.Helper()
	for i := 0; i < 400; i++ {
		r.mu.Lock()
		n := len(r.subs)
		r.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("chat run never drained")
}

// ---- journal level: the absorbed shape --------------------------------

// TestJournal_UnpersistedTail covers both shapes at the journal seam the
// backend-error path now consults, plus the cases that MUST still persist.
func TestJournal_UnpersistedTail(t *testing.T) {
	ctx := context.Background()
	newJ := func() (*turnJournal, *recordingHistoryWriter) {
		w := &recordingHistoryWriter{}
		return newTurnJournal(w, nil, "s", testUserTurnID, nil), w
	}

	t.Run("absorbed final owns its text", func(t *testing.T) {
		j, _ := newJ()
		j.OpenAssistantSegment()
		j.RecordAssistantMove(ctx, "final words", corellm.Response{}, "", "")
		if _, err := j.AppendEntry(ctx, "s", coreag.HistoryEntry{Role: "assistant", Content: "final words"}); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
		if got := j.UnpersistedTail("final words"); got != "" {
			t.Errorf("UnpersistedTail after the final was written = %q, want \"\" (already persisted)", got)
		}
	})
	t.Run("parked text is owned — Finish will write it", func(t *testing.T) {
		j, _ := newJ()
		j.OpenAssistantSegment()
		j.RecordAssistantMove(ctx, "parked words", corellm.Response{}, "", "")
		if got := j.UnpersistedTail("parked words"); got != "" {
			t.Errorf("UnpersistedTail with the text parked = %q, want \"\"", got)
		}
	})
	t.Run("an in-flight segment is not owned", func(t *testing.T) {
		j, _ := newJ()
		j.OpenAssistantSegment()
		j.RecordAssistantMove(ctx, "earlier", corellm.Response{}, "", "")
		j.RecordToolCall(ctx, coreag.ToolCall{ID: "t1", Name: "x"})
		j.OpenAssistantSegment() // a new fire started streaming
		if got := j.UnpersistedTail("earlier, and then new words"); got != "earlier, and then new words" {
			t.Errorf("UnpersistedTail for an open segment = %q, want it untouched", got)
		}
	})
	t.Run("streamed past the parked text keeps the tail", func(t *testing.T) {
		j, _ := newJ()
		j.OpenAssistantSegment()
		j.RecordAssistantMove(ctx, "head", corellm.Response{}, "", "")
		if got := j.UnpersistedTail("head and tail"); got != " and tail" {
			t.Errorf("UnpersistedTail = %q, want only the un-owned tail", got)
		}
	})
	t.Run("an inert journal owns nothing", func(t *testing.T) {
		j := newTurnJournal(nil, nil, "s", "", nil)
		if got := j.UnpersistedTail("anything"); got != "anything" {
			t.Errorf("inert journal UnpersistedTail = %q, want the input", got)
		}
	})
}
