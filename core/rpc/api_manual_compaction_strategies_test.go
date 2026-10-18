package rpc

// api_manual_compaction_strategies_test.go — chat-turn-integrity-01PMZ606
// WP07 (spec.md §1.5, §5.6). AC-009.
import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// TestRegisterManualCompactionStrategies_ClosesEveryPanelOfferedGap is
// AC-009: for every strategy the compaction Settings panel offers
// (summary, drop_oldest, semantic_cluster — custom_subgraph is no
// longer offered, see the frontend panel test), and for the SiteManual
// DEFAULT with no explicit override (which resolves to session_rewrite
// per compaction.PresetForTier and is what "Compact now" reaches with
// nothing selected), Pipeline.Run on the BASE pipeline must not return
// ErrUnknownStrategy.
//
// Before this WP, drop_oldest and summary were the only two
// registrations (core/rpc/api.go), so semantic_cluster and the
// session_rewrite default both failed unconditionally.
//
// Mutation: comment out registerManualCompactionStrategies's call to
// RegisterStrategy(chat.NewSessionRewriteStrategy(...)) — the "" (no
// override) case must fail with ErrUnknownStrategy.
func TestRegisterManualCompactionStrategies_ClosesEveryPanelOfferedGap(t *testing.T) {
	pipeline := compaction.NewPipeline(
		compaction.WithResolver(compaction.NewMemoryResolverWithDefaults(compaction.PresetForTier("balanced"))),
	)
	// The two registrations production always had.
	pipeline.RegisterStrategy(compaction.NewDropOldestStrategy())
	pipeline.RegisterStrategy(compaction.NewSummaryStrategy(nil))
	// The WP07 fix under test. nil deps/history is fine here: AC-009
	// only asserts dispatch reaches a registered strategy rather than
	// ErrUnknownStrategy — sessionRewriteStrategy.Compact degrades to a
	// no-op passthrough on nil deps, which is a DIFFERENT, already-
	// covered code path (see session_compaction.go's Compact tests).
	registerManualCompactionStrategies(pipeline, nil, nil, nil)

	for _, tc := range []struct {
		name     string
		override compaction.Strategy
	}{
		{"summary (panel offered)", compaction.StrategySummary},
		{"drop_oldest (panel offered)", compaction.StrategyDropOldest},
		{"semantic_cluster (panel offered)", compaction.StrategySemanticCluster},
		{"session_rewrite (SiteManual's tier-preset default, no override)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pipeline.Run(context.Background(), compaction.CompactRequest{
				RunID:    "manual:test-session",
				Scope:    compaction.ScopeKey{SessionID: "test-session"},
				Site:     compaction.SiteManual,
				Override: tc.override,
			})
			if errors.Is(err, compaction.ErrUnknownStrategy) {
				t.Fatalf("Pipeline.Run(override=%q) = %v, want no ErrUnknownStrategy — "+
					"the base pipeline is missing a registration the manual-trigger RPC surface needs",
					tc.override, err)
			}
		})
	}
}
