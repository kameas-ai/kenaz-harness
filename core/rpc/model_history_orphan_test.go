package rpc

// model_history_orphan_test.go — the adversarial half of spec §5's
// "tool_use/tool_result orphans are the classic provider 400 — pinned".
//
// model_history_test.go pins the two orphan sources the WP knew about: a
// truncation window that cuts a pair, and a tool_call whose model-layer
// arguments are missing. Both walk the SAME canonical fixture — one
// sequential pair, one turn. This file attacks the sweep with the shapes
// a real session produces that the canonical fixture does not:
//
//	interleaved pairs        parallel tool_dispatch resolving out of order
//	nested pairs             two calls open before either answers
//	a result before its call  row order the sweep must not assume away
//	duplicate ids across turns two turns reusing one provider id
//	a pair split by a classic row   a compaction summary landing mid-pair
//	a synthetic orphan result  what WP02 persists for an interrupted turn
//	one unbuildable pair beside a healthy one
//	a multi-call assistant message with one call unanswered
//
// Each case is checked at EVERY truncation window, not just n=0, because
// the window is where the sweep's ordering invariant is load-bearing.
//
// The suite asserts BOTH directions. An orphan surviving is a provider
// 400; a NON-orphan being dropped is silent history loss, which is the
// failure a sweep written too aggressively produces and which no
// orphan-only assertion can see.

import (
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

func orphanToolCall(id string) modelHistoryRow {
	return modelHistoryRow{
		Role: "tool", Content: "t(arg=<string>)",
		MoveKind: string(session.MoveKindToolCall), ToolID: id, ToolName: "t",
		ModelToolArgs: map[string]string{id: `{"arg":"v"}`},
	}
}

func orphanToolResult(id string) modelHistoryRow {
	return modelHistoryRow{
		Role: "tool", Content: "output",
		MoveKind: string(session.MoveKindToolResult), ToolID: id, ToolName: "t",
	}
}

func orphanClassic(role, content string) modelHistoryRow {
	return modelHistoryRow{Role: role, Content: content}
}

func orphanFinal(content string) modelHistoryRow {
	return modelHistoryRow{Role: "assistant", Content: content,
		MoveKind: string(session.MoveKindFinal)}
}

// assertPairIntegrityEveryWindow composes rows at every tail window from
// 0 (no window) to len(rows)+1 and asserts, at each, that no half-pair
// survives and no role:"tool" message carries an empty ToolCallID — the
// exact shape that reaches the provider as a 400. At n=0 it additionally
// asserts that every id in wantKept survived, which is the "the sweep
// must not drop a non-orphan" direction.
func assertPairIntegrityEveryWindow(t *testing.T, name string,
	rows []modelHistoryRow, wantKept []string) {
	t.Helper()
	for n := 0; n <= len(rows)+1; n++ {
		got := composeModelHistory(rows, moveFidelityMoves, n)
		calls, results := map[string]int{}, map[string]int{}
		for _, m := range got {
			for _, tc := range m.ToolCalls {
				calls[tc.ID]++
			}
			if m.Role == "tool" {
				if m.ToolCallID == "" {
					t.Errorf("[%s n=%d] a role:%q message survived with an EMPTY "+
						"ToolCallID — this is the pre-WP03 defect verbatim", name, n, "tool")
					continue
				}
				results[m.ToolCallID]++
			}
		}
		for id := range calls {
			if results[id] == 0 {
				t.Errorf("[%s n=%d] orphaned tool_use %q — no tool_result answers it", name, n, id)
			}
		}
		for id := range results {
			if calls[id] == 0 {
				t.Errorf("[%s n=%d] orphaned tool_result %q — no tool_use precedes it", name, n, id)
			}
		}
		if n != 0 {
			continue
		}
		for _, id := range wantKept {
			if calls[id] == 0 || results[id] == 0 {
				t.Errorf("[%s] the sweep DROPPED the intact pair %q (calls=%d results=%d) — "+
					"a sweep that removes non-orphans loses history silently",
					name, id, calls[id], results[id])
			}
		}
	}
}

func TestModelHistory_OrphanImpossibility_AdversarialShapes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		rows     []modelHistoryRow
		wantKept []string
	}{
		{
			// parallel tool_dispatch: B resolves before A.
			name: "interleaved pairs",
			rows: []modelHistoryRow{
				orphanClassic("user", "go"),
				orphanToolCall("A"), orphanToolCall("B"),
				orphanToolResult("B"), orphanToolResult("A"),
				orphanFinal("done"),
			},
			wantKept: []string{"A", "B"},
		},
		{
			name: "nested pairs",
			rows: []modelHistoryRow{
				orphanClassic("user", "go"),
				orphanToolCall("A"), orphanToolCall("B"),
				orphanToolResult("A"), orphanToolResult("B"),
				orphanFinal("done"),
			},
			wantKept: []string{"A", "B"},
		},
		{
			// The sweep is set-membership, not order-aware. This pins
			// that it at least never emits a HALF pair when the rows
			// arrive out of order — see the note in the test below about
			// what it deliberately does not check.
			name: "a result stored before its call",
			rows: []modelHistoryRow{
				orphanClassic("user", "go"),
				orphanToolResult("A"), orphanToolCall("A"),
				orphanFinal("done"),
			},
			wantKept: []string{"A"},
		},
		{
			name: "one provider id reused across two turns",
			rows: []modelHistoryRow{
				orphanClassic("user", "first"),
				orphanToolCall("A"), orphanToolResult("A"), orphanFinal("one"),
				orphanClassic("user", "second"),
				orphanToolCall("A"), orphanToolResult("A"), orphanFinal("two"),
			},
			wantKept: []string{"A"},
		},
		{
			// A compaction summary is a classic row and can land between
			// a call and its result. It must not break the pair.
			name: "a pair split by a classic compaction row",
			rows: []modelHistoryRow{
				orphanClassic("user", "go"),
				orphanToolCall("A"),
				orphanClassic("system", "[earlier turns compacted]"),
				orphanToolResult("A"),
				orphanFinal("done"),
			},
			wantKept: []string{"A"},
		},
		{
			// WP02 persists a synthetic tool_result when a turn is
			// stopped mid-dispatch; the call may never have been written.
			name: "an interrupted turn's synthetic result with no call",
			rows: []modelHistoryRow{
				orphanClassic("user", "go"),
				orphanToolResult("A"),
				orphanFinal("interrupted"),
			},
			wantKept: nil,
		},
		{
			// The unbuildable pair must go; the healthy one must stay.
			name: "an unbuildable pair beside a healthy one",
			rows: func() []modelHistoryRow {
				noArgs := orphanToolCall("A")
				noArgs.ModelToolArgs = nil
				return []modelHistoryRow{
					orphanClassic("user", "go"),
					noArgs, orphanToolResult("A"),
					orphanToolCall("B"), orphanToolResult("B"),
					orphanFinal("done"),
				}
			}(),
			wantKept: []string{"B"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertPairIntegrityEveryWindow(t, tc.name, tc.rows, tc.wantKept)
		})
	}
}

// TestPairIntegritySweep_PartiallyAnsweredMultiCallMessage attacks the
// one shape composeModelHistory cannot itself build but the kernel
// certainly can: ONE assistant message carrying several tool_calls
// (exec_dispatch's parallel path), only some of which are answered.
//
// The sweep must prune the unanswered call out of the array while
// keeping the answered one and keeping the message. Dropping the whole
// message would lose an answered pair; keeping the whole array would
// ship an orphaned tool_use.
//
// Mutation: in pairIntegritySweep, replace the per-call `kept` filter
// with a whole-message `continue` when ANY call is incomplete — the
// answered pair then disappears and this fails.
func TestPairIntegritySweep_PartiallyAnsweredMultiCallMessage(t *testing.T) {
	t.Parallel()
	got := pairIntegritySweep([]coreag.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []coreag.ToolCallRequest{
			{ID: "A", Name: "t", Arguments: "{}"},
			{ID: "B", Name: "t", Arguments: "{}"},
		}},
		{Role: "tool", ToolCallID: "A", Content: "answered"},
		{Role: "assistant", Content: "done"},
	})

	var keptA, keptB, sawResultB bool
	for _, m := range got {
		for _, tc := range m.ToolCalls {
			switch tc.ID {
			case "A":
				keptA = true
			case "B":
				keptB = true
			}
		}
		if m.Role == "tool" && m.ToolCallID == "B" {
			sawResultB = true
		}
	}
	if keptB || sawResultB {
		t.Error("the UNANSWERED call B survived inside a multi-call assistant message — " +
			"a lone tool_use is a provider 400")
	}
	if !keptA {
		t.Error("the ANSWERED call A was dropped along with its unanswered sibling — " +
			"the sweep removed a non-orphan")
	}
}
