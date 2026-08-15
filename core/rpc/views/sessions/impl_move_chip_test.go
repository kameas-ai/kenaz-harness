package sessions

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

// TestMessageToView_ToolResultErrorReachesTheWire pins the last hop of
// the chat chip's durable error signal
// (model-moves-transcript-01PMCH01 WP04).
//
// The chain is: the tool failed → turnJournal stamps
// ToolCallRequest.IsError → moveToolCalls projects it onto
// session.ToolCall (pinned in core/rpc) → messageToView puts it on the
// wire (here) → the frontend renders the chip as an error. Every hop
// needs a reader, or a reloaded session downgrades a failed tool to
// "ok" and the surface lies about what happened.
//
// MUTATION EVIDENCE (run and confirmed to fail): drop
// `IsError: tc.IsError` from messageToView's ToolCall projection -> the
// wire row reports success for the failed call and this fails.
func TestMessageToView_ToolResultErrorReachesTheWire(t *testing.T) {
	t.Parallel()

	view := messageToView(session.Message{
		ID:        "m-1",
		SessionID: "s-1",
		Role:      session.RoleTool,
		Content:   "permission denied",
		CreatedAt: time.Now().UTC(),
		ToolCalls: []session.ToolCall{
			{ID: "tu-ok", Name: "fs__read"},
			{ID: "tu-bad", Name: "sh__exec", IsError: true},
		},
	})
	if len(view.ToolCalls) != 2 {
		t.Fatalf("wire row carries %d tool calls, want 2", len(view.ToolCalls))
	}
	if view.ToolCalls[0].IsError {
		t.Error("a successful call reached the wire flagged as an error")
	}
	if !view.ToolCalls[1].IsError {
		t.Error("a failed call reached the wire unflagged — the reloaded chip would read ok")
	}
	// The display layer still carries no argument values.
	for i, tc := range view.ToolCalls {
		if tc.ArgsSummary != "" {
			t.Errorf("call %d: messageToView invented an args summary %q", i, tc.ArgsSummary)
		}
	}
}
