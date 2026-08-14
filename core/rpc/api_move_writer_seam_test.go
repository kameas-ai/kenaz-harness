package rpc

// api_move_writer_seam_test.go — the seam half of
// model-moves-transcript-01PMCH01 WP01.
//
// llmHistoryWriter is the production binding of agentgraph.HistoryWriter
// and therefore the single point at which chat transcript entries —
// classic or move — reach core/session. These tests pin that the seam
// carries the whole HistoryEntry through, in both directions: move
// metadata survives, and a classic entry stays classic.

import (
	"context"
	"errors"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// newSeamTestWriter builds an llmHistoryWriter over an in-memory session
// manager and returns it alongside the manager and a live session id.
func newSeamTestWriter(t *testing.T) (*llmHistoryWriter, *session.Manager, string) {
	t.Helper()
	mgr := session.NewManager(session.NewMemoryStore())
	rec, err := mgr.Create(context.Background(), "seam")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return &llmHistoryWriter{inner: &sessionHistoryReader{mgr: mgr}}, mgr, rec.ID
}

// TestHistoryWriterSeam_CarriesMoveMetadata proves the seam is capable of
// what WP02 will ask of it: a HistoryEntry with move fields persists as a
// move-kind row.
//
// Mutation: drop Kind / MoveIndex / TurnSpanID from the
// session.TranscriptEntry literal in llmHistoryWriter.AppendEntry — the
// exact shape of "a seam that silently degrades moves to classic
// entries" — and this fails.
func TestHistoryWriterSeam_CarriesMoveMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, mgr, sid := newSeamTestWriter(t)

	id, err := w.AppendEntry(ctx, sid, coreag.HistoryEntry{
		Role:       "assistant",
		Content:    "iteration 2 text",
		MoveKind:   string(session.MoveKindAssistantMove),
		MoveIndex:  1,
		TurnSpanID: "turn-abc",
	})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if id == "" {
		t.Fatal("AppendEntry returned an empty message id")
	}

	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d rows, want 1", len(msgs))
	}
	got := msgs[0]
	if got.MoveKind() != session.MoveKindAssistantMove {
		t.Errorf("MoveKind = %q, want %q", got.MoveKind(), session.MoveKindAssistantMove)
	}
	if idx := got.MoveIndex(); idx == nil || *idx != 1 {
		t.Errorf("MoveIndex = %v, want 1", idx)
	}
	if got.TurnSpanID() != "turn-abc" {
		t.Errorf("TurnSpanID = %q, want turn-abc", got.TurnSpanID())
	}
}

// TestHistoryWriterSeam_ClassicEntryStaysClassic is the WP01 no-behaviour-
// change assertion. Everything that writes through this seam today
// (session_write, the user turn in StartStream, PersistInterrupt) passes
// a zero-move HistoryEntry, and must keep producing exactly the rows it
// produced before the mission.
//
// Mutation: have AppendEntry default MoveKind to "final" when empty →
// this fails.
func TestHistoryWriterSeam_ClassicEntryStaysClassic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, mgr, sid := newSeamTestWriter(t)

	if _, err := w.AppendEntry(ctx, sid, coreag.HistoryEntry{
		Role:    "assistant",
		Content: "one flattened turn",
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	got := msgs[0]
	if k := got.MoveKind(); k != "" {
		t.Errorf("MoveKind = %q, want empty", k)
	}
	if idx := got.MoveIndex(); idx != nil {
		t.Errorf("MoveIndex = %d, want nil", *idx)
	}
	if s := got.TurnSpanID(); s != "" {
		t.Errorf("TurnSpanID = %q, want empty", s)
	}
	if got.Role != session.RoleAssistant || got.Content != "one flattened turn" {
		t.Errorf("row = {%s, %q}, want {assistant, \"one flattened turn\"}", got.Role, got.Content)
	}
}

// TestHistoryWriterSeam_RejectsIncoherentMove proves the seam does not
// launder a bad move past the store's validation: the error from
// AppendTranscriptEntry propagates, and nothing is persisted.
//
// Mutation: swallow the error in AppendEntry (return "", nil) → this
// fails on the error check.
func TestHistoryWriterSeam_RejectsIncoherentMove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, mgr, sid := newSeamTestWriter(t)

	_, err := w.AppendEntry(ctx, sid, coreag.HistoryEntry{
		Role:     "assistant",
		Content:  "orphan move",
		MoveKind: string(session.MoveKindToolCall),
		// TurnSpanID deliberately omitted.
	})
	if !errors.Is(err, session.ErrMoveTurnSpanRequired) {
		t.Fatalf("AppendEntry error = %v, want ErrMoveTurnSpanRequired", err)
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("rejected move still persisted %d row(s)", len(msgs))
	}
}

// TestMoveToolCalls_NeverCarriesRawArguments pins the display-layer
// half of spec §4's two-layer redaction rule at the seam where it is
// actually decided (added by the adversarial review of WP02).
//
// moveToolCalls projects coreag.ToolCallRequest onto session.ToolCall
// and deliberately leaves Arguments nil: the persisted tool_call row's
// Content already holds the args SUMMARY, and session.ToolCall.Arguments
// is durable, exported and shared. Before this test the invariant was a
// comment — filling Arguments in was a one-line change no test noticed
// (verified: the mutation survived the whole suite).
func TestMoveToolCalls_NeverCarriesRawArguments(t *testing.T) {
	t.Parallel()

	const secret = "sk-live-DO-NOT-PERSIST"
	got := moveToolCalls([]coreag.ToolCallRequest{
		{ID: "tu-1", Name: "fs__write", Arguments: `{"token":"` + secret + `"}`},
		{ID: "tu-2", Name: "bash", Arguments: `{"cmd":"export TOKEN=` + secret + `"}`},
	})
	if len(got) != 2 {
		t.Fatalf("moveToolCalls returned %d calls, want 2", len(got))
	}
	for i, tc := range got {
		if len(tc.Arguments) != 0 {
			t.Errorf("call %d carried raw arguments into the display layer: %q", i, tc.Arguments)
		}
		if tc.ID == "" || tc.Name == "" {
			t.Errorf("call %d lost its pairing identity: %+v", i, tc)
		}
	}
	if moveToolCalls(nil) != nil {
		t.Error("moveToolCalls(nil) should stay nil so a classic entry's wire bytes are unchanged")
	}
}
