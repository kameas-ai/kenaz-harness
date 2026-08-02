package rpc

// api_compaction_dial_test.go — regression guard for the FR-041
// global-layer seed deriving from the user's ACTUAL compaction
// aggressiveness dial (settings.Settings.CompactionAggressiveness /
// EffectiveCompactionAggressiveness) instead of a hardcoded
// PresetForTier("balanced").
//
// Prior to this fix, newGraphManagerWithDeps wired
// fr041compaction.ProductionDefaults() — always "balanced" — into the
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

	fr041compaction "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// TestCompactionGlobalSeed_NilSettings_FallsBackToProductionDefaults
// locks the pre-existing behaviour for every caller that has no
// settings store available (nil-Core test-harness boot paths): the
// seed must still be ProductionDefaults() (== PresetForTier("balanced")),
// not some zero-value CompactionConfig.
func TestCompactionGlobalSeed_NilSettings_FallsBackToProductionDefaults(t *testing.T) {
	got := compactionGlobalSeed(nil)
	want := fr041compaction.ProductionDefaults()
	if got.ForSite(fr041compaction.SitePreCall) != want.ForSite(fr041compaction.SitePreCall) {
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
	want := fr041compaction.PresetForTier("balanced")
	if got.ForSite(fr041compaction.SitePreCall) != want.ForSite(fr041compaction.SitePreCall) {
		t.Fatalf("default-tier seed pre_call diverged from PresetForTier(\"balanced\")")
	}
	if got.ForSite(fr041compaction.SiteManual) != want.ForSite(fr041compaction.SiteManual) {
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
			want := fr041compaction.PresetForTier(tier)

			gotPre := got.ForSite(fr041compaction.SitePreCall)
			wantPre := want.ForSite(fr041compaction.SitePreCall)
			if gotPre.PreCallThreshold != wantPre.PreCallThreshold {
				t.Errorf("tier %q: PreCallThreshold = %v, want %v (PresetForTier(%q), not balanced's 0.80)",
					tier, gotPre.PreCallThreshold, wantPre.PreCallThreshold, tier)
			}
			// Automatic sites stay disabled at every tier — this WP does
			// NOT flip that switch (a parallel WP owns the tool-pairing
			// precondition for SitePreCall/SitePostTool).
			if gotPre.Enabled {
				t.Errorf("tier %q: SitePreCall.Enabled = true, want false (unchanged by this fix)", tier)
			}
			gotPost := got.ForSite(fr041compaction.SitePostTool)
			if gotPost.Enabled {
				t.Errorf("tier %q: SitePostTool.Enabled = true, want false (unchanged by this fix)", tier)
			}
		})
	}
}

// TestCompactionGlobalSeed_Off_MeansOffEndToEnd asserts the "off"
// tier's specific numerics: PreCallThreshold is 0 (mirrors
// core/compaction.Tier's ModeNone — no threshold-based trigger), and
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
	pre := cfg.ForSite(fr041compaction.SitePreCall)
	if pre.Enabled {
		t.Fatalf("off tier: SitePreCall.Enabled = true, want false")
	}
	if pre.PreCallThreshold != 0 {
		t.Fatalf("off tier: PreCallThreshold = %v, want 0", pre.PreCallThreshold)
	}
	post := cfg.ForSite(fr041compaction.SitePostTool)
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

	_, pipeline := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, api)
	if pipeline == nil {
		t.Fatal("newGraphManagerWithDeps returned nil pipeline")
	}
	resolver := pipeline.Resolver()
	if resolver == nil {
		t.Fatal("pipeline.Resolver() is nil")
	}
	cfg := resolver.Resolve(fr041compaction.ScopeKey{})
	pre := cfg.ForSite(fr041compaction.SitePreCall)
	want := fr041compaction.PresetForTier("aggressive").ForSite(fr041compaction.SitePreCall)
	if pre.PreCallThreshold != want.PreCallThreshold {
		t.Fatalf("resolved PreCallThreshold = %v, want %v (aggressive tier) — the pipeline's resolver did not pick up the settings dial",
			pre.PreCallThreshold, want.PreCallThreshold)
	}
}

// TestCompactionGlobalSeed_RuntimeDialChangeNotReflectedAfterConstruction
// documents, on purpose, the boot-time-seed limitation called out in
// newGraphManagerWithDeps's construction-site comment: once
// compactionGlobalSeed has been read into the resolver at
// construction time, a LATER change to the persisted
// CompactionAggressiveness value is not retroactively observed by
// that already-constructed resolver. Re-deriving live would require
// either re-resolving on every read (the resolver has no settings
// handle to do that with today) or pushing dial changes into the
// resolver from Settings.Save — both out of scope for this WP. This
// test exists so a future change to that boot-time behaviour is a
// deliberate, reviewed edit here, not a silent regression either way.
func TestCompactionGlobalSeed_RuntimeDialChangeNotReflectedAfterConstruction(t *testing.T) {
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = "balanced"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	_, pipeline := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, api)
	resolver := pipeline.Resolver()

	// Simulate the user flipping the dial at runtime, after construction.
	s.CompactionAggressiveness = "maximal"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	cfg := resolver.Resolve(fr041compaction.ScopeKey{})
	pre := cfg.ForSite(fr041compaction.SitePreCall)
	balancedPre := fr041compaction.PresetForTier("balanced").ForSite(fr041compaction.SitePreCall)
	if pre.PreCallThreshold != balancedPre.PreCallThreshold {
		t.Fatalf("boot-time-seed limitation changed: resolver reflected the post-construction dial change (got threshold %v); "+
			"if this now tracks live changes, update this test's name/doc and the construction-site comment in newGraphManagerWithDeps instead of just changing the assertion",
			pre.PreCallThreshold)
	}
}
