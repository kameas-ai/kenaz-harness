package sessions

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestMessageToView_ToolCallChipShowsNonSecretValues pins the reload half
// of audit finding HR-7: a reloaded tool chip shows non-secret argument
// values (computed from the model-layer arguments through
// core/tooldisplay) while the row's persisted Content — what export,
// search and the provider history read — stays names-and-types.
//
// Real sqlite, not the memory store: the values come from the
// model_tool_args column, and a memory-store fixture would skip the
// encode/decode this depends on (CLAUDE.md blind spot #2).
//
// MUTATION EVIDENCE: return "" from toolCallArgsDisplay -> the first
// assertion fails; replace the tooldisplay call with the raw JSON -> the
// token assertion fails.
func TestMessageToView_ToolCallChipShowsNonSecretValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(ctx, "chip")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const token = "sk-live-DO-NOT-RENDER-0123456789"
	const typesOnly = "kenaz__read_file(path=<string>, token=<string>)"
	if _, err := mgr.AppendTranscriptEntry(ctx, rec.ID, session.TranscriptEntry{
		Role:       session.RoleTool,
		Content:    typesOnly,
		Kind:       session.MoveKindToolCall,
		MoveIndex:  1,
		TurnSpanID: "span-1",
		ToolCalls:  []session.ToolCall{{ID: "tu-1", Name: "kenaz__read_file"}},
		ModelToolArgs: map[string]string{
			"tu-1": `{"path":"/workspace/src","token":"` + token + `"}`,
		},
	}); err != nil {
		t.Fatalf("AppendTranscriptEntry: %v", err)
	}

	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %d rows, %v", len(msgs), err)
	}
	view := messageToView(msgs[0])

	if len(view.ToolCalls) != 1 {
		t.Fatalf("wire row carries %d tool calls, want 1", len(view.ToolCalls))
	}
	got := view.ToolCalls[0].ArgsSummary
	if want := "kenaz__read_file(path=/workspace/src, token=<redacted>)"; got != want {
		t.Errorf("chip args = %q, want %q", got, want)
	}
	if strings.Contains(got, token) {
		t.Fatalf("a secret argument reached the wire: %q", got)
	}
	if view.Content != typesOnly {
		t.Errorf("row Content changed to %q — the persisted/exported layer must stay names-and-types", view.Content)
	}
}

// A classic (pre-moves) row that persisted a structured Arguments map
// gets the same policy; a tool_result row gets nothing.
func TestMessageToView_ClassicArgumentsUseThePolicy(t *testing.T) {
	t.Parallel()
	view := messageToView(session.Message{
		ID:        "m-1",
		SessionID: "s-1",
		Role:      session.RoleAssistant,
		CreatedAt: time.Now().UTC(),
		ToolCalls: []session.ToolCall{{
			ID:        "tc-1",
			Name:      "bash",
			Arguments: map[string]any{"command": "ls -la", "password": "hunter2"},
		}},
	})
	if got, want := view.ToolCalls[0].ArgsSummary, "bash(command=ls -la, password=<redacted>)"; got != want {
		t.Errorf("classic chip args = %q, want %q", got, want)
	}
}
