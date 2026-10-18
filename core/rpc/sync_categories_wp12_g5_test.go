// sync_categories_wp12_g5_test.go — the G-5 pushed-but-inert-field gate
// (fleet-enforcement-truth-01PMZ505 WP12, spec §5.11/§7 G-5, AC-022).
//
// Before this file, docs/unwired-ledger.md's 2026-09-12 entry recorded the
// G-5 enumeration test as explicitly NOT built: "G-5 enumeration test
// (AC-022) NOT built." Everything else in that WP12 pass (the Accent
// removal, the panel-copy rewrite) landed; only the assertion that makes
// the invariant durable did not.
//
// The gate: for every category returned by corefleet.AllSyncCategories(),
// marshal its Collect() output and require every top-level JSON key to
// appear in syncCategoryKeyConsumers, naming the real (non-sync-applier)
// branch that reads the value back out of settings. A key with no entry
// is exactly the SD-08 shape — a value the device pushes to every other
// enrolled device that nothing on this device (or the receiving one) ever
// reads for anything but writing it back to storage.
//
// "Real consumer" excludes the category's own Apply closure: per spec
// §1.2's rule (already applied to FleetModelPrefs()), a read that only
// copies the value into another struct is not consumption. Every entry
// below names a branch outside core/rpc/sync_categories.go.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// syncCategoryKeyConsumers maps each registered sync category to the set
// of top-level JSON keys its Collect() output may carry, each naming the
// real, non-sync-applier consumer that reads it back out of settings.
//
// provider_profiles and mcp_recipes are intentionally absent (or present
// with an empty key set) — both are wired through emptyPayloadKind /
// mcpRecipesKind's user-scope arm, whose Collect always returns `{}`
// (E-006, "not yet syncing" — spec §1.10). A category with zero collected
// keys trivially satisfies this gate; the day either grows a real
// credential-free snapshot accessor, its keys must be added here or this
// test fails, by design (that is the point of the gate).
var syncCategoryKeyConsumers = map[corefleet.SyncCategory]map[string]string{
	corefleet.SyncCategoryUITheme: {
		"theme": "frontend/src/lib/useTheme.ts reads Settings.Theme (via " +
			"Settings_Get) to drive the live theme switch — not merely " +
			"SettingsStore.LoadTheme/SaveTheme, which is storage, not a " +
			"consumer.",
	},
	corefleet.SyncCategoryModelPrefs: {
		"compactionAggressiveness": "core/compactionpolicy/policy.go's " +
			"CompactionAggressiveness branches compaction sweep behavior " +
			"(AggressivenessOff short-circuits core/rpc/api.go's sweep at " +
			"api.go:~6280); core/agentgraph's chat runner reads the " +
			"effective value per turn.",
		"compactionArchiveDays": "core/rpc/api.go's sweep goroutine reads " +
			"Settings.EffectiveCompactionArchiveDays() as the RunSweep " +
			"cutoff (api.go:6287).",
		"compactionRecentWindow": "core/agentgraph/compaction/session_engine.go " +
			"reads Settings.CompactionRecentWindow to decide how many " +
			"recent messages the compactor keeps uncompacted.",
		"maxAgentTurns": "core/rpc/views/agentgraph/chat/chat_runner.go " +
			"resolves Settings.EffectiveMaxAgentTurns() per StartStream " +
			"and enforces the per-run turn cap.",
	},
	corefleet.SyncCategoryInstalledMCP: {
		"items": "the MCPRegistryWriter this category's Apply writes " +
			"through (core/fleet/sync_mcp.go's MCPSyncCategory.Apply) " +
			"feeds the installed-recipe store that core/rpc/views/tools's " +
			"ListRecipes and the MCP server pool read at tool-load time — " +
			"a real branch downstream of the write, not the write itself.",
	},
	corefleet.SyncCategoryProviderProfiles: {
		// Collect always returns {} (emptyPayloadKind) — zero keys expected.
	},
	corefleet.SyncCategoryMCPRecipes: {
		// Collect always returns {} on the user-scope arm that this test
		// exercises (mcpRecipesKind's Collect closure ignores scope
		// entirely and always returns {}); the org-scope Apply arm is a
		// different, already-tested path (fleet-org-config-inheritance
		// provisioned_mcp) with no Collect-side payload of its own.
	},
}

// verifySyncPayloadKeysMapped is the pure checking function the gate is
// built on: given a category's raw Collect() JSON and its consumer map,
// return every top-level key with no consumer entry. Extracted as a pure
// function (no *testing.T) so both the real-registry test and the
// planted-violation proofs below can call it directly.
func verifySyncPayloadKeysMapped(raw []byte, consumers map[string]string) ([]string, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	var unmapped []string
	for k := range payload {
		if _, ok := consumers[k]; !ok {
			unmapped = append(unmapped, k)
		}
	}
	sort.Strings(unmapped)
	return unmapped, nil
}

// TestSyncCategoryPayloads_EveryKeyHasNamedConsumer is AC-022's main
// assertion: every key every registered category actually collects, built
// exactly the way production builds it (registerSyncCategories, a real
// file-backed SettingsStore, a real MCPSyncCategory with a populated
// fake reader so installed_mcp's "items" key is actually exercised
// non-trivially), must have a consumer-map entry.
func TestSyncCategoryPayloads_EveryKeyHasNamedConsumer(t *testing.T) {
	store := newTestStore(t)
	s, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.Theme = "dark"
	s.CompactionAggressiveness = "balanced"
	s.CompactionArchiveDays = 14
	s.CompactionRecentWindow = 5
	s.MaxAgentTurns = 42
	if err := store.SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	reader := &fakeMCPReader{items: []corefleet.InstalledMCP{
		{ID: "mcp-1", RecipeID: "github", EnabledState: true},
	}}
	mcpCat := corefleet.NewMCPSyncCategory(reader, nil, func() map[string]bool { return map[string]bool{} }, nil)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, mcpCat, nil)

	for _, cat := range corefleet.AllSyncCategories() {
		raw, err := syncer.CollectCategory(context.Background(), cat)
		if err != nil {
			t.Fatalf("CollectCategory(%s): %v", cat, err)
		}
		consumers, known := syncCategoryKeyConsumers[cat]
		if !known {
			t.Errorf("category %s is registered but has no entry (even an "+
				"empty one) in syncCategoryKeyConsumers — add one", cat)
			continue
		}
		unmapped, err := verifySyncPayloadKeysMapped(raw, consumers)
		if err != nil {
			t.Errorf("%s: %v", cat, err)
			continue
		}
		if len(unmapped) > 0 {
			t.Errorf("%s payload carries key(s) %v with no consumer entry in "+
				"syncCategoryKeyConsumers — this is the SD-08 shape: a value "+
				"pushed to every enrolled device that nothing reads", cat, unmapped)
		}
	}
}

// ── Planted-violation proofs (AC-022's own "must fail if / must pass") ─────

// TestG5_PlantedUnmappedKey_Fails proves the checker actually fires: a
// synthetic payload carrying a key with no consumer-map entry must be
// flagged. Modeled on exactly the shape the spec predicts — "a field is
// added to uiThemePayload with no map entry."
func TestG5_PlantedUnmappedKey_Fails(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{
		"theme":         "dark",
		"zzUnmappedKey": "this key has no consumer entry",
	})
	unmapped, err := verifySyncPayloadKeysMapped(raw, syncCategoryKeyConsumers[corefleet.SyncCategoryUITheme])
	if err != nil {
		t.Fatalf("verifySyncPayloadKeysMapped: %v", err)
	}
	if len(unmapped) != 1 || unmapped[0] != "zzUnmappedKey" {
		t.Fatalf("unmapped = %v, want exactly [zzUnmappedKey] — the planted "+
			"violation must be caught, and the pre-existing mapped key "+
			"(theme) must NOT be flagged", unmapped)
	}
}

// TestG5_MappedKeyOnly_Passes is the required inverse: a payload whose
// keys are ALL present in the consumer map must produce zero unmapped
// keys. Without this, a checker that flags everything (or nothing) would
// pass TestG5_PlantedUnmappedKey_Fails vacuously — spec §7 G-5's own
// words: "a test that fails on every addition is a tax, not a gate."
func TestG5_MappedKeyOnly_Passes(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"theme": "dark"})
	unmapped, err := verifySyncPayloadKeysMapped(raw, syncCategoryKeyConsumers[corefleet.SyncCategoryUITheme])
	if err != nil {
		t.Fatalf("verifySyncPayloadKeysMapped: %v", err)
	}
	if len(unmapped) != 0 {
		t.Fatalf("unmapped = %v, want none — every key in this payload has a "+
			"consumer entry", unmapped)
	}
}

// TestG5_ConsumerEntryNamingTheApplierItself_IsNotAcceptable pins the
// spec's own worked example (§9 AC-022: "a map entry naming the sync
// applier itself fails ... the same rule spec §1.2 applies to
// FleetModelPrefs()"). This does not re-derive that rule generically (it
// is a documentation/code-review discipline, not something a string match
// can enforce without false positives on legitimate prose) — it instead
// pins the one concrete case the spec names, so a future edit that
// resurrects the rejected shape for this exact field is caught.
func TestG5_ConsumerEntryNamingTheApplierItself_IsNotAcceptable(t *testing.T) {
	entry, ok := syncCategoryKeyConsumers[corefleet.SyncCategoryModelPrefs]["compactionAggressiveness"]
	if !ok {
		t.Fatal("compactionAggressiveness has no consumer entry at all")
	}
	// The rejected shape reads like "written back to settings by the sync
	// applier" or "applied to the store by Apply" — i.e. it names this
	// file's own Apply closure as the consumer. The accepted entry above
	// names core/compactionpolicy and core/agentgraph instead.
	forbidden := []string{
		"sync_categories.go",
		"this file's Apply",
		"the sync applier",
		"written back to settings",
	}
	for _, f := range forbidden {
		if strings.Contains(entry, f) {
			t.Errorf("consumer entry for compactionAggressiveness reads %q, "+
				"which names the sync applier itself as its own consumer — "+
				"not a real branch. entry: %s", f, entry)
		}
	}
}
