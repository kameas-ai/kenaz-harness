package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// interruptedByUserMarker is the canonical string appended to the partial
// assistant text when a user initiates a Stop while the agent is running.
// The frontend surfaces this verbatim in the stream bubble.
// Mirrors the reference implementation (utils/messages.ts interrupt marker).
const interruptedByUserMarker = "[interrupted by user]"

// InterruptState captures the transcript state needed to make a
// mid-run user interrupt leave an API-valid session history.
// FR-001 (agent-loop-robustness-parity).
//
// InterruptState is created by NewInterruptState from the bridge's
// accumulated partial state at the moment StopStream is called. The
// runner appends the "[interrupted by user]" marker and backfills
// synthetic is_error tool_result messages for every dangling tool_use
// so the transcript stays valid on resume.
type InterruptState struct {
	// PartialText is the accumulated streamed assistant text at the time
	// of interrupt. May be empty when the interrupt fired before any text
	// was emitted.
	PartialText string
	// DanglingToolCalls is the set of tool_use calls that have no
	// matching tool_result at the time of interrupt. The runner generates
	// a synthetic is_error tool_result for each one.
	DanglingToolCalls []coreag.ToolCallRequest
	// InterruptedAt records when the interrupt was processed.
	InterruptedAt time.Time
}

// NewInterruptState builds the interrupt state from the bridge's
// current partial accumulation. The caller supplies any in-flight
// tool calls that have not yet received a result (the LLMNode emits
// these via the bridge's ToolCallsSeen seam). When no partial text
// exists and there are no dangling calls the state is still created
// but PersistInterrupt is a no-op for the text portion.
func NewInterruptState(bridge *StreamBridge, danglingCalls []coreag.ToolCallRequest) *InterruptState {
	text, _ := bridge.PartialState()
	return &InterruptState{
		PartialText:       text,
		DanglingToolCalls: danglingCalls,
		InterruptedAt:     time.Now(),
	}
}

// markedText returns the partial text with the interrupt marker appended.
// When PartialText is empty, just the marker is returned.
func (is *InterruptState) markedText() string {
	if is.PartialText == "" {
		return interruptedByUserMarker
	}
	if strings.HasSuffix(strings.TrimSpace(is.PartialText), interruptedByUserMarker) {
		return is.PartialText // already marked
	}
	return is.PartialText + "\n" + interruptedByUserMarker
}

// PersistInterrupt writes the interrupt state to durable storage through
// the HistoryWriter. It:
//  1. Appends the partial assistant text (with interrupt marker) as an
//     assistant message row so the transcript shows the incomplete turn.
//  2. For each dangling tool_use, appends a synthetic tool-result
//     message with IsError=true so the transcript stays API-valid (no
//     unmatched tool_use) on resume.
//
// The caller is responsible for using a fresh background context so a
// cancelled streamCtx does not abort the persist.
//
// Returns the persisted assistant message ID (may be empty when the
// writer is nil). Non-fatal: errors are logged and the run continues
// to its stop-called terminal path regardless.
func (is *InterruptState) PersistInterrupt(
	ctx context.Context,
	sessionID string,
	writer coreag.HistoryWriter,
) (assistantMsgID string) {
	if writer == nil {
		return ""
	}

	// 1. Persist the marked partial text as the assistant row.
	marked := is.markedText()
	mid, err := writer.AppendMessage(ctx, sessionID, "assistant", marked)
	if err != nil {
		logging.L().Warn("chat.interrupt.persist_assistant.failed",
			"session_id", sessionID,
			"err", err.Error(),
		)
		// Continue; we still try to persist tool results below.
	} else {
		assistantMsgID = mid
		logging.L().Info("chat.interrupt.persist_assistant.ok",
			"session_id", sessionID,
			"message_id", mid,
			"marker_appended", true,
			"dangling_tools", len(is.DanglingToolCalls),
		)
	}

	// 2. Backfill synthetic is_error tool_results for every dangling tool_use.
	for _, tc := range is.DanglingToolCalls {
		content := fmt.Sprintf(
			"tool call %q (id=%s) cancelled: interrupted by user",
			tc.Name, tc.ID,
		)
		_, terr := writer.AppendMessage(ctx, sessionID, "tool", content)
		if terr != nil {
			logging.L().Warn("chat.interrupt.persist_tool_result.failed",
				"session_id", sessionID,
				"tool_name", tc.Name,
				"tool_call_id", tc.ID,
				"err", terr.Error(),
			)
		} else {
			logging.L().Info("chat.interrupt.persist_tool_result.ok",
				"session_id", sessionID,
				"tool_name", tc.Name,
				"tool_call_id", tc.ID,
			)
		}
	}

	return assistantMsgID
}
