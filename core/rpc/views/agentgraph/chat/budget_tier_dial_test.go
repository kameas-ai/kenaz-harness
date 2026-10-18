package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// Owner directive 2026-09-09 ("agent reached the per-run budget cap",
// filed live; ruling "we should be using our built in autonomy dial").
//
// Before applyBudgetTierDial, MaxLLMCallsPerRun and MaxToolCallsPerRun
// were the flat chat_default_classic.yaml constants (5000/10000)
// regardless of autonomy tier -- only MaxTokensPerRun scaled, via
// applyTokenCeilingKnob. This file proves the missing half end-to-end
// against the REAL coreag.Kernel: a run that trips ErrBudgetExceeded at
// a low tier's call-volume ceiling completes cleanly at a higher tier's
// ceiling, with every other variable (the graph, the executor, the
// iteration count) held identical. Not a unit test on the pure
// function alone -- CLAUDE.md's dials-to-consumer rule: "a read that
// only copies the value into another struct is not consumption --
// follow it to a branch." checkBudget's branch is the one this test
// exercises.

// tierLoopExecutor stands in for a chat turn's model-dispatch loop: one
// Execute on the "review" node == one simulated LLM call
// (env.Counters.AddLLM bumps LLMCallsMade, the exact counter
// checkBudget's MaxLLMCallsPerRun guard reads). It requests a
// kernel backtrack to "draft" to fire again, UNTIL it has fired stopAt
// times, at which point it completes without requesting another
// backtrack -- so a run that survives past stopAt calls finishes
// cleanly, and a run that hits its budget ceiling before stopAt calls
// halts with ErrBudgetExceeded. The real coreag.Kernel does every bit
// of the dispatching, budget-checking and backtrack bookkeeping; only
// the LLM call itself is faked, same shape as
// TestKernel_BacktrackBudgetCapHaltsInfiniteRewind
// (core/agentgraph/kernel_test.go) reuses for its own cap.
type tierLoopExecutor struct {
	kind     coreag.NodeKind
	reviewer string
	target   string
	stopAt   int

	mu    sync.Mutex
	fires int
}

func (e *tierLoopExecutor) Kind() coreag.NodeKind { return e.kind }

func (e *tierLoopExecutor) Execute(_ context.Context, env *coreag.Env, node *coreag.Node, _ coreag.PortValues) (coreag.Result, error) {
	r := coreag.NewResult()
	r.Outputs["value"] = node.ID
	if node.ID != e.reviewer {
		return r, nil
	}
	if env.Counters != nil {
		env.Counters.AddLLM(0)
	}
	e.mu.Lock()
	e.fires++
	stop := e.fires >= e.stopAt
	e.mu.Unlock()
	if !stop {
		r.Backtrack = &coreag.BacktrackRequest{TargetNode: e.target, Reason: "keep going"}
	}
	return r, nil
}

func tierLoopGraph(kind coreag.NodeKind) *coreag.Graph {
	return &coreag.Graph{
		SpecVersion: coreag.SpecVersion, ID: "tier-loop-graph", Entrypoints: []string{"draft"},
		Nodes: []coreag.Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
		},
		Edges: []coreag.Edge{
			{From: coreag.EndpointRef{Node: "draft", Port: "value"}, To: coreag.EndpointRef{Node: "review", Port: "in"}},
		},
	}
}

// TestApplyBudgetTierDial_KernelTripsAtLowTierCompletesAtHighTier is
// the mission's required proof: run the real kernel at two different
// autonomy tiers and show the budget ceiling actually differs.
func TestApplyBudgetTierDial_KernelTripsAtLowTierCompletesAtHighTier(t *testing.T) {
	t.Parallel()

	// The simulated turn always makes exactly 200 LLM calls before
	// finishing on its own -- held constant across both runs below.
	const stopAt = 200

	// declared mirrors chat_default_classic.yaml's budget: block
	// (max_llm_calls_per_run: 5000): larger than every tier's ceiling,
	// so applyBudgetTierDial's clamp is what's under test, not the
	// declared value passing through untouched. MaxBacktracksPerRun is
	// set far above stopAt so the backtrack cap can never be what halts
	// the run first -- only the LLM-call cap is under test here.
	declared := coreag.Budget{
		MaxLLMCallsPerRun:   5000,
		MaxBacktracksPerRun: 100000,
	}

	runTier := func(t *testing.T, tier autonomy.Tier) (err error, calls int) {
		t.Helper()
		kind := coreag.NodeKind("fake_tier_budget_loop_" + tier.String())
		ex := &tierLoopExecutor{kind: kind, reviewer: "review", target: "draft", stopAt: stopAt}
		k := coreag.NewKernel(coreag.WithExecutor(ex))
		env := &coreag.Env{
			RunID:  "tier-run-" + tier.String(),
			Graph:  tierLoopGraph(kind),
			Budget: applyBudgetTierDial(declared, tier),
		}
		err = k.Run(context.Background(), env)
		_, llmCalls, _, _ := env.Counters.Snapshot()
		return err, llmCalls
	}

	// TierStrict's ceiling (150, presets.go's budgetCeilingTable) is
	// below stopAt (200): the run must halt with ErrBudgetExceeded
	// before the simulated turn ever gets to finish on its own.
	strictErr, strictCalls := runTier(t, autonomy.TierStrict)
	if !errors.Is(strictErr, coreag.ErrBudgetExceeded) {
		t.Fatalf("TierStrict: err = %v, want ErrBudgetExceeded (ceiling=%d < stopAt=%d)",
			strictErr, autonomy.BudgetCeilingForTier(autonomy.TierStrict).MaxLLMCallsPerRun, stopAt)
	}
	wantStrictCeiling := autonomy.BudgetCeilingForTier(autonomy.TierStrict).MaxLLMCallsPerRun
	if strictCalls <= wantStrictCeiling || strictCalls > wantStrictCeiling+1 {
		t.Errorf("TierStrict: LLMCallsMade = %d, want just past the tier ceiling %d", strictCalls, wantStrictCeiling)
	}

	// TierBold's ceiling (3000) is comfortably above stopAt: the SAME
	// simulated turn, the SAME 200-call target, must now complete
	// cleanly instead of budget-exceeding. This is the actual proof —
	// identical graph, identical executor, identical iteration target;
	// only the tier differs, and that alone is what flips the outcome.
	boldErr, boldCalls := runTier(t, autonomy.TierBold)
	if boldErr != nil {
		t.Fatalf("TierBold: err = %v, want nil (ceiling=%d > stopAt=%d, run should complete)",
			boldErr, autonomy.BudgetCeilingForTier(autonomy.TierBold).MaxLLMCallsPerRun, stopAt)
	}
	if boldCalls != stopAt {
		t.Errorf("TierBold: LLMCallsMade = %d, want exactly %d (the turn's own natural stop)", boldCalls, stopAt)
	}

	// The headline assertion: the ceiling that governed the SAME
	// declared graph budget actually differs by tier. If this ever
	// reads equal, applyBudgetTierDial has been severed from the
	// autonomy dial and every guard above is checking the wrong thing.
	strictCeiling := autonomy.BudgetCeilingForTier(autonomy.TierStrict).MaxLLMCallsPerRun
	boldCeiling := autonomy.BudgetCeilingForTier(autonomy.TierBold).MaxLLMCallsPerRun
	if strictCeiling >= boldCeiling {
		t.Fatalf("BudgetCeilingForTier: strict=%d bold=%d, want strict strictly below bold", strictCeiling, boldCeiling)
	}
}
