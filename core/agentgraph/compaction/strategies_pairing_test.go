package compaction

// strategies_pairing_test.go — model-moves-transcript-01PMCH01 WP06, the
// drop_oldest half of "compaction never orphans a pair".
//
// The input shapes here are what composeModelHistory (core/rpc) actually
// hands the kernel once MoveFidelityHistory is on: a tool_call move
// becomes an assistant message carrying ToolCalls, a tool_result move
// becomes a role:"tool" message carrying ToolCallID. Parallel
// tool_dispatch resolves out of order, so the pairs interleave.
//
// TWO INVARIANTS, AND THE SECOND ONE IS THE INTERESTING ONE.
//
//  1. No half-pair in the output. This is the provider 400.
//  2. The output is a contiguous SUFFIX of the input, modulo rows that
//     were already orphans on the way in. drop_oldest is defined as
//     "drop the oldest units until we fit"; a row disappearing from the
//     middle of the kept region means the grouping deleted it, not the
//     trim.
//
// Invariant 2 exists because invariant 1 alone is satisfied by deleting
// both halves of a pair, which is exactly what the pre-WP06 grouping did
// to every out-of-order pair — silently, at every cut point, including
// cut points that were supposed to drop nothing near it.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

func pairUse(id, note string) agentgraph.Message {
	return agentgraph.Message{
		Role: "assistant", Content: "",
		ToolCalls: []agentgraph.ToolCallRequest{{ID: id, Name: "bash-" + note, Arguments: `{"cmd":"ls"}`}},
	}
}

func pairRes(id string) agentgraph.Message {
	return agentgraph.Message{
		Role: "tool", ToolCallID: id, Name: "bash",
		Content: strings.Repeat("output-"+id+" ", 6),
	}
}

func pairText(role, body string) agentgraph.Message {
	return agentgraph.Message{Role: role, Content: body}
}

// pairResNamed is pairRes with a distinguishing tool name, for fixtures
// that reuse one tool id across turns. Without it the two result rows
// for that id are byte-identical, identity() below cannot tell them
// apart, and the suffix assertion reports a false middle-deletion the
// moment the trim is able to cut between the two turns.
func pairResNamed(id, note string) agentgraph.Message {
	m := pairRes(id)
	m.Name = "bash-" + note
	return m
}

// identity gives every fixture row a stable key. Tool-call rows have
// empty Content by construction (their display summary must never reach
// the model), so the key has to include the call ids — and the tool
// NAME, which is the only thing separating two rows of a reused id.
func identity(m agentgraph.Message) string {
	var b strings.Builder
	b.WriteString(m.Role)
	b.WriteString("|")
	b.WriteString(m.Content)
	b.WriteString("|")
	b.WriteString(m.Name)
	b.WriteString("|")
	b.WriteString(m.ToolCallID)
	for _, tc := range m.ToolCalls {
		b.WriteString("|call:")
		b.WriteString(tc.ID)
		b.WriteString("/")
		b.WriteString(tc.Name)
	}
	return b.String()
}

// pairFixtures are the adversarial move-bearing histories.
func pairFixtures() map[string][]agentgraph.Message {
	return map[string][]agentgraph.Message{
		"sequential pairs": {
			pairText("user", "first question here"),
			pairUse("a1", "1"), pairRes("a1"),
			pairText("assistant", "the first answer"),
			pairText("user", "second question here"),
			pairUse("a2", "2"), pairRes("a2"),
			pairText("assistant", "the second answer"),
		},
		"interleaved pairs from parallel dispatch": {
			pairText("user", "first question here"),
			pairUse("b1", "1"), pairUse("b2", "2"),
			pairRes("b2"), pairRes("b1"),
			pairText("assistant", "the first answer"),
			pairText("user", "second question here"),
			pairUse("b3", "3"), pairUse("b4", "4"),
			pairRes("b4"), pairRes("b3"),
			pairText("assistant", "the second answer"),
		},
		"a pair split by an assistant segment": {
			pairText("user", "first question here"),
			pairUse("c1", "1"),
			pairText("assistant", "thinking out loud between the call and its result"),
			pairRes("c1"),
			pairText("assistant", "the answer"),
		},
		"an id reused across turns": {
			pairText("user", "first question here"),
			pairUse("d1", "1"), pairResNamed("d1", "1"),
			pairText("assistant", "answer one"),
			pairText("user", "second question here"),
			pairUse("d1", "2"), pairResNamed("d1", "2"),
			pairText("assistant", "answer two"),
		},
		"one assistant message carrying two calls": {
			pairText("user", "first question here"),
			{Role: "assistant", ToolCalls: []agentgraph.ToolCallRequest{
				{ID: "e1", Name: "bash", Arguments: "{}"},
				{ID: "e2", Name: "bash", Arguments: "{}"},
			}},
			pairRes("e2"), pairRes("e1"),
			pairText("assistant", "the answer"),
		},
		"a long many-move session": func() []agentgraph.Message {
			var out []agentgraph.Message
			for turn := 0; turn < 6; turn++ {
				out = append(out, pairText("user", fmt.Sprintf("question number %d", turn)))
				for k := 0; k < 3; k++ {
					out = append(out, pairText("assistant",
						fmt.Sprintf("segment %d of turn %d", k, turn)))
					id := fmt.Sprintf("t%d-%d", turn, k)
					out = append(out, pairUse(id, "x"))
				}
				// Results arrive in reverse order — the parallel case.
				for k := 2; k >= 0; k-- {
					out = append(out, pairRes(fmt.Sprintf("t%d-%d", turn, k)))
				}
				out = append(out, pairText("assistant", fmt.Sprintf("answer %d", turn)))
			}
			return out
		}(),
	}
}

// inputOrphans reports tool ids that are already half-pairs on the way
// in. Rows carrying one of these are allowed to vanish.
func inputOrphans(msgs []agentgraph.Message) map[string]bool {
	calls, results := map[string]bool{}, map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			results[m.ToolCallID] = true
		}
	}
	out := map[string]bool{}
	for id := range calls {
		if !results[id] {
			out[id] = true
		}
	}
	for id := range results {
		if !calls[id] {
			out[id] = true
		}
	}
	return out
}

// TestDropOldest_NeverOrphansAndNeverDeletesFromTheMiddle sweeps every
// achievable cut point by walking TargetTokens across the whole range.
//
// MUTATION EVIDENCE: restore dropOldestUnits' pre-WP06 lookahead
// grouping (absorb only immediately-following tool rows answering THIS
// message's calls; drop any unclaimed tool row) and this fails on
// "interleaved pairs from parallel dispatch", "one assistant message
// carrying two calls" and "a long many-move session" with the
// middle-deletion message — every out-of-order pair is destroyed whole,
// which invariant 1 alone cannot see.
func TestDropOldest_NeverOrphansAndNeverDeletesFromTheMiddle(t *testing.T) {
	t.Parallel()
	s := NewDropOldestStrategy()

	for name, msgs := range pairFixtures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			total := approxTokens(bytesOf(msgs))
			wasOrphan := inputOrphans(msgs)
			trimmedAtLeastOnce := false

			for target := 1; target <= total+1; target++ {
				for _, keepN := range []int{0, 2, 5} {
					label := fmt.Sprintf("target=%d keepRecentN=%d", target, keepN)
					got, err := s.Compact(context.Background(),
						ContextSlice{Messages: msgs, TargetTokens: target},
						CompactOpts{DropOldestKeepRecentN: keepN})
					if err != nil {
						t.Fatalf("[%s] Compact: %v", label, err)
					}
					out := got.Messages
					if len(out) < len(msgs) {
						trimmedAtLeastOnce = true
					}

					// Invariant 1 — no half-pair reaches the provider.
					calls, results := map[string]bool{}, map[string]bool{}
					for _, m := range out {
						for _, tc := range m.ToolCalls {
							calls[tc.ID] = true
						}
						if m.Role == "tool" && m.ToolCallID != "" {
							results[m.ToolCallID] = true
						}
					}
					for id := range calls {
						if !results[id] {
							t.Errorf("[%s] tool_use %q survived with no tool_result", label, id)
						}
					}
					for id := range results {
						if !calls[id] {
							t.Errorf("[%s] tool_result %q survived with no tool_use", label, id)
						}
					}

					// Invariant 2 — the survivors are a contiguous suffix.
					kept := map[string]int{}
					for _, m := range out {
						kept[identity(m)]++
					}
					firstKept := -1
					for i, m := range msgs {
						if kept[identity(m)] > 0 {
							firstKept = i
							break
						}
					}
					if firstKept < 0 {
						continue // everything trimmed; nothing to check
					}
					seen := map[string]int{}
					for i := firstKept; i < len(msgs); i++ {
						key := identity(msgs[i])
						seen[key]++
						if seen[key] <= kept[key] {
							continue
						}
						if orphanRow(msgs[i], wasOrphan) {
							continue
						}
						t.Errorf("[%s] input row %d (%s) was deleted from the MIDDLE of "+
							"the kept region. drop_oldest may only drop a prefix; a row "+
							"vanishing here means the trim-unit grouping destroyed a pair "+
							"it could not recognise.", label, i, key)
					}
				}
			}
			if !trimmedAtLeastOnce {
				t.Fatal("no target trimmed anything — the sweep is vacuous")
			}
		})
	}
}

// orphanRow reports whether m only carries ids that were already
// orphaned in the input, and may therefore legitimately be pruned.
func orphanRow(m agentgraph.Message, wasOrphan map[string]bool) bool {
	if m.Role == "tool" {
		return wasOrphan[m.ToolCallID]
	}
	if len(m.ToolCalls) == 0 {
		return false
	}
	for _, tc := range m.ToolCalls {
		if !wasOrphan[tc.ID] {
			return false
		}
	}
	return true
}

// TestDropOldestUnits_BindsInterleavedPairsIntoOneUnit is the unit-level
// statement of the same fact, kept separate because it names the shape
// rather than sweeping for it: an out-of-order pair must land in ONE
// trim-atomic unit, so no cut point can take half of it.
//
// Mutation: revert dropOldestUnits to the lookahead grouping and the
// unit count goes to 2 — [use A] alone and [use B, res B] — with [res A]
// dropped by the grouping entirely, so the units cover 3 of 4 rows.
func TestDropOldestUnits_BindsInterleavedPairsIntoOneUnit(t *testing.T) {
	t.Parallel()
	msgs := []agentgraph.Message{
		pairUse("A", "1"), pairUse("B", "2"), pairRes("B"), pairRes("A"),
	}
	units := dropOldestUnits(msgs)
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 — an interleaved pair split across units can be "+
			"halved by a cut point between them", len(units))
	}
	n := 0
	for _, u := range units {
		n += len(u)
	}
	if n != len(msgs) {
		t.Errorf("units cover %d of %d messages — the grouping dropped rows outright, "+
			"which is how pair A disappeared before the trim loop ever ran", n, len(msgs))
	}
}
