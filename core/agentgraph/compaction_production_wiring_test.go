package agentgraph_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// TestKernel_ProductionCompactionDefaults_NoOpOnLLMNode builds the
// Compactor exactly the way production does — NewPipeline seeded with
// ProductionDefaults() via NewMemoryResolverWithDefaults,
// DropOldestStrategy + SummaryStrategy(nil) registered — and asserts a
// 200-message conversation reaches the LLM byte-for-byte unchanged.
//
// WHAT THIS PROVES NOW. Since
// agentgraph-total-convergence-01PMGX01 WP08 the pre_call site is
// ENABLED at the balanced tier, so this is no longer a "the site is
// switched off" test. It passes because the Env supplies no
// ContextWindowSource, which makes resolveContextWindow return 0, which
// makes the pipeline skip with reason "context window unknown". That is
// the deliberate design: compacting toward a guessed window is worse
// than not compacting, so an unknown model is a skip, not a trim.
//
// It also remains the TargetTokens-separation proof, which is why the
// node's MaxTokens is deliberately small (256). That is the exact shape
// of the conflation bug compaction-convergence-01PMDL05 WP02 named and
// this mission's WP08 closed: a compaction target derived from the
// node's *output* token cap would read this history as wildly over
// budget on every single call. The target derives from the context
// window, which is a different quantity, and there is a dedicated pin
// for that in compaction_target_test.go.
//
// (An earlier revision of this comment cited core/eval's compaction
// matrix and its StrategyOverrides as the evidence this test stood in
// for. WP09 deleted that machinery outright — a replay re-renders
// cached responses and never re-executes a model, so a strategy dial
// could not have moved a score. This test was always the real
// evidence.)
func TestKernel_ProductionCompactionDefaults_NoOpOnLLMNode(t *testing.T) {
	pipeline := compaction.NewPipeline(
		compaction.WithResolver(
			compaction.NewMemoryResolverWithDefaults(compaction.ProductionDefaults()),
		),
	)
	pipeline.RegisterStrategy(compaction.NewDropOldestStrategy())
	pipeline.RegisterStrategy(compaction.NewSummaryStrategy(nil))

	history := make([]agentgraph.Message, 0, 200)
	for i := 0; i < 200; i++ {
		history = append(history, agentgraph.Message{
			Role:    "user",
			Content: "filler conversation turn number " + strconv.Itoa(i) + " padding padding padding padding",
		})
	}

	graph := &agentgraph.Graph{
		ID:          "g-prod-defaults",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 256,
				},
			},
		},
	}
	llm := &fakeLLM{resp: "ok"}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(pipeline))
	env := &agentgraph.Env{
		RunID:     "r-prod-defaults",
		SessionID: "s-prod-defaults",
		Graph:     graph,
		LLM:       llm,
		History: agentgraph.HistoryReaderFunc(func(_ context.Context, _ string, _ int) ([]agentgraph.Message, error) {
			return history, nil
		}),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := len(llm.seen.Messages), len(history); got != want {
		t.Fatalf("production compaction defaults changed history length: got %d messages sent to the LLM, want %d (unchanged) — kernel-side compaction must be a no-op at defaults", got, want)
	}
	for i := range history {
		if llm.seen.Messages[i].Content != history[i].Content {
			t.Fatalf("production compaction defaults mutated message %d content: got %q want %q", i, llm.seen.Messages[i].Content, history[i].Content)
		}
	}
}

// TestPresetForTier_SitePostureByTier pins the per-site posture the
// dial resolves to, for every tier.
//
// It replaces TestPresetForTier_PreCallPostToolDisabledEverySite, which
// asserted that BOTH automatic sites were off at every tier including
// "balanced". That was true and correct while the chat surface ran its
// own pre-kernel compaction pass the kernel could not see: enabling a
// kernel site as well would have compacted the same conversation twice
// on the same turn. It was also the exact "wired but default-off" shape
// agentgraph-total-convergence-01PMGX01 exists to eliminate — a fully
// configured surface no configuration could reach.
//
// WP08 removed the reason rather than the surface, so the posture this
// test pins is now differentiated per site and per tier:
//
//   - SiteManual is on at every tier, including "off". It is the dial
//     itself, reached from the `compact` node; the "off" tier's meaning
//     ("do not compact, but say so honestly when the session is full")
//     is resolved inside the strategy, not by refusing to run it.
//   - SitePreCall is on at every tier except "off". Both of the
//     preconditions compaction-convergence-01PMDL05 recorded are met:
//     DropOldestStrategy trims in tool_use/tool_result atomic units, and
//     the two compaction systems are now one.
//   - SitePostTool is off everywhere, and this one is not a deferral:
//     ToolResultCap bounds tool results unconditionally at dispatch, so
//     there is no work left for the site to do.
//
// If a future change flips any of these, it should have to edit this
// test and say why.
func TestPresetForTier_SitePostureByTier(t *testing.T) {
	for _, tier := range []string{"off", "conservative", "balanced", "aggressive", "maximal", "unknown-tier"} {
		cfg := compaction.PresetForTier(tier)

		// "unknown-tier" falls back to balanced, so it is live like the
		// named non-off tiers.
		wantPre := tier != "off"
		if got := cfg.ForSite(compaction.SitePreCall).Enabled; got != wantPre {
			t.Errorf("tier %q: SitePreCall.Enabled = %v, want %v", tier, got, wantPre)
		}
		if cfg.ForSite(compaction.SitePostTool).Enabled {
			t.Errorf("tier %q: SitePostTool.Enabled = true, want false — ToolResultCap already bounds tool results at dispatch", tier)
		}
		if !cfg.ForSite(compaction.SiteManual).Enabled {
			t.Errorf("tier %q: SiteManual.Enabled = false, want true — that site is the dial", tier)
		}
		if got := cfg.ForSite(compaction.SiteManual).Strategy; got != compaction.StrategySessionRewrite {
			t.Errorf("tier %q: SiteManual.Strategy = %q, want %q — the dial rewrites persisted session history",
				tier, got, compaction.StrategySessionRewrite)
		}
	}
}

// TestProductionDefaults_IsBalancedTier locks ProductionDefaults to
// the "balanced" preset — today's shipped default dial tier
// (core/compactionpolicy.AggressivenessBalanced). If the dial's own default
// ever changes, this test intentionally does NOT auto-follow it: a
// production-default change should be a deliberate, reviewed edit to
// both this preset and the assertion below, not a silent drift.
func TestProductionDefaults_IsBalancedTier(t *testing.T) {
	got := compaction.ProductionDefaults()
	want := compaction.PresetForTier("balanced")
	if got.ForSite(compaction.SitePreCall) != want.ForSite(compaction.SitePreCall) {
		t.Fatalf("ProductionDefaults pre_call diverged from PresetForTier(\"balanced\")")
	}
	preCall := got.ForSite(compaction.SitePreCall)
	if preCall.PreCallThreshold != 0.80 {
		t.Fatalf("ProductionDefaults pre_call threshold = %v, want 0.80 (balanced tier's TriggerPct)", preCall.PreCallThreshold)
	}
}
