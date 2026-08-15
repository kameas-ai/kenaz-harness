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

// refSplits reports whether ANY tool id has an opener strictly before
// idx and a closer at or after it. Deliberately conservative about a
// repeated id (any opener, any closer) — that is also the reading the
// production helper takes, because when one provider id appears twice
// there is no way to tell from the row alone which closer answers which
// opener, and keeping the whole run intact is the only safe resolution.
func refSplits(msgs []SessionMessage, idx int) (string, bool) {
	opens := map[string][]int{}
	closes := map[string][]int{}
	for i, m := range msgs {
		if m.ToolUseID != "" {
			opens[m.ToolUseID] = append(opens[m.ToolUseID], i)
		}
		if m.ToolResultForID != "" {
			closes[m.ToolResultForID] = append(closes[m.ToolResultForID], i)
		}
	}
	for id, os := range opens {
		before := false
		for _, o := range os {
			if o < idx {
				before = true
			}
		}
		if !before {
			continue
		}
		for _, c := range closes[id] {
			if c >= idx {
				return id, true
			}
		}
	}
	return "", false
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

// TestSnapBoundaryForToolPairs_ExhaustiveOverEveryCutPoint asserts the
// forward clamp lands on a boundary no pair straddles, from every
// starting index, on every shape.
//
// Mutation evidence: replace straddler's two scans with the pre-WP06
// test of messages[idx] / messages[idx-1] only, and this fails on
// "a pair straddling two unrelated rows" (idx=3),
// "a pair split by an interleaved second pair" (idx=3) and
// "three calls, the middle one unanswered" (idx=3).
func TestSnapBoundaryForToolPairs_ExhaustiveOverEveryCutPoint(t *testing.T) {
	t.Parallel()
	for name, msgs := range snapShapes() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for idx := 0; idx <= len(msgs); idx++ {
				got := snapBoundaryForToolPairs(msgs, idx)
				if got < idx {
					t.Errorf("idx=%d: forward clamp moved BACKWARD to %d", idx, got)
				}
				if id, split := refSplits(msgs, got); split {
					t.Errorf("idx=%d → %d: pair %q still straddles the boundary. "+
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
	for name, msgs := range snapShapes() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for idx := 0; idx <= len(msgs); idx++ {
				got := snapBoundaryBackForToolPairs(msgs, idx)
				if got > idx {
					t.Errorf("idx=%d: backward clamp moved FORWARD to %d — it would be "+
						"able to undo the recent-window guarantee it runs after", idx, got)
				}
				if id, split := refSplits(msgs, got); split {
					t.Errorf("idx=%d → %d: pair %q still straddles the boundary", idx, got, id)
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
// Mutation evidence: delete step 4b from engine.Compact and the
// wiring-level sweep loses its guarantee; run the forward clamp there
// instead of the backward one and the recent-window assertion below
// fails.
func TestSnapClampsCompose(t *testing.T) {
	t.Parallel()
	for name, msgs := range snapShapes() {
		for _, rw := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("%s/window=%d", name, rw), func(t *testing.T) {
				t.Parallel()
				for start := 0; start <= len(msgs); start++ {
					b := snapBoundaryForToolPairs(msgs, start)
					b = snapBoundaryForRecentWindow(msgs, b, rw)
					afterWindow := b
					b = snapBoundaryBackForToolPairs(msgs, b)

					if id, split := refSplits(msgs, b); split {
						t.Errorf("start=%d window=%d: final boundary %d still splits %q",
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
