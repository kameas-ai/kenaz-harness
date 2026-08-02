package agentgraph_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	fr041compaction "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// TestKernel_ProductionCompactionDefaults_NoOpOnLLMNode proves the
// wiring compaction-convergence-01PMDL05 WP01 lands in
// core/rpc/api.go's newGraphManagerWithDeps is behind current
// production behaviour. It builds the Compactor exactly the way
// production does — fr041compaction.NewPipeline seeded with
// fr041compaction.ProductionDefaults() via
// fr041compaction.NewMemoryResolverWithDefaults, DropOldestStrategy +
// SummaryStrategy(nil) registered — and asserts a long conversation
// history reaches the LLM byte-for-byte unchanged when pre_call
// compaction fires on an LLMNode. SitePreCall.Enabled is false in
// every dial-tier preset (see presets.go), so the kernel-side seam is
// a documented no-op at defaults; the pre-send dial (core/compaction,
// invoked from chat_runner.go's runPreSendCompaction) remains the
// sole automatic compactor until a later WP retires it.
//
// This is the WP01 evidence for "default path is unchanged" in place
// of core/eval's compaction matrix: RunMatrix's StrategyOverrides are
// recorded into the run trace for offline diffing but are not wired
// into any execution path that would actually invoke a Compactor
// (core/eval/replay.go, "Apply strategy overrides" step, explicit
// TODO) — so that infra cannot exercise either compaction code path
// today and would not have been meaningful evidence here. This test
// exercises the real production Pipeline + real Kernel instead.
//
// The node's MaxTokens is deliberately small (256) — the exact shape
// of the pre-WP02 bug, where TargetTokens was conflated with the
// node's *output* token cap and would have driven DropOldestStrategy
// to treat this 200-message history as wildly over budget on every
// call. Passing with a small MaxTokens demonstrates WP02's fix (the
// compaction target no longer derives from a.MaxTokens) together with
// WP01's disabled-by-default site gate.
func TestKernel_ProductionCompactionDefaults_NoOpOnLLMNode(t *testing.T) {
	pipeline := fr041compaction.NewPipeline(
		fr041compaction.WithResolver(
			fr041compaction.NewMemoryResolverWithDefaults(fr041compaction.ProductionDefaults()),
		),
	)
	pipeline.RegisterStrategy(fr041compaction.NewDropOldestStrategy())
	pipeline.RegisterStrategy(fr041compaction.NewSummaryStrategy(nil))

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

// TestPresetForTier_PreCallPostToolDisabledEverySite is a companion
// unit test asserting the same guarantee at the config level, for
// every dial tier — not just "balanced" (ProductionDefaults). Wiring
// FR-041 in must never silently enable kernel-side automatic
// compaction for ANY tier the user can pick, including "off" and
// "maximal": the pre-send dial already encodes what those tiers mean
// today, and this kernel seam does not yet participate in that
// meaning.
func TestPresetForTier_PreCallPostToolDisabledEverySite(t *testing.T) {
	for _, tier := range []string{"off", "conservative", "balanced", "aggressive", "maximal", "unknown-tier"} {
		cfg := fr041compaction.PresetForTier(tier)
		if cfg.ForSite(fr041compaction.SitePreCall).Enabled {
			t.Errorf("tier %q: SitePreCall.Enabled = true, want false", tier)
		}
		if cfg.ForSite(fr041compaction.SitePostTool).Enabled {
			t.Errorf("tier %q: SitePostTool.Enabled = true, want false", tier)
		}
		if !cfg.ForSite(fr041compaction.SiteManual).Enabled {
			t.Errorf("tier %q: SiteManual.Enabled = false, want true (explicit trigger, independent of the dial)", tier)
		}
	}
}

// TestProductionDefaults_IsBalancedTier locks ProductionDefaults to
// the "balanced" preset — today's shipped default dial tier
// (core/compaction.AggressivenessBalanced). If the dial's own default
// ever changes, this test intentionally does NOT auto-follow it: a
// production-default change should be a deliberate, reviewed edit to
// both this preset and the assertion below, not a silent drift.
func TestProductionDefaults_IsBalancedTier(t *testing.T) {
	got := fr041compaction.ProductionDefaults()
	want := fr041compaction.PresetForTier("balanced")
	if got.ForSite(fr041compaction.SitePreCall) != want.ForSite(fr041compaction.SitePreCall) {
		t.Fatalf("ProductionDefaults pre_call diverged from PresetForTier(\"balanced\")")
	}
	preCall := got.ForSite(fr041compaction.SitePreCall)
	if preCall.PreCallThreshold != 0.80 {
		t.Fatalf("ProductionDefaults pre_call threshold = %v, want 0.80 (balanced tier's TriggerPct)", preCall.PreCallThreshold)
	}
}
