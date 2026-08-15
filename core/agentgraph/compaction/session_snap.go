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
	for _, s := range toolPairSpans(messages) {
		if s[0] < idx && idx <= s[1] {
			return s[0], s[1], true
		}
	}
	return 0, 0, false
}

// toolPairSpans matches every opener to the ONE closer that answers it
// and returns the [lo, hi] index span of each matched pair. A row whose
// counterpart is absent contributes no span — it is already an orphan on
// the way in and no boundary can make it worse.
//
// WHY THE MATCH IS CHRONOLOGICAL AND NOT PER-ID
// (review of model-moves-transcript-01PMCH01 WP06).
//
// WP06's first version of this predicate keyed on the id alone: openers
// mapped an id to its LAST opening index, closers to its LAST closing
// index, and any opener-before / closer-after of the same id counted as
// a straddle. Its comment argued that when one provider id appears twice
// "there is no way to tell from the row alone which closer answers which
// opener, and keeping the whole run intact is the only safe resolution."
//
// The first half is true and the second does not follow. The transcript
// is ORDERED — that is the whole premise of a boundary index — so a
// result answers the most recent call of that id that nothing has
// answered yet. Reading the id globally instead binds the FIRST mention
// to the LAST, so one repeated id fuses every turn between them into a
// single indivisible run, and the two clamps then have nowhere legal to
// stand. Measured on an 8-turn / 40-row session whose first and last
// turns share an id: with recentWindow=2 the composed clamps returned
// boundary 2 for EVERY requested percentage — a session that can no
// longer be compacted at all — and with recentWindow=0 they returned 39,
// summarizing the newest turn along with everything else. Neither is a
// safe resolution; they are the two ways of not compacting.
//
// Repeated ids are not hypothetical on this product. Provider-issued
// ids (toolu_*, call_*) are effectively unique, but the harness ships a
// local-runtime lane (Ollama / LM Studio / llama.cpp / Jan, via the
// OpenAI-compatible wire in core/llm/openaiwire) where small models
// routinely number their tool calls per request, so call_0 in turn one
// and call_0 in turn nine are different calls with the same id.
//
// Chronological matching costs nothing in safety: a genuinely
// interleaved, nested or far-apart pair still produces one span covering
// both halves, which is what the clamps need. It only declines to invent
// a span between two exchanges that were each complete.
func toolPairSpans(messages []SessionMessage) [][2]int {
	var spans [][2]int
	// open[id] is the stack of opener indices for id that nothing has
	// answered yet — a stack, so the newest unanswered call is matched
	// first. pending[id] is the queue of closer indices that arrived
	// before any opener of that id (the result-stored-before-its-call
	// shape); the oldest is matched first.
	open := map[string][]int{}
	pending := map[string][]int{}
	for i, m := range messages {
		if id := m.ToolResultForID; id != "" {
			if st := open[id]; len(st) > 0 {
				spans = append(spans, [2]int{st[len(st)-1], i})
				open[id] = st[:len(st)-1]
			} else {
				pending[id] = append(pending[id], i)
			}
		}
		if id := m.ToolUseID; id != "" {
			if q := pending[id]; len(q) > 0 {
				spans = append(spans, [2]int{q[0], i})
				pending[id] = q[1:]
			} else {
				open[id] = append(open[id], i)
			}
		}
	}
	return spans
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
