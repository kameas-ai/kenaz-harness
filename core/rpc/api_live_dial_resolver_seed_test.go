package rpc

// api_live_dial_resolver_seed_test.go — chat-turn-integrity-01PMZ606
// WP06 (spec.md §1.6, §5.5). liveDialResolver.syncTier's first call used
// to always rewrite the global compaction layer, on the theory that it
// made the resolver's state "a function of the dial". In practice that
// clobbered whatever compaction.yaml the boot sequence had just loaded
// from disk — including a global layer the user hand-tuned through the
// compaction Settings view — the very first time anything resolved
// after construction (which happens on every app launch, and often on
// the panel's own GetEffective before the user does anything at all).
//
// These tests drive a real disk-backed compaction.YAMLResolver, not
// compaction.NewMemoryResolver — CLAUDE.md blind spot #2: the defect
// this WP fixes IS the flush-and-reload round trip, so a resolver that
// never touches disk cannot see it.
import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// dialedSettings returns a settings.API whose persisted
// CompactionAggressiveness is tier.
func dialedSettings(t *testing.T, tier string) *settings.API {
	t.Helper()
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = tier
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	return api
}

// bootLiveDialResolver constructs the resolver stack the way production
// does at chassis boot: a disk-backed YAMLResolver seeded from the
// dial, wrapped by liveDialResolver. Reconstructing this (fresh call,
// same dataDir) is what a restart looks like.
func bootLiveDialResolver(t *testing.T, dataDir string, api *settings.API) compaction.Resolver {
	t.Helper()
	seed := compactionGlobalSeed(api)
	disk, err := compaction.NewYAMLResolverWithDefaults(dataDir, seed)
	if err != nil {
		t.Fatalf("NewYAMLResolverWithDefaults: %v", err)
	}
	return newLiveDialResolver(disk, api)
}

// TestLiveDialResolver_SeedDoesNotClobberPersistedGlobalLayer is AC-007:
// write a non-default global layer through the Settings path, tear
// down, reconstruct as boot does, Resolve, and assert the layer is
// unchanged.
//
// Mutation: remove the `last: tier()` seed in newLiveDialResolver
// (leave `last` at its zero value). Must fail — the reconstructed
// resolver's first Resolve() would overwrite the 0.42 threshold with
// PresetForTier("aggressive")'s default (0.60, see preCallThresholdForTier).
func TestLiveDialResolver_SeedDoesNotClobberPersistedGlobalLayer(t *testing.T) {
	dir := t.TempDir()
	api := dialedSettings(t, "aggressive")

	// Boot #1: construct the real production stack. Warm it up with one
	// Resolve first — the realistic sequence (the compaction Settings
	// panel's own GetEffective fires before the user edits anything) —
	// so this test's fixture setup does not itself depend on the fix
	// under test: it isolates the defect to the *reconstruction* step
	// below, matching AC-007's "tear down, reconstruct" wording exactly.
	first := bootLiveDialResolver(t, dir, api)
	_ = first.Resolve(compaction.ScopeKey{})

	// Now write a hand-tuned global layer the way the compaction
	// Settings view does — through the SAME resolver, which is what
	// makes this defect class impossible to catch by only inspecting
	// the write path in isolation (CLAUDE.md § "Registration-vs-
	// consumption diffs").
	custom := compaction.PresetForTier("aggressive")
	preCfg := custom.ForSite(compaction.SitePreCall)
	const customThreshold = 0.42
	preCfg.PreCallThreshold = customThreshold
	custom.Sites[compaction.SitePreCall] = preCfg
	first.Set(compaction.LayerGlobal, "", custom)

	sanity := first.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if sanity.PreCallThreshold != customThreshold {
		t.Fatalf("fixture did not take: PreCallThreshold = %v immediately after Set, want %v", sanity.PreCallThreshold, customThreshold)
	}

	// Reconstruct exactly as a cold boot does: a fresh YAMLResolver over
	// the same dataDir (so it reloads what Set just flushed), wrapped by
	// a fresh liveDialResolver, with the dial unchanged.
	second := bootLiveDialResolver(t, dir, api)
	got := second.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if got.PreCallThreshold != customThreshold {
		t.Fatalf("global layer clobbered on restart: PreCallThreshold = %v, want %v (the persisted custom value) — "+
			"the reconstructed resolver's first Resolve() call overwrote what compaction.yaml held",
			got.PreCallThreshold, customThreshold)
	}
}

// TestLiveDialResolver_TracksMidProcessTierChange is AC-008: without
// this, AC-007 would be satisfiable by deleting syncTier entirely (never
// write the global layer to the dial at all). The dial must still be
// live: a tier change after construction is reflected on the next
// Resolve, with no restart.
func TestLiveDialResolver_TracksMidProcessTierChange(t *testing.T) {
	dir := t.TempDir()
	api := dialedSettings(t, "balanced")
	resolver := bootLiveDialResolver(t, dir, api)

	boot := resolver.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if !boot.Enabled {
		t.Fatalf("balanced tier resolved SitePreCall.Enabled = false; fixture does not exercise a live site")
	}

	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.CompactionAggressiveness = "off"
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	got := resolver.Resolve(compaction.ScopeKey{}).ForSite(compaction.SitePreCall)
	if got.Enabled {
		t.Fatalf("dial moved to \"off\" mid-process but SitePreCall.Enabled is still true — the seed made the resolver stop tracking the live dial")
	}
}
