package compaction

// session_snap_exhaustive_test.go — model-moves-transcript-01PMCH01 WP06.
//
// The end-to-end sweep in wiring/pairing_sql_test.go drives real
// percentages through the real engine, which means it only ever visits
// the boundary indices those percentages happen to produce. That is the
// right test for "does the shipped path work" and the wrong test for
// "is the clamp correct", because a clamp can be wrong at an index the
// arithmetic never lands on — and then land on it the first time a user
// has a slightly different session. (Measured: reverting the clamp to
// its pre-WP06 two-index form does NOT fail the end-to-end sweep. It
// fails this file at three shapes.)
//
// So this file is exhaustive instead of representative: every shape is
// checked at EVERY boundary index from 0 to len(messages).
//
// The oracle is an INDEPENDENT restatement of the invariant —
// refSplits below — written from the provider's rule rather than from
// straddler's implementation. A test that called straddler to decide
// whether straddler was right would pass for any straddler.

import (
	"fmt"
	"testing"
)

// refSplits reports whether the boundary idx falls strictly inside one
// of the pairs listed in pairs — i.e. lo < idx <= hi.
//
// THE ORACLE IS HAND-WRITTEN DATA, NOT A SECOND ALGORITHM.
//
// pairs comes from snapWantPairs() below, which spells out by hand,
// per shape, which opener answers which closer. That is what makes this
// an independent restatement: a re-derived matching would be the
// production matcher written twice, and would agree with it for the same
// wrong reason.
//
// The earlier version of this oracle keyed on the id alone — "any opener
// before idx, any closer at or after it" — and its comment argued that a
// repeated provider id is irresolvable from the row, so fusing the whole
// run is the only safe reading. Fusing is not safe; it is a different
// failure. See toolPairSpans in session_snap.go for the measurement, and
// TestSnapClamps_RepeatedIDStillMakesProgress below for the pin.
func refSplits(pairs [][2]int, idx int) ([2]int, bool) {
	for _, p := range pairs {
		if p[0] < idx && idx <= p[1] {
			return p, true
		}
	}
	return [2]int{}, false
}

func snapUse(id string) SessionMessage {
	return SessionMessage{ID: "use-" + id, Role: "tool", Content: "bash(cmd=<string>)", ToolUseID: id}
}

func snapRes(id string) SessionMessage {
	return SessionMessage{ID: "res-" + id, Role: "tool", Content: "output " + id, ToolResultForID: id}
}

func snapRow(role, id string) SessionMessage {
	return SessionMessage{ID: id, Role: role, Content: "text " + id}
}

// snapShapes is the adversarial matrix. Each name states the hazard it
// contributes; several exist specifically because the pre-WP06 helper
// inspected only messages[idx] and messages[idx-1].
func snapShapes() map[string][]SessionMessage {
	return map[string][]SessionMessage{
		"sequential pair": {
			snapRow("user", "u"), snapUse("A"), snapRes("A"), snapRow("assistant", "f"),
		},
		"interleaved pairs (parallel dispatch, out of order)": {
			snapRow("user", "u"), snapUse("A"), snapUse("B"),
			snapRes("B"), snapRes("A"), snapRow("assistant", "f"),
		},
		"nested pairs (both open before either answers)": {
			snapRow("user", "u"), snapUse("A"), snapUse("B"),
			snapRes("A"), snapRes("B"), snapRow("assistant", "f"),
		},
		// THE two-index killer: with two unrelated rows between the
		// opener and the closer, a boundary in the middle sees neither
		// half at messages[idx] nor at messages[idx-1].
		"a pair straddling two unrelated rows": {
			snapRow("user", "u"), snapUse("A"), snapRow("assistant", "m1"),
			snapRow("system", "m2"), snapRes("A"), snapRow("assistant", "f"),
		},
		"a pair split by an interleaved second pair": {
			snapRow("user", "u"), snapUse("A"), snapRow("assistant", "m"),
			snapUse("B"), snapRes("B"), snapRes("A"), snapRow("assistant", "f"),
		},
		"a compaction summary landing inside a pair": {
			snapRow("user", "u"), snapUse("A"),
			snapRow("system", "[Earlier conversation summary: …]"),
			snapRes("A"), snapRow("assistant", "f"),
		},
		"one provider id reused across two turns": {
			snapRow("user", "u1"), snapUse("A"), snapRes("A"), snapRow("assistant", "f1"),
			snapRow("user", "u2"), snapUse("A"), snapRes("A"), snapRow("assistant", "f2"),
		},
		"a result stored before its call": {
			snapRow("user", "u"), snapRes("A"), snapUse("A"), snapRow("assistant", "f"),
		},
		"an interrupted turn: a call with no result": {
			snapRow("user", "u"), snapUse("A"), snapRow("assistant", "f"),
		},
		"a synthetic result whose call was never written": {
			snapRow("user", "u"), snapRes("A"), snapRow("assistant", "f"),
		},
		"three calls, the middle one unanswered": {
			snapRow("user", "u"), snapUse("A"), snapUse("B"), snapUse("C"),
			snapRes("C"), snapRes("A"), snapRow("assistant", "f"),
		},
		"back-to-back pairs with no separator": {
			snapUse("A"), snapRes("A"), snapUse("B"), snapRes("B"),
			snapUse("C"), snapRes("C"),
		},
		// A pair straddling a USER row is the one shape that separates
		// the three-clamp composition from the two-clamp one, because
		// snapBoundaryForRecentWindow always comes to rest ON a user row
		// (it decrements until the count clears, and the row that clears
		// it is a user row) or on 0. Every other landing spot is already
		// pair-safe from the forward clamp, so this is where the third
		// clamp earns its place — see TestSnapClampsCompose.
		//
		// No writer produces this today: a human turn opens with a user
		// row and all of its moves follow. It is included anyway, and
		// deliberately, because the forward clamp's ORIGINAL comment made
		// exactly that argument about a different shape ("a genuine
		// agentic turn never interleaves an unrelated message between a
		// tool_use and its results") and parallel dispatch had already
		// falsified it. These helpers take an arbitrary row sequence and
		// promise the boundary does not split a pair; the promise is
		// tested against arbitrary rows, not against the rows one release
		// of one writer happens to emit.
		"a pair straddling a user row": {
			snapUse("A"), snapRow("user", "u2"), snapRes("A"), snapRow("assistant", "f"),
		},
	}
}

// snapWantPairs states, by hand and per shape, which opener answers
// which closer — the [lo, hi] index span of every real pair. Shapes whose
// tool rows are all half-pairs map to no spans at all.
//
// This table is the oracle. It is written from reading the shape, not
// from running the matcher, and TestToolPairSpans_MatchTheHandWrittenTable
// pins the matcher against it.
func snapWantPairs() map[string][][2]int {
	return map[string][][2]int{
		"sequential pair": {{1, 2}},
		"interleaved pairs (parallel dispatch, out of order)": {{2, 3}, {1, 4}},
		"nested pairs (both open before either answers)":      {{1, 3}, {2, 4}},
		"a pair straddling two unrelated rows":                {{1, 4}},
		"a pair split by an interleaved second pair":          {{3, 4}, {1, 5}},
		"a compaction summary landing inside a pair":          {{1, 3}},
		// The two exchanges are each complete. Nothing spans the gap
		// between them, so a boundary at 4 splits nothing.
		"one provider id reused across two turns":         {{1, 2}, {5, 6}},
		"a result stored before its call":                 {{1, 2}},
		"an interrupted turn: a call with no result":      nil,
		"a synthetic result whose call was never written": nil,
		"three calls, the middle one unanswered":          {{3, 4}, {1, 5}},
		"back-to-back pairs with no separator":            {{0, 1}, {2, 3}, {4, 5}},
		"a pair straddling a user row":                    {{0, 2}},
	}
}

// TestToolPairSpans_MatchTheHandWrittenTable pins the matcher against the
// oracle data every other test in this file is judged by. Without it the
// oracle could drift into agreement with a broken matcher.
func TestToolPairSpans_MatchTheHandWrittenTable(t *testing.T) {
	t.Parallel()
	want := snapWantPairs()
	for name, msgs := range snapShapes() {
		got := toolPairSpans(msgs)
		set := map[[2]int]bool{}
		for _, s := range got {
			set[s] = true
		}
		w := want[name]
		if len(got) != len(w) {
			t.Errorf("%s: toolPairSpans = %v, want %v", name, got, w)
			continue
		}
		for _, s := range w {
			if !set[s] {
				t.Errorf("%s: toolPairSpans = %v, missing hand-written pair %v", name, got, s)
			}
		}
	}
}

// TestSnapClamps_RepeatedIDStillMakesProgress is the regression pin for
// the over-merge the first WP06 draft introduced.
//
// Keying the straddle test on the id alone bound the FIRST mention of a
// repeated id to the LAST, so on this session — eight turns, first and
// last sharing an id, which is what a local runtime that numbers its tool
// calls per request produces — the composed clamps returned boundary 2
// for EVERY requested cut point at recentWindow=2, and 39 (summarize the
// newest turn too) at recentWindow=0. Both are "compaction that cannot
// compact"; neither is safe.
//
// The assertion is that the boundary TRACKS the request rather than
// collapsing to a constant, and does it while still never splitting a
// pair.
func TestSnapClamps_RepeatedIDStillMakesProgress(t *testing.T) {
	t.Parallel()
	var msgs []SessionMessage
	var pairs [][2]int
	for turn := 0; turn < 8; turn++ {
		id := fmt.Sprintf("t%d", turn)
		if turn == 0 || turn == 7 {
			id = "call_0" // the repeated id
		}
		msgs = append(msgs, snapRow("user", fmt.Sprintf("u%d", turn)))
		msgs = append(msgs, snapRow("assistant", fmt.Sprintf("m%d", turn)))
		use := len(msgs)
		msgs = append(msgs, snapUse(id))
		msgs = append(msgs, snapRes(id))
		pairs = append(pairs, [2]int{use, use + 1})
		msgs = append(msgs, snapRow("assistant", fmt.Sprintf("f%d", turn)))
	}

	for _, rw := range []int{0, 2} {
		distinct := map[int]bool{}
		for start := 1; start < len(msgs); start++ {
			b := snapBoundaryForToolPairs(msgs, start)
			b = snapBoundaryForRecentWindow(msgs, b, rw)
			b = snapBoundaryBackForToolPairs(msgs, b)
			distinct[b] = true
			if p, split := refSplits(pairs, b); split {
				t.Errorf("rw=%d start=%d → %d: pair %v still straddles", rw, start, b, p)
			}
			// A boundary that lands inside a turn's own pair legitimately
			// moves by one; what must not happen is a repeated id dragging
			// it across whole turns.
			if b > start+1 {
				t.Errorf("rw=%d start=%d → %d: the clamps compacted PAST the request by "+
					"more than one row; a repeated id must not drag the boundary over "+
					"turns that are already complete", rw, start, b)
			}
		}
		// 39 achievable boundaries collapsing to one or two values is the
		// pathology; a healthy clamp reaches a boundary per turn at least.
		if len(distinct) < 8 {
			t.Errorf("rw=%d: %d distinct boundaries across %d cut points — the clamps "+
				"collapsed onto a constant, so this session cannot be compacted at any "+
				"requested percentage", rw, len(distinct), len(msgs)-1)
		}
	}
}

// TestSnapBoundaryForToolPairs_ExhaustiveOverEveryCutPoint asserts the
// forward clamp lands on a boundary no pair straddles, from every
// starting index, on every shape.
//
// Mutation evidence: replace straddler with the pre-WP06 test of
// messages[idx] / messages[idx-1] only, and this fails at four shapes —
// "a pair straddling two unrelated rows", "a pair split by an
// interleaved second pair", "three calls, the middle one unanswered"
// and "one provider id reused across two turns".
func TestSnapBoundaryForToolPairs_ExhaustiveOverEveryCutPoint(t *testing.T) {
	t.Parallel()
	want := snapWantPairs()
	for name, msgs := range snapShapes() {
		pairs := want[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for idx := 0; idx <= len(msgs); idx++ {
				got := snapBoundaryForToolPairs(msgs, idx)
				if got < idx {
					t.Errorf("idx=%d: forward clamp moved BACKWARD to %d", idx, got)
				}
				if id, split := refSplits(pairs, got); split {
					t.Errorf("idx=%d → %d: pair %v still straddles the boundary. "+
						"The span archives messages[:%d] and leaves a half-pair on one "+
						"side of it; the provider rejects the request the surviving half "+
						"reaches it in.", idx, got, id, got)
				}
			}
		})
	}
}

// TestSnapBoundaryBackForToolPairs_ExhaustiveOverEveryCutPoint is the
// same assertion for the backward clamp — the one the engine runs LAST,
// after the recent-window clamp has been free to move the boundary left.
func TestSnapBoundaryBackForToolPairs_ExhaustiveOverEveryCutPoint(t *testing.T) {
	t.Parallel()
	want := snapWantPairs()
	for name, msgs := range snapShapes() {
		pairs := want[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for idx := 0; idx <= len(msgs); idx++ {
				got := snapBoundaryBackForToolPairs(msgs, idx)
				if got > idx {
					t.Errorf("idx=%d: backward clamp moved FORWARD to %d — it would be "+
						"able to undo the recent-window guarantee it runs after", idx, got)
				}
				if id, split := refSplits(pairs, got); split {
					t.Errorf("idx=%d → %d: pair %v still straddles the boundary", idx, got, id)
				}
			}
		})
	}
}

// TestSnapClampsCompose pins the property the engine's step 3 / step 4 /
// step 4b ordering depends on: whatever the recent-window clamp does to
// the boundary in between, the backward clamp restores pair integrity
// WITHOUT taking back the recent-window guarantee.
//
// Mutation evidence: replace snapBoundaryBackForToolPairs with the
// identity function and this fails; run the FORWARD clamp in its place
// and the recent-window assertion below fails.
//
// What it cannot see is whether engine.Compact actually CALLS the third
// clamp — this composes the helpers by hand. An earlier draft of this
// comment claimed deleting step 4b from the engine would fail here; it
// does not, and TestCompact_BackwardClampIsLoadBearing exists because of
// that gap.
func TestSnapClampsCompose(t *testing.T) {
	t.Parallel()
	want := snapWantPairs()
	for name, msgs := range snapShapes() {
		pairs := want[name]
		for _, rw := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("%s/window=%d", name, rw), func(t *testing.T) {
				t.Parallel()
				for start := 0; start <= len(msgs); start++ {
					b := snapBoundaryForToolPairs(msgs, start)
					b = snapBoundaryForRecentWindow(msgs, b, rw)
					afterWindow := b
					b = snapBoundaryBackForToolPairs(msgs, b)

					if id, split := refSplits(pairs, b); split {
						t.Errorf("start=%d window=%d: final boundary %d still splits %v",
							start, rw, b, id)
					}
					if b > afterWindow {
						t.Errorf("start=%d window=%d: the final clamp moved the boundary "+
							"from %d to %d, shrinking the live tail the recent-window "+
							"clamp had just guaranteed", start, rw, afterWindow, b)
					}
				}
			})
		}
	}
}
