package chat

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// partialFlushInterval is the period between periodic checkpoint
// upserts during a live kernel run. 10 s gives a worst-case crash-loss
// window of ~10 s while keeping write amplification bounded to a
// single UPSERT into stream_checkpoints — one row per (session, sub)
// no matter how many ticks land, not one INSERT into session_messages
// per tick (chat-turn-integrity-01PMZ606 WP03 — the latter was the P0:
// a healthy multi-tick turn wrote up to six copies of its own answer
// into the transcript, each one flagged as a streaming failure;
// spec.md §1.1).
//
// FR-002 (agent-loop-robustness-parity): streamed output is flushed to
// durable storage periodically during a turn (not only at turn end /
// on backend-error), closing the crash-loss window.
const partialFlushInterval = 10 * time.Second

// runPeriodicFlush drives periodic checkpoint upserts for the duration
// of a kernel run. It is started as a goroutine in driveRun and exits
// when ctx is cancelled (i.e. the run ends).
//
// Design contract:
//   - Calls StreamCheckpointWriter.UpsertStreamCheckpoint at most once
//     per interval.
//   - Skips a tick when no new text has been accumulated since the last
//     flush (flushWatermark tracking).
//   - Never blocks driveRun: writes use a separate 5 s deadline.
//   - Non-fatal: flush errors are logged and ignored so a transient
//     write failure does not interrupt the streaming run.
//
// This is a durability checkpoint, not a transcript write — it never
// creates a session_messages row on its own. driveRun deletes the
// checkpoint on both the clean-close and error-close terminal paths
// (chat_runner.go); only an actual crash leaves it behind, which is
// the FR-002 window this closes. Promoting a surviving checkpoint into
// a resumable message on the next boot is deliberately out of scope
// (E-006, spec.md §12).
func runPeriodicFlush(
	ctx context.Context,
	sessionID, subID string,
	bridge *StreamBridge,
	writer StreamCheckpointWriter,
	interval time.Duration,
) {
	if writer == nil || bridge == nil {
		return
	}
	if interval <= 0 {
		interval = partialFlushInterval
	}

	var lastFlushedLen int
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			text, hasTool := bridge.PartialState()
			if len(text) <= lastFlushedLen {
				// No new content since last flush — skip this tick.
				continue
			}
			lastFlushedLen = len(text)

			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := writer.UpsertStreamCheckpoint(flushCtx, sessionID, subID, text, hasTool)
			cancel()
			if err != nil {
				logging.L().Warn("chat.partial_flush.periodic.failed",
					"session_id", sessionID,
					"sub_id", subID,
					"bytes", len(text),
					"err", err.Error(),
				)
			} else {
				logging.L().Debug("chat.partial_flush.periodic.ok",
					"session_id", sessionID,
					"sub_id", subID,
					"bytes", len(text),
				)
			}
		}
	}
}
