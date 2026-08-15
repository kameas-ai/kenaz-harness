package compaction

// This file is part of the PERSISTED-HISTORY layer of the compaction
// package — the half that rewrites a session's stored transcript,
// as opposed to the in-memory FR-041 strategy layer it now sits beside.
// It moved here from core/compaction in
// agentgraph-total-convergence-01PMGX01 WP10a, which merged the
// harness's two compaction packages into one: the two are layers of a
// single subsystem, not two subsystems. The `session_` filename prefix
// marks the layer at a glance; compactor.go carries the package-level
// map of how the layers fit together.
//
// session_snap.go houses the pure boundary-clamp helpers the threshold-mode
// engine uses to keep span endpoints honest:
//
//   - snapBoundaryForToolPairs / snapBoundaryBackForToolPairs guarantee a
//     span boundary never splits a tool_use / tool_result pair (plan
//     §2.7). Both are written against one shared predicate, straddler;
//     they differ only in which side of the split pair the boundary is
//     moved to. The engine runs the forward clamp before the
//     recent-window clamp and the backward clamp after it — see
//     snapBoundaryBackForToolPairs for why a single direction cannot
//     hold the invariant on its own.
//
//   - snapBoundaryForRecentWindow guarantees the engine never compacts
//     into the most-recent N user turns (plan §2.6). The recent window
//     protects the user's working context — a session that just typed
//     "yes please" doesn't want that turn folded into a summary.
//
// Both helpers operate on []SessionMessage indexed left-to-right by ascending
// sequence (i.e. messages[0] is oldest). The "boundary" is the first
// index that is NOT part of the compaction span — everything strictly
// less than the boundary is summarized; everything at or after is kept
// live. A boundary of 0 means "no compaction" (no-op); a boundary of
// len(messages) would mean "summarize everything" but in practice the
// recent-window clamp pulls it back below len.

// straddler reports a pair that the boundary idx splits: an opener at
// index o and its closer at index c with o < idx <= c. ok is false when
// no pair straddles idx.
//
// This is the ONE definition of "the boundary splits a pair", and both
// clamp directions below are written against it. Before WP06 the
// forward clamp open-coded a narrower test — it looked only at
// messages[idx] and messages[idx-1] — which is correct for a pair whose
// two halves are adjacent to the cut and blind to every other split. A
// single unrelated row between the opener and the boundary
//
//	[tool_use A] [assistant text] | [tool_result A]
//
// walked straight through it: messages[idx] is the result, but its
// opener is at idx-2, and messages[idx-1] opened nothing. Parallel
// tool_dispatch produces exactly that shape whenever two calls resolve
// out of order, so this was not a theoretical hole.
func straddler(messages []SessionMessage, idx int) (o, c int, ok bool) {
	if idx <= 0 || idx >= len(messages) {
		return 0, 0, false
	}
	// openers maps a tool id to the LAST index that opened it, and
	// closers to the LAST index that closed it. Last-wins matters when a
	// provider id repeats across turns: pairing the newest opener with
	// the newest closer keeps the most recent exchange whole, and the
	// scan below re-runs until no pair straddles, so an older repeat is
	// caught on a later pass.
	openers := map[string]int{}
	closers := map[string]int{}
	for i, m := range messages {
		if m.ToolUseID != "" {
			openers[m.ToolUseID] = i
		}
		if m.ToolResultForID != "" {
			closers[m.ToolResultForID] = i
		}
	}
	// Scan every row on the older side for an opener answered on the
	// newer side, and every row on the newer side for a closer opened on
	// the older side. Both directions are needed: with a repeated id the
	// two maps can disagree about which half is the "same" pair, and
	// either disagreement is still a split.
	for i := 0; i < idx; i++ {
		if id := messages[i].ToolUseID; id != "" {
			if ci, hit := closers[id]; hit && ci >= idx {
				return i, ci, true
			}
		}
	}
	for k := idx; k < len(messages); k++ {
		if id := messages[k].ToolResultForID; id != "" {
			if oi, hit := openers[id]; hit && oi < idx {
				return oi, k, true
			}
		}
	}
	return 0, 0, false
}

// snapBoundaryForToolPairs pushes idx FORWARD until no tool_use /
// tool_result pair straddles it — the whole pair ends up on the older
// (summarized) side.
//
// Splitting a pair leaves an orphan half in the post-compaction
// transcript. Providers reject a lone tool_use and a lone tool_result
// alike, as a 400 on the whole request, so the model-visible composition
// drops whichever half survived — meaning a split does not crash, it
// silently deletes a tool exchange from the model's memory. Summarizing
// a few extra turns is cheaper than either outcome.
//
// Direction note, because the pre-WP06 comment here had it backwards:
// idx is the first index NOT summarized, so pushing idx forward moves
// the pair INTO the span (it is summarized whole), not out of it.
// Whole-into-the-summary is the correct resolution for this clamp —
// the summarizer sees both halves and can describe the exchange.
//
// Terminates: each iteration strictly increases idx, bounded by
// len(messages).
func snapBoundaryForToolPairs(messages []SessionMessage, idx int) int {
	for idx > 0 && idx < len(messages) {
		_, c, ok := straddler(messages, idx)
		if !ok {
			return idx
		}
		idx = c + 1
	}
	return idx
}

// snapBoundaryBackForToolPairs pulls idx BACKWARD until no pair
// straddles it — the whole pair ends up on the newer (kept-live) side.
//
// WHY BOTH DIRECTIONS EXIST. The forward clamp cannot be the last word,
// because the engine applies a second clamp after it:
// snapBoundaryForRecentWindow moves idx LEFT to guarantee the live tail
// carries enough user turns. Moving left grows the span, and a span that
// grows can swallow the opener of a pair whose closer is still in the
// tail — re-splitting a pair the forward clamp had just made whole. The
// two clamps were applied in that order since the helper was written and
// nothing re-established pair integrity afterwards, so on a move-bearing
// session (many non-user rows per turn, which is exactly when the
// recent-window clamp fires) the guarantee was conditional on the
// recent-window clamp happening not to move.
//
// This clamp is the one that can run last, because pulling idx backward
// only ever GROWS the live tail — it cannot violate the recent-window
// guarantee it runs after, so the two clamps compose instead of fighting.
// It compacts strictly less than the forward clamp would; that is the
// price of running second, and under-compacting is the safe direction.
//
// Terminates: each iteration strictly decreases idx, bounded below by 0.
func snapBoundaryBackForToolPairs(messages []SessionMessage, idx int) int {
	for idx > 0 && idx < len(messages) {
		o, _, ok := straddler(messages, idx)
		if !ok {
			return idx
		}
		idx = o
	}
	return idx
}

// snapBoundaryForRecentWindow clamps idx so that at least recentWindow
// user-role messages sit AT or AFTER the boundary (i.e. survive the
// compaction). The "user turn" count is the unit the user reasons in
// when picking a window — assistant replies and tool exchanges sit
// within a turn but don't count toward the window themselves.
//
// If idx already leaves >= recentWindow user turns untouched, returns
// idx unchanged. Otherwise walks idx forward (toward higher indices,
// shrinking the span) until the post-boundary slice carries at least
// recentWindow user messages, OR until idx reaches 0 — meaning the
// span has been clamped away entirely (the compaction will be a no-op).
//
// Note the direction: we want >= recentWindow AT-OR-AFTER idx. So if
// the count is short, idx must move LEFT (smaller, exposing more of
// the tail) — sorry, the opposite of what step 4 of the plan describes
// in tail terms. Said precisely: idx is the first index NOT compacted;
// everything at-or-after idx is kept live; we need len(kept) >= window
// where kept = messages[idx:]. So if there aren't enough user messages
// in messages[idx:], we shrink idx (move it left) to expose more.
//
// recentWindow <= 0 disables the clamp.
func snapBoundaryForRecentWindow(messages []SessionMessage, idx int, recentWindow int) int {
	if recentWindow <= 0 {
		return idx
	}
	if idx <= 0 {
		return idx
	}

	// Count user messages currently AT-OR-AFTER idx (the live tail).
	userTurnsInTail := 0
	for i := idx; i < len(messages); i++ {
		if messages[i].Role == "user" {
			userTurnsInTail++
		}
	}

	if userTurnsInTail >= recentWindow {
		return idx
	}

	// Shrink idx until the tail carries enough user turns. We walk
	// backward, growing the tail one message at a time. Stop the moment
	// the count clears the threshold OR idx hits 0.
	for idx > 0 && userTurnsInTail < recentWindow {
		idx--
		if messages[idx].Role == "user" {
			userTurnsInTail++
		}
	}

	return idx
}
