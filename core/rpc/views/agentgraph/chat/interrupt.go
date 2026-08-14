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
	// SegmentText is the text of the move that was in flight — the tail
	// of PartialText since the last move boundary
	// (model-moves-transcript-01PMCH01 WP02, adversarial review).
	//
	// The move path persists THIS, not PartialText: every earlier
	// segment of the turn is already its own persisted move, so writing
	// the whole accumulation again duplicates them in the transcript.
	// The classic path keeps using PartialText, which is correct there
	// because it writes exactly one row for the whole turn.
	SegmentText string
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
		SegmentText:       bridge.PartialSegment(),
		DanglingToolCalls: danglingCalls,
		InterruptedAt:     time.Now(),
	}
}

// markedText returns the partial text with the interrupt marker appended.
// When PartialText is empty, just the marker is returned.
func (is *InterruptState) markedText() string { return markInterrupted(is.PartialText) }

// markedSegment is markedText for the move path: only the in-flight
// move's own text (model-moves-transcript-01PMCH01 WP02).
func (is *InterruptState) markedSegment() string { return markInterrupted(is.SegmentText) }

// markInterrupted appends the interrupt marker, idempotently.
func markInterrupted(text string) string {
	if text == "" {
		return interruptedByUserMarker
	}
	if strings.HasSuffix(strings.TrimSpace(text), interruptedByUserMarker) {
		return text // already marked
	}
	return text + "\n" + interruptedByUserMarker
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
//
// journal is the turn's move journal
// (model-moves-transcript-01PMCH01 WP02). When it is recording, both
// writes go through it so the interrupted segment and each synthetic
// result take positions in the turn instead of landing as classic rows
// in the middle of a move-tagged turn. The partial is an
// `assistant_move`, never a `final`: an interrupted turn produced no
// answer, and labelling a truncated segment as the answer would make
// WP05's collapsed view lie about what happened. The journal forwards
// to the same seam `writer` points at, so a nil/inert journal keeps
// the pre-mission behaviour exactly.
func (is *InterruptState) PersistInterrupt(
	ctx context.Context,
	sessionID string,
	writer coreag.HistoryWriter,
	journal *turnJournal,
) (assistantMsgID string) {
	if writer == nil {
		return ""
	}

	marked := is.markedText()

	if journal.records() {
		// 1. The interrupted segment, at the position its first delta
		//    already announced on the stream. SegmentText, not
		//    PartialText: the turn's earlier segments are already
		//    persisted moves of their own.
		journal.RecordPartial(ctx, is.markedSegment())
		// 2. Close every dangling tool_use. Its tool_call entry already
		//    exists — kernelToolAdapter writes that before dispatch —
		//    so this backfills the answering half and the pair is whole.
		for _, tc := range is.DanglingToolCalls {
			journal.RecordSyntheticToolResult(ctx, tc, danglingToolResultText(tc))
		}
		logging.L().Info("chat.interrupt.persist_moves.ok",
			"session_id", sessionID,
			"dangling_tools", len(is.DanglingToolCalls),
			"moves", journal.MoveCount(),
		)
		// The move path does not surface a message id: the partial row's
		// id is not used by any caller (driveRun discards it), and
		// inventing a second return channel for it would be plumbing
		// with no reader.
		return ""
	}

	// 1. Persist the marked partial text as the assistant row.
	mid, err := writer.AppendEntry(ctx, sessionID, coreag.HistoryEntry{
		Role:    "assistant",
		Content: marked,
	})
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
		_, terr := writer.AppendEntry(ctx, sessionID, coreag.HistoryEntry{
			Role:    "tool",
			Content: danglingToolResultText(tc),
		})
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

// danglingToolResultText is the synthetic is_error body written for a
// tool_use the interrupt cancelled. One definition so the move path and
// the classic path cannot drift.
func danglingToolResultText(tc coreag.ToolCallRequest) string {
	return fmt.Sprintf(
		"tool call %q (id=%s) cancelled: interrupted by user",
		tc.Name, tc.ID,
	)
}
