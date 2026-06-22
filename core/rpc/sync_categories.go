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

// modelPrefsPayload is the credential-free wire shape for SyncCategoryModelPrefs.
// Only fields that carry NO credential bytes are included.
// API keys / credential store references live outside the Settings struct and
// must never appear here. This payload covers model selection + compaction prefs.
type modelPrefsPayload struct {
	CompactionAggressiveness string `json:"compactionAggressiveness,omitempty"`
	CompactionArchiveDays    int    `json:"compactionArchiveDays,omitempty"`
	CompactionRecentWindow   int    `json:"compactionRecentWindow,omitempty"`
	MaxAgentTurns            int    `json:"maxAgentTurns,omitempty"`
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

	// ── model_prefs ─────────────────────────────────────────────────────────
	//
	// Credential-free model preference fields: compaction aggressiveness,
	// archive days, recent window, max agent turns. API keys are in the
	// credential store and never appear here.
	if store != nil {
		syncer.RegisterCategory(corefleet.SyncCategoryModelPrefs, corefleet.CategoryConfig{
			Collector: func(_ context.Context) (json.RawMessage, error) {
				s, err := store.LoadAll()
				if err != nil {
					return nil, err
				}
				return json.Marshal(modelPrefsPayload{
					CompactionAggressiveness: s.CompactionAggressiveness,
					CompactionArchiveDays:    s.CompactionArchiveDays,
					CompactionRecentWindow:   s.CompactionRecentWindow,
					MaxAgentTurns:            s.MaxAgentTurns,
				})
			},
			Applier: func(_ context.Context, raw json.RawMessage) error {
				var p modelPrefsPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return err
				}
				s, err := store.LoadAll()
				if err != nil {
					return err
				}
				if p.CompactionAggressiveness != "" {
					s.CompactionAggressiveness = p.CompactionAggressiveness
				}
				if p.CompactionArchiveDays > 0 {
					s.CompactionArchiveDays = p.CompactionArchiveDays
				}
				if p.CompactionRecentWindow > 0 {
					s.CompactionRecentWindow = p.CompactionRecentWindow
				}
				if p.MaxAgentTurns > 0 {
					s.MaxAgentTurns = p.MaxAgentTurns
				}
				return store.SaveAll(s)
			},
		})
	}

	// ── provider_profiles / mcp_recipes ─────────────────────────────────────
	//
	// These subsystems do not yet expose a single credential-free snapshot
	// accessor. They are registered with an empty-payload collector (HARD
	// RULE: no credential bytes) and a no-op applier (non-nil so the pull-
	// apply path is live — when fleet carries newer state the loop runs
	// through without error, ready for the subsystem to be extended later).
	emptyCollector := func(_ context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	noopApplier := func(_ context.Context, _ json.RawMessage) error {
		return nil
	}
	for _, cat := range []corefleet.SyncCategory{
		corefleet.SyncCategoryProviderProfiles,
		corefleet.SyncCategoryMCPRecipes,
	} {
		syncer.RegisterCategory(cat, corefleet.CategoryConfig{
			Collector: emptyCollector,
			Applier:   noopApplier,
		})
	}

	syncer.StartPolling(ctx)
	logging.L().Info("rpc.settings_sync.started",
		"categories", len(corefleet.AllSyncCategories()),
		"theme_wired", store != nil,
		"mcp_wired", mcpCategory != nil,
	)
}
