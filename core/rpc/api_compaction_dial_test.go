package rpc

// api_compaction_dial_test.go — regression guard for the FR-041
// global-layer seed deriving from the user's ACTUAL compaction
// aggressiveness dial (settings.Settings.CompactionAggressiveness /
// EffectiveCompactionAggressiveness) instead of a hardcoded
// PresetForTier("balanced").
//
// Prior to this fix, newGraphManagerWithDeps wired
// compaction.ProductionDefaults() — always "balanced" — into the
// kernel's compaction resolver regardless of what the user had
// actually dialed, including "off". That was mostly latent (the
// automatic SitePreCall/SitePostTool sites are unconditionally
// disabled at every tier — see presets.go), but SiteManual and every
// PreCallThreshold numeric were already wrong for any user not on
// "balanced", and it was a live trap for whoever enables the
// automatic sites later.
//
// This is a BOOT-TIME seed: settingsImpl is read once, when
// newGraphManagerWithDeps constructs the resolver. A dial change made
// afterward (without a restart) is NOT reflected in the FR-041 global
// layer — see
// TestCompactionGlobalSeed_RuntimeDialChangeNotReflectedAfterConstruction
// below, which documents that limitation explicitly rather than
// silently shipping it. This is judged acceptable because (a) the
// automatic sites are disabled either way so the only thing the seed
// currently drives is SiteManual + PreCallThreshold numerics nothing
// automatic reads yet, (b) the pre-send path
// (ChatRunner.runPreSendCompaction) re-resolves the dial from the
// settings store on every send and remains the authoritative
// automatic compactor, and (c) an on-disk config/compaction.yaml
// (loaded by NewYAMLResolverWithDefaults) always wins over this seed
// regardless, so a user who has explicitly configured the FR-041
// layer is unaffected by dial drift either way.
import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// TestCompactionGlobalSeed_NilSettings_FallsBackToProductionDefaults
// locks the pre-existing behaviour for every caller that has no
// settings store available (nil-Core test-harness boot paths): the
// seed must still be ProductionDefaults() (== PresetForTier("balanced")),
// not some zero-value CompactionConfig.
func TestCompactionGlobalSeed_NilSettings_FallsBackToProductionDefaults(t *testing.T) {
	got := compactionGlobalSeed(nil)
	want := compaction.ProductionDefaults()
	if got.ForSite(compaction.SitePreCall) != want.ForSite(compaction.SitePreCall) {
		t.Fatalf("compactionGlobalSeed(nil) pre_call diverged from ProductionDefaults()")
	}
}

// TestCompactionGlobalSeed_DefaultTier_NoBehaviourChange covers a user
// who has never touched the dial (fresh install, empty persisted
// CompactionAggressiveness). EffectiveCompactionAggressiveness resolves
// empty to "balanced", so the seed must be byte-for-byte the same as
// ProductionDefaults() / PresetForTier("balanced") — no behaviour
// change for the default-tier user.
func TestCompactionGlobalSeed_DefaultTier_NoBehaviourChange(t *testing.T) {
	api := settings.NewAPI(nil) // in-memory store, zero value = defaults
	got := compactionGlobalSeed(api)
	want := compaction.PresetForTier("balanced")
	if got.ForSite(compaction.SitePreCall) != want.ForSite(compaction.SitePreCall) {
		t.Fatalf("default-tier seed pre_call diverged from PresetForTier(\"balanced\")")
	}
	if got.ForSite(compaction.SiteManual) != want.ForSite(compaction.SiteManual) {
		t.Fatalf("default-tier seed manual site diverged from PresetForTier(\"balanced\")")
	}
}

// TestCompactionGlobalSeed_DerivesFromEveryDialTier is the core
// regression: for every persisted CompactionAggressiveness value, the
// FR-041 global seed must match PresetForTier(tier) — NOT always
// "balanced". This is the defect this WP fixes.
func TestCompactionGlobalSeed_DerivesFromEveryDialTier(t *testing.T) {
	for _, tier := range []string{"off", "conservative", "aggressive", "maximal"} {
		t.Run(tier, func(t *testing.T) {
			api := settings.NewAPI(nil)
			s, err := api.Store().LoadAll()
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			s.CompactionAggressiveness = tier
			if err := api.Store().SaveAll(s); err != nil {
				t.Fatalf("SaveAll: %v", err)
			}

			got := compactionGlobalSeed(api)
			want := compaction.PresetForTier(tier)

			gotPre := got.ForSite(compaction.SitePreCall)
			wantPre := want.ForSite(compaction.SitePreCall)
			if gotPre.PreCallThreshold != wantPre.PreCallThreshold {
				t.Errorf("tier %q: PreCallThreshold = %v, want %v (PresetForTier(%q), not balanced's 0.80)",
					tier, gotPre.PreCallThreshold, wantPre.PreCallThreshold, tier)
			}
			// Site posture per tier. Since
			// agentgraph-total-convergence-01PMGX01 WP08 the automatic
			// pre_call site is live at every tier except "off" — the
			// second compaction system that made it unsafe is gone, and
			// DropOldestStrategy trims in tool_use/tool_result units so
			// it can no longer strand a tool result. post_tool stays
			// off because ToolResultCap already bounds tool results
			// unconditionally at dispatch. See presets.go.
			if gotPre.Enabled != wantPre.Enabled {
				t.Errorf("tier %q: SitePreCall.Enabled = %v, want %v", tier, gotPre.Enabled, wantPre.Enabled)
			}
			if want := tier != "off"; gotPre.Enabled != want {
				t.Errorf("tier %q: SitePreCall.Enabled = %v, want %v (live everywhere but the off tier)",
					tier, gotPre.Enabled, want)
			}
			gotPost := got.ForSite(compaction.SitePostTool)
			if gotPost.Enabled {
				t.Errorf("tier %q: SitePostTool.Enabled = true, want false (ToolResultCap owns tool-result trimming)", tier)
			}
			if !got.ForSite(compaction.SiteManual).Enabled {
				t.Errorf("tier %q: SiteManual.Enabled = false, want true — that site is the dial itself", tier)
			}
		})
	}
}

// TestCompactionGlobalSeed_Off_MeansOffEndToEnd asserts the "off"
// tier's specific numerics: PreCallThreshold is 0 (mirrors
// core/compactionpolicy.Tier's ModeNone — no threshold-based trigger), and
// (as with every tier) the automatic sites are disabled, so nothing in
// the FR-041 layer would automatically compact even if a future WP
// enabled the sites without also checking the tier. Combined with the
// pre-send path's own AggressivenessOff short-circuit (chat_runner.go),
// "off" means off end-to-end today.
func TestCompactionGlobalSeed_Off_MeansOffEndToEnd(t *testing.T) {
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = "off"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	cfg := compactionGlobalSeed(api)
	pre := cfg.ForSite(compaction.SitePreCall)
	if pre.Enabled {
		t.Fatalf("off tier: SitePreCall.Enabled = true, want false")
	}
	if pre.PreCallThreshold != 0 {
		t.Fatalf("off tier: PreCallThreshold = %v, want 0", pre.PreCallThreshold)
	}
	post := cfg.ForSite(compaction.SitePostTool)
	if post.Enabled {
		t.Fatalf("off tier: SitePostTool.Enabled = true, want false")
	}
}

// TestNewGraphManagerWithDeps_ThreadsSettingsIntoPipelineResolver
// exercises the actual production wiring seam end-to-end: construct
// via newGraphManagerWithDeps with a settings API dialed to
// "aggressive", and assert the returned Pipeline's Resolver reflects
// the aggressive tier's PreCallThreshold — proving the settings value
// actually reaches the live kernel-side resolver, not just the
// isolated helper function.
func TestNewGraphManagerWithDeps_ThreadsSettingsIntoPipelineResolver(t *testing.T) {
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = "aggressive"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	_, pipeline := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, api, nil)
	if pipeline == nil {
		t.Fatal("newGraphManagerWithDeps returned nil pipeline")
	}
	resolver := pipeline.Resolver()
	if resolver == nil {
		t.Fatal("pipeline.Resolver() is nil")
	}
	cfg := resolver.Resolve(compaction.ScopeKey{})
	pre := cfg.ForSite(compaction.SitePreCall)
	want := compaction.PresetForTier("aggressive").ForSite(compaction.SitePreCall)
	if pre.PreCallThreshold != want.PreCallThreshold {
		t.Fatalf("resolved PreCallThreshold = %v, want %v (aggressive tier) — the pipeline's resolver did not pick up the settings dial",
			pre.PreCallThreshold, want.PreCallThreshold)
	}
}

// TestCompactionGlobalSeed_TracksRuntimeDialChange replaces
// TestCompactionGlobalSeed_RuntimeDialChangeNotReflectedAfterConstruction,
// which documented — deliberately, and correctly for its time — that the
// FR-041 global layer froze whatever the dial said at construction.
//
// That limitation was safe to accept while every tier resolved to the
// same per-site posture: the automatic sites were disabled at all five
// tiers, so a stale global layer could only mis-set numerics nothing
// evaluated. agentgraph-total-convergence-01PMGX01 WP08 made the posture
// tier-dependent — SitePreCall is enabled at every tier except "off" —
// which converted the limitation into a live defect: a user who moved
// the dial to "off" mid-session kept getting automatic pre-call
// compaction until they restarted, while the `compact` node, which reads
// the dial on every turn, correctly stopped. One control, two answers.
//
// The old test's own doc named the fix ("pushing dial changes into the
// resolver"). liveDialResolver does it by reading the dial where it is
// consumed, so no write path can bypass it.
//
// The assertion deliberately checks Enabled, not just PreCallThreshold.
// The threshold is what the old test looked at, and on the "off" tier
// both a live and a stale resolver could agree on a numeric while
// disagreeing about whether the site fires at all — which is the half
// that spends the user's tokens.
func TestCompactionGlobalSeed_TracksRuntimeDialChange(t *testing.T) {
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = "balanced"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	_, pipeline := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, api, nil)
	resolver := pipeline.Resolver()

	// Boot state: balanced, so the automatic pre-call site is live.
	bootPre := resolver.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if !bootPre.Enabled {
		t.Fatalf("balanced tier resolved SitePreCall.Enabled = false; the fixture no longer exercises a live site, so the off-tier assertion below would pass vacuously")
	}

	// The user moves the dial to "off" at runtime, after construction —
	// through the settings store, which is the one thing every write
	// path (Settings panel, fleet sync applier) has in common.
	s.CompactionAggressiveness = "off"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	pre := resolver.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if pre.Enabled {
		t.Fatalf("dial moved to \"off\" but the resolver still reports SitePreCall.Enabled = true — automatic compaction is ignoring the user's dial until restart")
	}
	offPre := compaction.PresetForTier("off").ForSite(compaction.SitePreCall)
	if pre.PreCallThreshold != offPre.PreCallThreshold {
		t.Fatalf("resolved PreCallThreshold = %v, want %v (off tier)", pre.PreCallThreshold, offPre.PreCallThreshold)
	}

	// And back again: the resolver must track the dial in both
	// directions, not merely latch the first change it sees.
	s.CompactionAggressiveness = "aggressive"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	back := resolver.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if !back.Enabled {
		t.Fatalf("dial moved back to \"aggressive\" but SitePreCall.Enabled is still false")
	}
	if want := compaction.PresetForTier("aggressive").ForSite(compaction.SitePreCall).PreCallThreshold; back.PreCallThreshold != want {
		t.Fatalf("resolved PreCallThreshold = %v, want %v (aggressive tier)", back.PreCallThreshold, want)
	}
}

