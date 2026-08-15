package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// newMovesSQLManager opens a real sqlite database (so migration 0333 is
// exercised, not stubbed), wraps it in the session SQL store, and
// returns a manager plus a created session id.
func newMovesSQLManager(t *testing.T) (*session.Manager, string) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(context.Background(), "moves")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, rec.ID
}

// movesFixture is the canonical one-human-turn trajectory: the model
// speaks, calls a tool, reads the result, then answers. Every WP01
// round-trip test walks this exact list so a schema regression shows up
// as a diff against a shape the mission actually intends to write.
func movesFixture(turnSpanID string) []session.TranscriptEntry {
	return []session.TranscriptEntry{
		{
			Role: session.RoleAssistant, Content: "Let me read the key files.",
			Kind: session.MoveKindAssistantMove, MoveIndex: 0, TurnSpanID: turnSpanID,
		},
		{
			Role: session.RoleTool, Content: "bash(cmd=…)",
			Kind: session.MoveKindToolCall, MoveIndex: 1, TurnSpanID: turnSpanID,
			ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "bash"}},
		},
		{
			Role: session.RoleTool, Content: "exit 0",
			Kind: session.MoveKindToolResult, MoveIndex: 2, TurnSpanID: turnSpanID,
			ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "bash", Result: "exit 0"}},
		},
		{
			Role: session.RoleAssistant, Content: "Found it — the seam is in exec_state.go.",
			Kind: session.MoveKindFinal, MoveIndex: 3, TurnSpanID: turnSpanID,
		},
	}
}

// TestAppendTranscriptEntry_SQLRoundTripsEveryMoveKind is the FR-001
// schema round-trip: write one entry per move kind through the seam,
// reload from sqlite, and assert every field survived.
//
// Mutation checks (each kills this test):
//   - drop `kind` (or move_index / turn_span_id) from the INSERT column
//     list in sqlStore.AppendMessage → reloaded kind is empty;
//   - drop the applyMoveColumns call from listMessages → same;
//   - have moveColumnValues bind moveIndex as nil → MoveIndex is nil;
//   - delete migration0333 from Migrations() → the INSERT fails with
//     "no such column".
func TestAppendTranscriptEntry_SQLRoundTripsEveryMoveKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	// The human turn the moves hang off. Its own id becomes the span.
	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "find the writer seam",
	})
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}
	want := movesFixture(turn.ID)
	for i, e := range want {
		if _, err := mgr.AppendTranscriptEntry(ctx, sid, e); err != nil {
			t.Fatalf("append move %d (%s): %v", i, e.Kind, err)
		}
	}

	got, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != len(want)+1 {
		t.Fatalf("ListMessages returned %d rows, want %d", len(got), len(want)+1)
	}

	// Row 0 is the human turn: classic, no move metadata.
	if k := got[0].MoveKind(); k != "" {
		t.Errorf("human turn row: MoveKind = %q, want empty", k)
	}
	if got[0].TurnSpanID() != "" || got[0].MoveIndex() != nil {
		t.Errorf("human turn row carries move metadata: index=%v span=%q",
			got[0].MoveIndex(), got[0].TurnSpanID())
	}

	for i, w := range want {
		row := got[i+1]
		if row.MoveKind() != w.Kind {
			t.Errorf("row %d: MoveKind = %q, want %q", i+1, row.MoveKind(), w.Kind)
		}
		idx := row.MoveIndex()
		if idx == nil {
			t.Fatalf("row %d (%s): MoveIndex is nil, want %d", i+1, w.Kind, w.MoveIndex)
		}
		if *idx != w.MoveIndex {
			t.Errorf("row %d (%s): MoveIndex = %d, want %d", i+1, w.Kind, *idx, w.MoveIndex)
		}
		if row.TurnSpanID() != turn.ID {
			t.Errorf("row %d (%s): TurnSpanID = %q, want %q", i+1, w.Kind, row.TurnSpanID(), turn.ID)
		}
		if row.Content != w.Content {
			t.Errorf("row %d (%s): Content = %q, want %q", i+1, w.Kind, row.Content, w.Content)
		}
	}

	// GetMessage is a second SELECT with its own column list — a
	// regression there is invisible to ListMessages.
	one, err := mgr.GetMessage(ctx, sid, got[1].ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if one.MoveKind() != session.MoveKindAssistantMove {
		t.Errorf("GetMessage: MoveKind = %q, want %q", one.MoveKind(), session.MoveKindAssistantMove)
	}
	if one.TurnSpanID() != turn.ID {
		t.Errorf("GetMessage: TurnSpanID = %q, want %q", one.TurnSpanID(), turn.ID)
	}
	if idx := one.MoveIndex(); idx == nil || *idx != 0 {
		t.Errorf("GetMessage: MoveIndex = %v, want 0", idx)
	}
}

// TestAppendTranscriptEntry_MoveIndexZeroSurvives pins the reason
// MoveIndex is a pointer everywhere. Index 0 is the FIRST move of every
// turn; a plain int would be indistinguishable from "unset" through any
// omitempty-shaped hop.
//
// Mutation: make moveColumnValues return nil for moveIndex when the
// value is 0, or make Message.MoveIndex() return nil for a zero value →
// this fails while every other assertion still passes.
func TestAppendTranscriptEntry_MoveIndexZeroSurvives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	if _, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleAssistant, Content: "first move",
		Kind: session.MoveKindAssistantMove, MoveIndex: 0, TurnSpanID: "turn-1",
	}); err != nil {
		t.Fatalf("AppendTranscriptEntry: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	idx := msgs[0].MoveIndex()
	if idx == nil {
		t.Fatal("MoveIndex is nil for move_index=0 — zero collapsed into unset")
	}
	if *idx != 0 {
		t.Errorf("MoveIndex = %d, want 0", *idx)
	}
}

// TestAppendTranscriptEntry_ClassicEntryCarriesNoMoveMetadata is the
// old-session half of the additive-wire contract: an entry written
// without a Kind must round-trip with all three move fields absent, so
// its wire projection is byte-identical to the pre-mission shape (the
// bytes themselves are pinned in
// core/rpc/views/sessions/moves_wire_test.go).
//
// Mutation: have AppendTranscriptEntry default Kind to MoveKindFinal
// when empty → this fails immediately.
func TestAppendTranscriptEntry_ClassicEntryCarriesNoMoveMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	if _, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleAssistant, Content: "one flattened turn",
	}); err != nil {
		t.Fatalf("AppendTranscriptEntry: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	m := msgs[0]
	if k := m.MoveKind(); k != "" {
		t.Errorf("MoveKind = %q, want empty", k)
	}
	if idx := m.MoveIndex(); idx != nil {
		t.Errorf("MoveIndex = %d, want nil", *idx)
	}
	if s := m.TurnSpanID(); s != "" {
		t.Errorf("TurnSpanID = %q, want empty", s)
	}
}

// TestAppendMessage_LeavesRowsClassic proves the OTHER writer on the
// manager cannot mint move metadata. AppendMessage takes a Message
// whose move fields are unexported, so a caller outside core/session
// has no syntax for setting them — this test pins the resulting
// behaviour rather than the compile error.
//
// Mutation: export the three fields on session.Message and set them
// here → the test would still pass, which is why the compiler check is
// backed by scripts/ci/check-single-move-writer.sh.
func TestAppendMessage_LeavesRowsClassic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	if _, err := mgr.AppendMessage(ctx, sid, session.Message{
		Role: session.RoleAssistant, Content: "classic path",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if k := msgs[0].MoveKind(); k != "" {
		t.Errorf("AppendMessage produced a move-kind row: %q", k)
	}
}

// TestAppendTranscriptEntry_RejectsIncoherentMoveMetadata pins the
// validation the seam owes its callers. Each case would otherwise
// persist a row no reader has a case for.
//
// Mutation: delete the validate() call from AppendTranscriptEntry →
// every subtest fails.
func TestAppendTranscriptEntry_RejectsIncoherentMoveMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name  string
		entry session.TranscriptEntry
		want  error
	}{
		{
			name: "unknown kind",
			entry: session.TranscriptEntry{
				Role: session.RoleAssistant, Content: "x",
				Kind: session.MoveKind("thinking"), MoveIndex: 0, TurnSpanID: "t1",
			},
			want: session.ErrUnknownMoveKind,
		},
		{
			name: "move without turn span",
			entry: session.TranscriptEntry{
				Role: session.RoleAssistant, Content: "x",
				Kind: session.MoveKindAssistantMove, MoveIndex: 0,
			},
			want: session.ErrMoveTurnSpanRequired,
		},
		{
			name: "negative index",
			entry: session.TranscriptEntry{
				Role: session.RoleAssistant, Content: "x",
				Kind: session.MoveKindFinal, MoveIndex: -1, TurnSpanID: "t1",
			},
			want: session.ErrNegativeMoveIndex,
		},
		{
			name: "span without kind",
			entry: session.TranscriptEntry{
				Role: session.RoleAssistant, Content: "x", TurnSpanID: "t1",
			},
			want: session.ErrMoveMetadataWithoutKind,
		},
		{
			name: "index without kind",
			entry: session.TranscriptEntry{
				Role: session.RoleAssistant, Content: "x", MoveIndex: 3,
			},
			want: session.ErrMoveMetadataWithoutKind,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := session.NewManager(session.NewMemoryStore())
			rec, err := mgr.Create(ctx, "s")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := mgr.AppendTranscriptEntry(ctx, rec.ID, tc.entry); !errors.Is(err, tc.want) {
				t.Fatalf("AppendTranscriptEntry error = %v, want %v", err, tc.want)
			}
			msgs, err := mgr.ListMessages(ctx, rec.ID)
			if err != nil {
				t.Fatalf("ListMessages: %v", err)
			}
			if len(msgs) != 0 {
				t.Errorf("rejected entry still persisted %d row(s)", len(msgs))
			}
		})
	}
}

// TestAppendTranscriptEntry_MemStoreMatchesSQL keeps the in-memory
// store honest: tests all over core/rpc run against NewMemoryStore, so
// a memstore that silently dropped move metadata would make those tests
// lie about the SQL behaviour.
//
// Mutation: have memStore.AppendMessage rebuild the Message from a
// field subset (the shape it uses for canonicalization) → the move
// fields vanish and this fails.
func TestAppendTranscriptEntry_MemStoreMatchesSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	rec, err := mgr.Create(ctx, "s")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, e := range movesFixture("turn-1") {
		if _, err := mgr.AppendTranscriptEntry(ctx, rec.ID, e); err != nil {
			t.Fatalf("AppendTranscriptEntry(%s): %v", e.Kind, err)
		}
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	want := movesFixture("turn-1")
	if len(msgs) != len(want) {
		t.Fatalf("got %d rows, want %d", len(msgs), len(want))
	}
	for i, w := range want {
		if msgs[i].MoveKind() != w.Kind {
			t.Errorf("row %d: MoveKind = %q, want %q", i, msgs[i].MoveKind(), w.Kind)
		}
		if idx := msgs[i].MoveIndex(); idx == nil || *idx != w.MoveIndex {
			t.Errorf("row %d: MoveIndex = %v, want %d", i, idx, w.MoveIndex)
		}
		if msgs[i].TurnSpanID() != "turn-1" {
			t.Errorf("row %d: TurnSpanID = %q, want turn-1", i, msgs[i].TurnSpanID())
		}
	}
}

// TestMoveKinds_MatchesWireVocabulary pins the four-kind vocabulary
// spec §4 names. The frontend union type (frontend/src/lib/types.ts
// MoveKind) and any provider-shape renderer added in WP03 mirror this
// list; a silent addition here without the mirrors is the drift this
// catches.
func TestMoveKinds_MatchesWireVocabulary(t *testing.T) {
	t.Parallel()
	want := []session.MoveKind{"assistant_move", "tool_call", "tool_result", "final"}
	got := session.MoveKinds()
	if len(got) != len(want) {
		t.Fatalf("MoveKinds() has %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MoveKinds()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestMigration0333_ColumnsExistAndDefaultNull confirms the columns
// ride along on a fresh Open and that a row inserted without them
// (every pre-mission row) reads back NULL — the discriminator the
// "classic entry" contract rests on.
func TestMigration0333_ColumnsExistAndDefaultNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(ctx)

	for _, col := range []string{"kind", "move_index", "turn_span_id"} {
		var n int
		row := db.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name=?", col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info %q: %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %q missing from session_messages (count=%d)", col, n)
		}
	}

	// A row written the pre-mission way (no move columns named) must
	// read back NULL on all three.
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position)
             VALUES ('s-legacy', 'legacy', 0, 0, 0, 0)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at)
             VALUES ('m-legacy', 's-legacy', 0, 'assistant', 'old text', 0)`)
		return err
	}); err != nil {
		t.Fatalf("seeding legacy row: %v", err)
	}

	var nonNull int
	row := db.Reader().QueryRow(ctx,
		`SELECT COUNT(*) FROM session_messages
         WHERE id='m-legacy'
           AND (kind IS NOT NULL OR move_index IS NOT NULL OR turn_span_id IS NOT NULL)`)
	if err := row.Scan(&nonNull); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if nonNull != 0 {
		t.Errorf("legacy row picked up %d non-NULL move column(s); migration must not backfill", nonNull)
	}
}

// TestAppendContinuation_CarriesMoveColumns closes the gap the WP01
// review found: sqlStore.AppendContinuation's INSERT named nine columns
// and omitted kind / move_index / turn_span_id, so a continuation row
// dropped its move identity in the database while the Message it
// RETURNED still carried it in memory. The caller and the store
// disagreed and only a reload told you which one was lying.
//
// It was unreachable while every production write left the move fields
// zero. WP02 fills a chat turn with non-zero ones, so the first user who
// resumes an interrupted move-bearing turn reaches it.
//
// The input Message here is one READ BACK from the store, which is the
// only way a caller outside core/session can hold move metadata at all
// (the fields are unexported) and exactly the shape the eventual
// Sessions_ResumeMessage continuation path will use.
//
// Mutation check (kills this test): drop the three columns from the
// AppendContinuation INSERT again → the reloaded continuation reads
// back with an empty MoveKind.
func TestAppendContinuation_CarriesMoveColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "do the thing",
	})
	if err != nil {
		t.Fatalf("user turn: %v", err)
	}
	original, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleAssistant, Content: "partial…",
		Kind: session.MoveKindAssistantMove, MoveIndex: 2, TurnSpanID: turn.ID,
	})
	if err != nil {
		t.Fatalf("partial move: %v", err)
	}

	// Read it back so the continuation is built from a Message that
	// actually carries the (unexported) move fields.
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var source session.Message
	for _, m := range msgs {
		if m.ID == original.ID {
			source = m
		}
	}
	if source.MoveKind() != session.MoveKindAssistantMove {
		t.Fatalf("precondition: source move kind = %q", source.MoveKind())
	}
	source.ID = ""
	source.Content = "partial… and the rest"

	cont, err := mgr.AppendContinuation(ctx, sid, original.ID, source)
	if err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if cont.MoveKind() != session.MoveKindAssistantMove {
		t.Errorf("returned continuation MoveKind = %q, want assistant_move", cont.MoveKind())
	}

	// The DURABLE row is the point: reload and check the columns landed.
	reloaded, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var got session.Message
	for _, m := range reloaded {
		if m.ID == cont.ID {
			got = m
		}
	}
	if got.ID == "" {
		t.Fatalf("continuation row %q not found on reload", cont.ID)
	}
	if got.MoveKind() != session.MoveKindAssistantMove {
		t.Errorf("reloaded MoveKind = %q, want assistant_move — the INSERT dropped it", got.MoveKind())
	}
	if got.MoveIndex() == nil || *got.MoveIndex() != 2 {
		t.Errorf("reloaded MoveIndex = %v, want 2", got.MoveIndex())
	}
	if got.TurnSpanID() != turn.ID {
		t.Errorf("reloaded TurnSpanID = %q, want %q", got.TurnSpanID(), turn.ID)
	}
}

// TestAppendTranscriptEntry_ToolResultErrorSurvivesReload pins the
// durability of the chat chip's error signal
// (model-moves-transcript-01PMCH01 WP04).
//
// A tool_result move records whether its call failed. That fact reaches
// the live chat surface on the stream's move boundary, but the surface
// has to render the same chip after a reload — FR-003's "no post-hoc
// mismatch between what you watched and what's stored". Since a reload
// reads the row, the flag has to survive the round trip.
//
// It needs no migration: session_messages.tool_calls holds the
// []session.ToolCall as JSON, so the field rides the existing column and
// rows written before the mission simply omit it. That is precisely why
// it needs a test — a `json:"-"` tag would silently sever it with no
// schema change to notice.
//
// Mutation check (kills this test): change session.ToolCall.IsError's
// tag to `json:"-"` → the reloaded row reports the failed call as
// successful.
func TestAppendTranscriptEntry_ToolResultErrorSurvivesReload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "run it",
	})
	if err != nil {
		t.Fatalf("user turn: %v", err)
	}
	if _, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleTool, Content: "cmd:string",
		ToolCalls:  []session.ToolCall{{ID: "tu-bad", Name: "sh__exec"}},
		Kind:       session.MoveKindToolCall,
		MoveIndex:  1,
		TurnSpanID: turn.ID,
	}); err != nil {
		t.Fatalf("tool_call move: %v", err)
	}
	stored, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleTool, Content: "permission denied",
		ToolCalls:  []session.ToolCall{{ID: "tu-bad", Name: "sh__exec", IsError: true}},
		Kind:       session.MoveKindToolResult,
		MoveIndex:  2,
		TurnSpanID: turn.ID,
	})
	if err != nil {
		t.Fatalf("tool_result move: %v", err)
	}

	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var got session.Message
	for _, m := range msgs {
		if m.ID == stored.ID {
			got = m
		}
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("reloaded ToolCalls = %d, want 1: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if !got.ToolCalls[0].IsError {
		t.Error("the failed call reloaded as a success — a reloaded chip would read ok")
	}
	if got.ToolCalls[0].ID != "tu-bad" {
		t.Errorf("pairing id = %q, want tu-bad", got.ToolCalls[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Model-layer tool arguments (model-moves-transcript-01PMCH01 WP03).
// ---------------------------------------------------------------------------

// TestAppendTranscriptEntry_SQLRoundTripsModelToolArgs is the schema
// round-trip for the MODEL LAYER's raw arguments — the half of spec §4's
// two-layer redaction that legitimately carries values, because the
// provider protocol cannot reconstruct an assistant tool_use block
// without them.
//
// It must run against SQLITE, not the memory store. The memory store
// keeps *session.Message values verbatim, so it exercises neither
// moveColumnValues' JSON encode nor applyMoveColumns' decode — the two
// places the column can actually be lost. WP03's composition tests use
// the memory store (they are about projection, not persistence), and a
// mutation that deleted the model_tool_args bind from the INSERT
// survived every one of them. This test is what killed it.
//
// Mutation checks (each kills this test):
//   - drop `model_tool_args` from the INSERT column list in
//     sqlStore.AppendMessage → reloaded args are nil;
//   - make moveColumnValues skip the marshal (bind nil) → same;
//   - drop the modelArgs decode from applyMoveColumns → same;
//   - delete migration0334 from Migrations() → the INSERT fails with
//     "no such column: model_tool_args".
func TestAppendTranscriptEntry_SQLRoundTripsModelToolArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "run the build",
	})
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}

	const rawArgs = `{"cmd":"go build ./...","timeout":60}`
	if _, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleTool, Content: "bash(cmd=<string>, timeout=<number>)",
		Kind: session.MoveKindToolCall, MoveIndex: 0, TurnSpanID: turn.ID,
		ToolCalls:     []session.ToolCall{{ID: "tc-1", Name: "bash"}},
		ModelToolArgs: map[string]string{"tc-1": rawArgs},
	}); err != nil {
		t.Fatalf("append tool_call: %v", err)
	}

	got, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListMessages returned %d rows, want 2", len(got))
	}

	// The model layer survived the round trip...
	args := got[1].ModelLayerToolArgs()
	if len(args) != 1 || args["tc-1"] != rawArgs {
		t.Fatalf("ModelLayerToolArgs = %#v, want map[tc-1:%s] — the raw arguments did not "+
			"survive persistence, so the composition cannot rebuild a tool_use block", args, rawArgs)
	}

	// ...and the DISPLAY layer still holds nothing (spec §4). These two
	// assertions belong in one test on purpose: they are the two halves
	// of one contract, and a change that satisfies either by violating
	// the other is the failure this pins.
	for _, tc := range got[1].ToolCalls {
		if len(tc.Arguments) > 0 {
			t.Errorf("session.ToolCall.Arguments = %#v on a persisted tool_call row — "+
				"raw arguments reached the display layer's field", tc.Arguments)
		}
	}

	// The human turn stays clean.
	if a := got[0].ModelLayerToolArgs(); a != nil {
		t.Errorf("classic row carries model-layer args: %#v", a)
	}
}

// TestAppendTranscriptEntry_RejectsModelArgsOnNonToolCall pins that the
// model layer's raw arguments cannot be attached to a kind that has no
// reader for them. A silent drop would be the lossy-seam failure WP01
// designed this validation against; a silent ACCEPT would put raw
// argument values on an assistant_move or final row, which the display
// surface renders.
//
// Mutation: delete the ErrModelToolArgsWithoutToolCall clause from
// TranscriptEntry.validate → this passes and the row persists.
func TestAppendTranscriptEntry_RejectsModelArgsOnNonToolCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "go",
	})
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}

	for _, kind := range []session.MoveKind{
		session.MoveKindAssistantMove,
		session.MoveKindToolResult,
		session.MoveKindFinal,
	} {
		_, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
			Role: session.RoleAssistant, Content: "text",
			Kind: kind, MoveIndex: 0, TurnSpanID: turn.ID,
			ModelToolArgs: map[string]string{"tc-1": `{"secret":"sk-live"}`},
		})
		if !errors.Is(err, session.ErrModelToolArgsWithoutToolCall) {
			t.Errorf("kind %q: err = %v, want ErrModelToolArgsWithoutToolCall", kind, err)
		}
	}
}

// TestGetMessage_SQLRoundTripsModelToolArgs covers the OTHER SQL read
// path for the model layer's arguments.
//
// sqlStore has two row hydrators — listMessages and GetMessage — with
// separately-written SELECT lists and separately-written Scan calls, and
// WP03 widened both. Only listMessages was pinned. A mutation that
// passed a zero sql.NullString to applyMoveColumns inside GetMessage
// (i.e. GetMessage silently stops hydrating the column) survived the
// whole suite.
//
// Mutation: pass sql.NullString{} for modelToolArgsCol in GetMessage, or
// drop model_tool_args from its SELECT list → this fails.
func TestGetMessage_SQLRoundTripsModelToolArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "run the build",
	})
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}

	const rawArgs = `{"cmd":"go build ./..."}`
	call, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleTool, Content: "bash(cmd=<string>)",
		Kind: session.MoveKindToolCall, MoveIndex: 0, TurnSpanID: turn.ID,
		ToolCalls:     []session.ToolCall{{ID: "tc-1", Name: "bash"}},
		ModelToolArgs: map[string]string{"tc-1": rawArgs},
	})
	if err != nil {
		t.Fatalf("append tool_call: %v", err)
	}

	got, err := mgr.GetMessage(ctx, sid, call.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	args := got.ModelLayerToolArgs()
	if len(args) != 1 || args["tc-1"] != rawArgs {
		t.Fatalf("GetMessage ModelLayerToolArgs = %#v, want map[tc-1:%s] — the second "+
			"row hydrator does not carry the model layer", args, rawArgs)
	}
	// The display layer stays empty on this path too.
	for _, tc := range got.ToolCalls {
		if len(tc.Arguments) > 0 {
			t.Errorf("GetMessage surfaced raw arguments in the DISPLAY field: %#v", tc.Arguments)
		}
	}
}

// TestAppendContinuation_SQLRoundTripsModelToolArgs covers the third SQL
// WRITE path. sqlStore has three INSERTs into session_messages
// (AppendMessage, AppendContinuation, and the migration's own), and WP03
// widened two. Only AppendMessage was pinned; a mutation that bound NULL
// for model_tool_args in AppendContinuation survived the whole suite.
//
// A continuation of a tool_call move is what a resumed/interrupted turn
// produces, so losing the column here means the resumed turn's tool_use
// can no longer be rebuilt — the pair-integrity sweep then drops both
// halves and the model silently forgets it ran the tool.
//
// Mutation: bind nil for modelArgsCol in AppendContinuation's INSERT →
// this fails.
func TestAppendContinuation_SQLRoundTripsModelToolArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sid := newMovesSQLManager(t)

	turn, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleUser, Content: "go",
	})
	if err != nil {
		t.Fatalf("user turn: %v", err)
	}
	const rawArgs = `{"path":"foo.txt"}`
	original, err := mgr.AppendTranscriptEntry(ctx, sid, session.TranscriptEntry{
		Role: session.RoleTool, Content: "read(path=<string>)",
		Kind: session.MoveKindToolCall, MoveIndex: 1, TurnSpanID: turn.ID,
		ToolCalls:     []session.ToolCall{{ID: "tc-1", Name: "read"}},
		ModelToolArgs: map[string]string{"tc-1": rawArgs},
	})
	if err != nil {
		t.Fatalf("tool_call move: %v", err)
	}

	// Build the continuation from the RELOADED row so it carries the
	// unexported model-layer field the way production does.
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var source session.Message
	for _, m := range msgs {
		if m.ID == original.ID {
			source = m
		}
	}
	if len(source.ModelLayerToolArgs()) != 1 {
		t.Fatalf("precondition: the source row lost its model-layer args")
	}
	source.ID = ""

	cont, err := mgr.AppendContinuation(ctx, sid, original.ID, source)
	if err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}

	// The DURABLE row is the point: reload rather than trusting the
	// value the call returned.
	reloaded, err := mgr.GetMessage(ctx, sid, cont.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	args := reloaded.ModelLayerToolArgs()
	if len(args) != 1 || args["tc-1"] != rawArgs {
		t.Fatalf("continuation ModelLayerToolArgs = %#v, want map[tc-1:%s] — the "+
			"continuation INSERT dropped the model layer, so a resumed turn's "+
			"tool_use can no longer be rebuilt", args, rawArgs)
	}
}
