package chat

import (
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// wiring-integrity-01PMAG04 WP08.
//
// ModelAttrs.ReasoningBudgetTokens -> LLMRequest.ReasoningBudgetTokens ->
// GenerationRequest.Reasoning has been plumbed and tested since
// model-request-path-live-01PMDL01 WP06b. No shipped graph ever set the
// attr, so the whole path was inert — a fully-built feature reachable by
// nothing. applyReasoningBudgetDial is the missing last hop.
//
// The dial is deliberately NOT defaulted on: enabling reasoning changes
// the cost and latency of every turn, which is a product decision, not a
// side effect of wiring. These tests pin both directions.

func graphWithModelNode(t *testing.T, existingBudget int) coreag.Graph {
	t.Helper()
	return coreag.Graph{
		SpecVersion: "1",
		ID:          "g",
		Entrypoints: []string{"m"},
		Nodes: []coreag.Node{
			{
				ID:   "m",
				Kind: coreag.NodeKindModel,
				Attrs: coreag.ModelAttrs{
					Model:                 "default",
					ReasoningBudgetTokens: existingBudget,
				},
			},
			{
				ID:    "l",
				Kind:  coreag.NodeKindLoop,
				Attrs: coreag.LoopAttrs{MaxIterations: 5, Body: []string{"m"}},
			},
		},
	}
}

func modelBudget(t *testing.T, g coreag.Graph) int {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Kind != coreag.NodeKindModel {
			continue
		}
		a, ok := n.Attrs.(coreag.ModelAttrs)
		if !ok {
			t.Fatalf("model node attrs are %T, want ModelAttrs", n.Attrs)
		}
		return a.ReasoningBudgetTokens
	}
	t.Fatal("no model node in graph")
	return 0
}

func TestApplyReasoningBudgetDial_ThreadsBudgetOntoModelNodes(t *testing.T) {
	t.Parallel()
	g := graphWithModelNode(t, 0)
	applyReasoningBudgetDial(&g, 8192)
	if got := modelBudget(t, g); got != 8192 {
		t.Fatalf("ReasoningBudgetTokens = %d, want 8192 — the dial is not reaching the model node", got)
	}
}

// A zero/negative budget must leave the attr UNTOUCHED rather than
// writing 0. "No-op means untouched" is what lets a graph author set the
// attr explicitly without the dial clobbering it, and it is why a
// harness with the dial off produces byte-identical requests to
// pre-01PMAG04.
func TestApplyReasoningBudgetDial_ZeroLeavesAuthoredAttrUntouched(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{0, -1} {
		g := graphWithModelNode(t, 4096)
		applyReasoningBudgetDial(&g, budget)
		if got := modelBudget(t, g); got != 4096 {
			t.Errorf("budget=%d: ReasoningBudgetTokens = %d, want the authored 4096 preserved", budget, got)
		}
	}
}

func TestApplyReasoningBudgetDial_NilGraphIsSafe(t *testing.T) {
	t.Parallel()
	applyReasoningBudgetDial(nil, 8192) // must not panic
}

// The default path — no resolver configured — must yield reasoning off,
// matching every pre-01PMAG04 run.
func TestApplyReasoningBudgetDial_DefaultIsOff(t *testing.T) {
	t.Parallel()
	g := graphWithModelNode(t, 0)
	// Mirrors NewChatRunner's nil-resolver fallback.
	resolver := ReasoningBudgetResolver(nil)
	if resolver == nil {
		resolver = func() int { return 0 }
	}
	applyReasoningBudgetDial(&g, resolver())
	if got := modelBudget(t, g); got != 0 {
		t.Fatalf("default ReasoningBudgetTokens = %d, want 0 (reasoning off) — wiring the dial must not enable it", got)
	}
}
