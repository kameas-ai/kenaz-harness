// sync_categories.go — wires the per-category settings Syncer
// (harness-fleet-sync-activation-01NSYNC01 gap #1).
//
// The fleet.Syncer foundation (core/fleet/sync.go) ships the debounced
// push / LWW-pull / backoff poll loop but, before this wiring, no categories
// were registered and StartPolling was never called — so the background
// settings-sync loop was dormant in production. This file registers the
// sync categories and starts the poll loop.
//
// fleet-generic-sync-framework-01NSYNC02 WP01: the five categories below are
// now declared as corefleet.SyncKind values and registered through a
// corefleet.KindRegistry (core/fleet/synckind.go) — the single registration
// point every kind, existing or future, plugs into (spec §2.1). Each kind's
// Collect/Apply logic is byte-for-byte the same as before this refactor;
// only the registration *shape* changed. See sync_categories_test.go for
// the round-trip regression pins that back this claim.
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
// core/fleet/synckind.go + core/fleet/sync_mcp.go + core/rpc/views/sync/ +
// the Sync tab.
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

// uiThemeKind builds the ui_theme SyncKind. Nil store yields a kind with no
// Collect/Apply wired — the caller skips registration in that case, exactly
// as the pre-registry code skipped RegisterCategory when store == nil.
func uiThemeKind(store settings.SettingsStore) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:        string(corefleet.SyncCategoryUITheme),
		Scopes:    []corefleet.Scope{corefleet.ScopeUser},
		Transport: corefleet.TransportLWWCategory,
		Collect: func(_ context.Context) ([]byte, error) {
			s, err := store.LoadAll()
			if err != nil {
				return nil, err
			}
			return json.Marshal(uiThemePayload{Theme: s.Theme, Accent: s.Accent})
		},
		Apply: func(_ context.Context, _ corefleet.Scope, raw []byte) error {
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
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// modelPrefsKind builds the model_prefs SyncKind.
//
// Credential-free model preference fields: compaction aggressiveness,
// archive days, recent window, max agent turns. API keys are in the
// credential store and never appear here.
func modelPrefsKind(store settings.SettingsStore) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:        string(corefleet.SyncCategoryModelPrefs),
		Scopes:    []corefleet.Scope{corefleet.ScopeUser, corefleet.ScopeOrg},
		Transport: corefleet.TransportLWWCategory,
		Collect: func(_ context.Context) ([]byte, error) {
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
		Apply: func(_ context.Context, _ corefleet.Scope, raw []byte) error {
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
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// installedMCPKind adapts the existing MCPSyncCategory (core/fleet/sync_mcp.go)
// into a SyncKind. Collect/Apply logic is unchanged — MCPSyncCategory already
// redacts secret env values on collect and queues secret prompts on apply.
func installedMCPKind(mcpCategory *corefleet.MCPSyncCategory) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:        string(corefleet.SyncCategoryInstalledMCP),
		Scopes:    []corefleet.Scope{corefleet.ScopeUser, corefleet.ScopeOrg},
		Transport: corefleet.TransportLWWCategory,
		Collect: func(ctx context.Context) ([]byte, error) {
			raw, err := mcpCategory.Collect(ctx)
			if err != nil {
				return nil, err
			}
			return []byte(raw), nil
		},
		Apply: func(ctx context.Context, _ corefleet.Scope, raw []byte) error {
			return mcpCategory.Apply(ctx, raw)
		},
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// emptyPayloadKind builds a SyncKind for categories that do not yet expose a
// single credential-free snapshot accessor (provider_profiles, mcp_recipes).
// The collector returns an empty JSON object (HARD RULE: no credential
// bytes) and the applier is a no-op (non-nil so the pull-apply path is
// live — when fleet carries newer state the loop runs through without
// error, ready for the subsystem to be extended later).
func emptyPayloadKind(id corefleet.SyncCategory, scopes []corefleet.Scope) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:        string(id),
		Scopes:    scopes,
		Transport: corefleet.TransportLWWCategory,
		Collect: func(_ context.Context) ([]byte, error) {
			return []byte(`{}`), nil
		},
		Apply: func(_ context.Context, _ corefleet.Scope, _ []byte) error {
			return nil
		},
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// registerSyncCategories wires collectors + appliers for every sync category
// onto syncer through the SyncKind registry, and starts the background poll
// loop. Safe to call with a nil syncer or nil settings store: it no-ops so
// the offline / fleet-disabled posture is preserved. Returns the
// KindRegistry so the caller can hold it for later use (diagnostics, the
// future Settings → Sync surface — WP06).
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
) *corefleet.KindRegistry {
	registry := corefleet.NewKindRegistry()
	if syncer == nil {
		return registry
	}

	register := func(k corefleet.SyncKind) {
		if err := registry.Register(k); err != nil {
			// A registration error here is a call-site bug (duplicate ID
			// or missing required field) — log and skip rather than panic,
			// so a single bad registration never takes down sync entirely.
			logging.L().Error("rpc.settings_sync.kind_registration_failed",
				"kind", k.ID, "err", err.Error())
			return
		}
		syncer.RegisterCategory(corefleet.SyncCategory(k.ID), k.CategoryConfig())
	}

	// ── ui_theme ────────────────────────────────────────────────────────────
	if store != nil {
		register(uiThemeKind(store))
	}

	// ── installed_mcp ───────────────────────────────────────────────────────
	if mcpCategory != nil {
		register(installedMCPKind(mcpCategory))
	}

	// ── model_prefs ─────────────────────────────────────────────────────────
	if store != nil {
		register(modelPrefsKind(store))
	}

	// ── provider_profiles / mcp_recipes ─────────────────────────────────────
	register(emptyPayloadKind(corefleet.SyncCategoryProviderProfiles,
		[]corefleet.Scope{corefleet.ScopeUser, corefleet.ScopeOrg}))
	register(emptyPayloadKind(corefleet.SyncCategoryMCPRecipes,
		[]corefleet.Scope{corefleet.ScopeUser, corefleet.ScopeOrg}))

	syncer.StartPolling(ctx)
	logging.L().Info("rpc.settings_sync.started",
		"categories", len(registry.IDs()),
		"theme_wired", store != nil,
		"mcp_wired", mcpCategory != nil,
	)
	return registry
}
