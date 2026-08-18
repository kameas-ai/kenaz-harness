package chat

// merge_suggestion.go — engineer-truth-pass-01PMTP01 WP08 (finding B16):
// the chat-runner trigger that turns a clean driveRun completion on an
// active branch's child session into a "does this look ready to merge?"
// broker event.
//
// A branch's child session is chatted through the ordinary ChatRunner
// path — there is no separate background child-run mechanism in
// production today (BranchSeamAdapter.WaitForChildRun is a documented
// no-op; see env_deps_branch.go). So "the child run completed" is
// observable exactly where every other chat turn's completion is
// observable: a clean driveRun exit. This mirrors autotitle.go's
// fire-and-forget trigger shape.

import (
	"context"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// mergeSuggestionTimeout bounds the ActiveBranchForChildSession lookup.
// The check is a single indexed row read in production; 5s matches the
// other post-run triggers' NFR-001-style budget.
const mergeSuggestionTimeout = 5 * time.Second

// MergeSuggestionPayload is the broker payload emitted on the
// "branches:merge-suggested" topic. Mirrors the frontend's local
// MergeSuggestionPayload interface in useEventToasts.ts.
type MergeSuggestionPayload struct {
	BranchID string `json:"branchId"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

// fireMergeSuggestion is called as a goroutine after a chat run
// completes cleanly (not paused, not errored, not stopped). When
// sessionID is the live child session of a still-active branch, it
// evaluates the merge heuristic against this turn's outcome and, when
// it fires, emits branches:merge-suggested so the frontend toast
// (useEventToasts.ts, "Merge now" → client.branches.merge) can offer
// it.
//
// No-ops silently (after logging) for every session that isn't an
// active branch's child — which is the overwhelming majority of chat
// turns — so this never adds visible cost to ordinary sessions.
func (r *ChatRunner) fireMergeSuggestion(sessionID string, branchSeam coreag.BranchSeam, suggester *coreag.MergeSuggester, lastAssistantMsg string) {
	if branchSeam == nil || suggester == nil {
		return
	}
	log := logging.L()
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("chat.merge_suggestion.panic", "session_id", sessionID, "panic", rec)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), mergeSuggestionTimeout)
	defer cancel()

	branchID, ok := branchSeam.ActiveBranchForChildSession(ctx, sessionID)
	if !ok {
		return
	}

	// RunComplete is deliberately FALSE here. It means "the child kernel
	// run reached a terminal state (run_complete event)" — and in this
	// architecture there is no background child kernel run to reach one:
	// BranchSeamAdapter.WaitForChildRun is a documented no-op and the
	// branch's child session is chatted one interactive turn at a time.
	// Passing true would short-circuit MergeSuggester.Inspect's first
	// arm unconditionally (see branch_merge_suggester.go), which has two
	// consequences: every clean turn on a branch child emits, including
	// the branch's very first reply, under the copy "Branch run finished
	// — Child run reached a terminal state." (false); and the suggester's
	// terminal-token and idle-timeout rules become unreachable from the
	// only production caller — i.e. the wiring would leave two thirds of
	// the heuristic dead, which is the exact defect class this mission
	// exists to remove. Leaving it false lets the terminal-token rule
	// decide, so the toast fires when the child's reply actually reads
	// like a conclusion. (engineer-truth-pass-01PMTP01 WP08 review.)
	suggestion := suggester.Inspect(coreag.ChildRunStatus{
		BranchID:         branchID,
		LastAssistantMsg: lastAssistantMsg,
		LastActivityAt:   time.Now().UTC(),
		RunComplete:      false,
		// The turn just ended, so nothing is in flight. LastActivityAt is
		// "now", so the idle rule cannot fire on this path; it stays
		// reachable for a future caller that inspects a parked child.
		HasInflightNodes: false,
	})
	if suggestion.Reason == "" {
		log.Debug("chat.merge_suggestion.no_fire", "session_id", sessionID, "branch_id", branchID)
		return
	}

	if r.cfg.Broker == nil {
		return
	}
	r.cfg.Broker.Emit("branches:merge-suggested", MergeSuggestionPayload{
		BranchID: suggestion.BranchID,
		Reason:   string(suggestion.Reason),
		Detail:   suggestion.Detail,
	})
	log.Info("chat.merge_suggestion.fired",
		"session_id", sessionID, "branch_id", branchID, "reason", string(suggestion.Reason))
}
