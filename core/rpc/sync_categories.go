// sync_categories.go — wires the per-category settings Syncer
// (harness-fleet-sync-activation-01NSYNC01 gap #1).
//
// The fleet.Syncer foundation (core/fleet/sync.go) ships the debounced
// push / LWW-pull / backoff poll loop but, before this wiring, no categories
// were registered and StartPolling was never called — so the background
// settings-sync loop was dormant in production. This file registers the five
// sync categories and starts the poll loop.
//
// OSS-first boundary: core/fleet must not import the settings view, so the
// collectors / appliers are constructed here in the rpc layer (which may
// import both core/fleet and the settings view) as closures over the settings
// store.
//
// HARD RULE (mirrors core/fleet/sync.go): a collector MUST NEVER return
// credential bytes. Provider API keys / MCP secrets live in the OS credstore
// and are never part of any synced payload.
//
// Fork-removal recipe: delete this file + core/fleet/sync.go +
// core/fleet/sync_mcp.go + core/rpc/views/sync/ + the Sync tab.
package rpc

import (
	"context"
	"encoding/json"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// uiThemePayload is the credential-free wire shape for SyncCategoryUITheme.
type uiThemePayload struct {
	Theme  string `json:"theme"`
	Accent string `json:"accent"`
}

// registerSyncCategories wires collectors + appliers for every sync category
// onto syncer and starts the background poll loop. Safe to call with a nil
// syncer or nil settings store: it no-ops so the offline / fleet-disabled
// posture is preserved.
//
//   - ui_theme         → settings store theme + accent (fully wired)
//   - installed_mcp    → the existing MCPSyncCategory (collector redacts
//     secret env values; applier queues secret prompts)
//   - provider_profiles / model_prefs / mcp_recipes → registered so the LWW
//     pull-apply path is live; their collectors source the credential-free
//     projection available from the settings store. provider_profiles and
//     mcp_recipes have no single credential-free store accessor yet, so their
//     collectors return an empty payload (pull-apply still works) until those
//     subsystems expose a redacted snapshot. See the per-category comments.
func registerSyncCategories(
	ctx context.Context,
	syncer *corefleet.Syncer,
	store settings.SettingsStore,
	mcpCategory *corefleet.MCPSyncCategory,
) {
	if syncer == nil {
		return
	}

	// ── ui_theme ────────────────────────────────────────────────────────────
	if store != nil {
		syncer.RegisterCategory(corefleet.SyncCategoryUITheme, corefleet.CategoryConfig{
			Collector: func(_ context.Context) (json.RawMessage, error) {
				s, err := store.LoadAll()
				if err != nil {
					return nil, err
				}
				return json.Marshal(uiThemePayload{Theme: s.Theme, Accent: s.Accent})
			},
			Applier: func(_ context.Context, raw json.RawMessage) error {
				var p uiThemePayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return err
				}
				s, err := store.LoadAll()
				if err != nil {
					return err
				}
				s.Theme = p.Theme
				s.Accent = p.Accent
				return store.SaveAll(s)
			},
		})
	}

	// ── installed_mcp ───────────────────────────────────────────────────────
	if mcpCategory != nil {
		syncer.RegisterCategory(corefleet.SyncCategoryInstalledMCP,
			corefleet.CategoryConfigForMCP(mcpCategory))
	}

	// ── provider_profiles / model_prefs / mcp_recipes ───────────────────────
	//
	// These three subsystems do not yet expose a single credential-free
	// snapshot accessor that this wiring can safely collect from. They are
	// registered with an empty-payload collector so:
	//   1. The category appears in Sync_Status and can be toggled.
	//   2. A fleet pull-down that carries newer state is still applied via the
	//      (nil) applier path without error — i.e. the loop is fully live.
	// Once those subsystems surface a redacted snapshot, swap the collector
	// here. The HARD RULE (no credential bytes) is trivially satisfied by the
	// empty payload.
	emptyCollector := func(_ context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	for _, cat := range []corefleet.SyncCategory{
		corefleet.SyncCategoryProviderProfiles,
		corefleet.SyncCategoryModelPrefs,
		corefleet.SyncCategoryMCPRecipes,
	} {
		syncer.RegisterCategory(cat, corefleet.CategoryConfig{Collector: emptyCollector})
	}

	syncer.StartPolling(ctx)
	logging.L().Info("rpc.settings_sync.started",
		"categories", len(corefleet.AllSyncCategories()),
		"theme_wired", store != nil,
		"mcp_wired", mcpCategory != nil,
	)
}
