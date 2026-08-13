package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// isContextOverflowError reports whether err looks like a context-window
// overflow from the provider: either an ErrInvalidRequest (400) whose
// message mentions context/token/length keywords, or an ErrTransient
// with a 413 status (Request Entity Too Large).
//
// This mirrors the fallback evaluator's TriggerErrorContextOverflow logic
// so the chat runner can intercept overflows that slip past the
// capability-table pre-check (FR-005 / agent-loop-robustness-parity WP05).
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	var inv *corellm.ErrInvalidRequest
	if errors.As(err, &inv) && inv.Status == 400 {
		msg := strings.ToLower(inv.Message)
		return strings.Contains(msg, "context") ||
			strings.Contains(msg, "token") ||
			strings.Contains(msg, "length") ||
			strings.Contains(msg, "too long") ||
			strings.Contains(msg, "maximum context")
	}
	var tr *corellm.ErrTransient
	if errors.As(err, &tr) && tr.Status == 413 {
		return true
	}
	return false
}

// attemptOverflowRecovery tries to compact the session and returns nil
// on success, or a non-nil error if compaction is not possible or failed.
// On success the caller should re-drive the kernel run (compact-and-retry).
//
// FR-005: an overflow that slips the capability table triggers a
// recompute-max_tokens / compact-and-retry path instead of failing the turn.
//
// This is a best-effort path: we use the aggressive compaction tier
// (summarizePct=0.5) to free as much context as possible. If the deps
// are not wired or the engine errors, we return the original error so the
// run exits normally.
func attemptOverflowRecovery(
	ctx context.Context,
	sessionID string,
	profileID string,
	modelOverride string,
	deps *CompactionDeps,
) error {
	if deps == nil || deps.Engine == nil {
		return errors.New("overflow: compaction engine not wired")
	}

	ref := compaction.ProviderProfileRef{ProviderID: profileID, ModelID: modelOverride}
	if deps.CompactionModel != nil {
		if r, ok := deps.CompactionModel(); ok && (r.ProviderID != "" || r.ModelID != "") {
			ref = r
		}
	}

	// Use the aggressive summarize fraction (50%) to free as much context as
	// possible — this is a "last-resort" compact so we want maximum shrinkage.
	const overflowSummarizePct = 0.5

	compactCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := deps.Engine.Compact(compactCtx, sessionID, ref, overflowSummarizePct)
	if err != nil {
		logging.L().Warn("chat.overflow_recovery.compact_failed",
			"session_id", sessionID,
			"err", err.Error(),
		)
		return err
	}

	logging.L().Info("chat.overflow_recovery.compact_ok",
		"session_id", sessionID,
		"profile_id", profileID,
		"model", modelOverride,
	)
	return nil
}
