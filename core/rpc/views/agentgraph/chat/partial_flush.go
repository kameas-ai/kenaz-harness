package chat

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// partialFlushInterval is the period between periodic partial-flush
// calls during a live kernel run. 10 s gives a worst-case crash-loss
// window of ~10 s while keeping write amplification acceptable for a
// single UPDATE to session_messages.
//
// FR-002 (agent-loop-robustness-parity): streamed output is flushed to
// durable storage periodically during a turn (not only at turn end /
// on backend-error), closing the crash-loss window.
const partialFlushInterval = 10 * time.Second

// runPeriodicFlush drives periodic partial-persists for the duration
// of a kernel run. It is started as a goroutine in driveRun and exits
// when ctx is cancelled (i.e. the run ends).
//
// Design contract:
//   - Calls PartialPersister.PersistPartial at most once per interval.
//   - Skips a tick when no new text has been accumulated since the last
//     flush (flushWatermark tracking).
//   - Never blocks driveRun: writes use a separate 5 s deadline.
//   - Non-fatal: flush errors are logged and ignored so a transient
//     write failure does not interrupt the streaming run.
//
// The partial persister seam (`kind="transient"`, `recoverable=true`)
// is appropriate here because a periodic flush during a healthy run is
// not a failure state — it is an optimistic durability checkpoint.
func runPeriodicFlush(
	ctx context.Context,
	sessionID string,
	bridge *StreamBridge,
	persister PartialPersister,
	interval time.Duration,
) {
	if persister == nil || bridge == nil {
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
			text, _ := bridge.PartialState()
			if len(text) <= lastFlushedLen {
				// No new content since last flush — skip this tick.
				continue
			}
			lastFlushedLen = len(text)

			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := persister.PersistPartial(flushCtx, sessionID, text,
				"transient", /* kind: mid-run checkpoint */
				true,        /* recoverable: no tool_use has executed at this point */
			)
			cancel()
			if err != nil {
				logging.L().Warn("chat.partial_flush.periodic.failed",
					"session_id", sessionID,
					"bytes", len(text),
					"err", err.Error(),
				)
			} else {
				logging.L().Debug("chat.partial_flush.periodic.ok",
					"session_id", sessionID,
					"bytes", len(text),
				)
			}
		}
	}
}
