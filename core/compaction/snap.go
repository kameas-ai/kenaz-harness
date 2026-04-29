package compaction

// snap.go houses the two pure boundary-clamp helpers the threshold-mode
// engine uses to keep span endpoints honest:
//
//   - snapBoundaryForToolPairs guarantees a span boundary never splits
//     a tool_use / tool_result pair (plan §2.7). When the proposed
//     boundary lands inside such a pair, we extend the span forward
//     (toward older messages) until the pair is whole or fully outside
//     the span.
//
//   - snapBoundaryForRecentWindow guarantees the engine never compacts
//     into the most-recent N user turns (plan §2.6). The recent window
//     protects the user's working context — a session that just typed
//     "yes please" doesn't want that turn folded into a summary.
//
// Both helpers operate on []Message indexed left-to-right by ascending
// sequence (i.e. messages[0] is oldest). The "boundary" is the first
// index that is NOT part of the compaction span — everything strictly
// less than the boundary is summarized; everything at or after is kept
// live. A boundary of 0 means "no compaction" (no-op); a boundary of
// len(messages) would mean "summarize everything" but in practice the
// recent-window clamp pulls it back below len.

// snapBoundaryForToolPairs walks idx forward (toward higher indices,
// shrinking the proposed span) past any tool_use whose matching
// tool_result lies AT or AFTER idx. Splitting a pair would produce an
// orphan tool_result in the post-compaction transcript — providers
// reject that — so we'd rather summarize a few extra turns than risk
// it.
//
// The implementation is symmetric in spirit (boundary lands inside a
// pair → push it past the pair) but biased one direction: we always
// push the boundary FORWARD (higher index), which means the pair stays
// fully OUTSIDE the span (kept live). This is the safer choice — folding
// half a tool exchange into a summary loses too much context.
//
// Returns idx unchanged when no clamp is needed.
func snapBoundaryForToolPairs(messages []Message, idx int) int {
	if idx <= 0 || idx >= len(messages) {
		return idx
	}

	// Build a forward map: ToolUseID → index of the message carrying that
	// tool_use. We need this so a tool_result message at position k can
	// find its opener efficiently.
	openers := map[string]int{}
	for i, m := range messages {
		if m.ToolUseID != "" {
			openers[m.ToolUseID] = i
		}
	}

	// Walk: if the message AT idx is a tool_result whose opener sits
	// strictly before idx, the pair is split. Push idx forward past the
	// tool_result so the pair sits fully outside the span (kept live).
	//
	// Symmetrically, if the message AT idx-1 is a tool_use whose closer
	// sits at or after idx, the pair is also split — push idx forward
	// past the closer.
	//
	// We loop because a snap-forward can land on a fresh split (back-to-
	// back tool exchanges).
	for idx < len(messages) {
		moved := false

		// Case 1: messages[idx] is a tool_result whose opener is before idx.
		if idx < len(messages) {
			cur := messages[idx]
			if cur.ToolResultForID != "" {
				if openerIdx, ok := openers[cur.ToolResultForID]; ok && openerIdx < idx {
					idx++
					moved = true
				}
			}
		}

		// Case 2: messages[idx-1] opened a tool_use whose closer is at-or-after idx.
		if idx > 0 && idx <= len(messages) {
			prev := messages[idx-1]
			if prev.ToolUseID != "" {
				closerIdx := findToolResultIndex(messages, prev.ToolUseID)
				if closerIdx >= idx {
					idx = closerIdx + 1
					moved = true
				}
			}
		}

		if !moved {
			return idx
		}
	}

	return idx
}

// findToolResultIndex returns the index of the message whose
// ToolResultForID matches toolUseID, or -1 if none exists. Used by the
// snap helper; small enough to keep linear.
func findToolResultIndex(messages []Message, toolUseID string) int {
	for i, m := range messages {
		if m.ToolResultForID == toolUseID {
			return i
		}
	}
	return -1
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
func snapBoundaryForRecentWindow(messages []Message, idx int, recentWindow int) int {
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
