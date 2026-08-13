// sync_categories_slashcmd.go — registers slash_commands as a generic sync
// kind (fleet-generic-sync-framework-01NSYNC02 WP05).
//
// This is the genericity proof: registering a brand-new user-scoped kind
// costs one SyncKind (registry entry) + one collector/applier here — no
// changes to core/fleet/sync.go's Syncer, no new endpoint. It rides the
// exact same PUT/GET /api/v1/sync/{category} transport the five WP01
// categories use (DESIGN.md §5.6: the endpoint is already generic
// server-side; no per-category schema on the harness side).
//
// Scope: only *global*-scope user commands sync (coreslashcmd.ScopeGlobal).
// Project-scoped commands are tied to a specific project checkout that may
// not exist on the receiving device, so they are intentionally excluded —
// same "smallest actually-syncable local store" reasoning as picking
// slash_commands over hooks/keybindings in the first place (see
// docs/sync-kinds.md).
//
// Known v1 limitation: apply is additive/upserting only (SaveUser per
// item) — a command deleted on the source device is not deleted on the
// receiving device by a pull. Full tombstone/delete propagation is left for
// a follow-up; the five WP01 categories have the same limitation today
// (installed_mcp's ApplyInstalled is the only one that fully replaces its
// set, and even that doesn't tombstone across a partial payload).
//
// HARD RULE (mirrors sync_categories.go): the collector MUST NEVER return
// credential bytes. Slash commands are user-authored markdown; the store
// never touches the credstore, so no redaction is needed here (unlike
// installed_mcp).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// SyncCategorySlashCommands is the SyncKind ID / wire category for
// user-defined slash commands (global scope only).
const SyncCategorySlashCommands corefleet.SyncCategory = "slash_commands"

// slashCommandSyncPayload is the wire shape for SyncCategorySlashCommands.
type slashCommandSyncPayload struct {
	Items []coreslashcmd.UserCommand `json:"items"`
}

// slashCommandsKind builds the slash_commands SyncKind. store may not be
// nil — callers gate registration on store != nil the same way the WP01
// categories gate on the settings store.
func slashCommandsKind(store *coreslashcmd.Store) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:        string(SyncCategorySlashCommands),
		Scopes:    []corefleet.Scope{corefleet.ScopeUser},
		Transport: corefleet.TransportLWWCategory,
		Collect: func(ctx context.Context) ([]byte, error) {
			cmds, err := store.LoadUser(ctx, "")
			if err != nil {
				return nil, err
			}
			// Only global-scope commands are synced (see file header).
			global := make([]coreslashcmd.UserCommand, 0, len(cmds))
			for _, c := range cmds {
				if c.Scope != coreslashcmd.ScopeGlobal {
					continue
				}
				// PayloadPath is a device-local filesystem path; never put
				// it on the wire — the receiving Store recomputes it in
				// SaveUser from dataDir + name.
				c.PayloadPath = ""
				global = append(global, c)
			}
			return json.Marshal(slashCommandSyncPayload{Items: global})
		},
		Apply: func(ctx context.Context, _ corefleet.Scope, raw []byte) error {
			var p slashCommandSyncPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return fmt.Errorf("slash_commands apply: decode: %w", err)
			}
			for _, cmd := range p.Items {
				// Enforce global scope regardless of what the payload
				// claims — this category never carries project-scoped
				// commands.
				cmd.Scope = coreslashcmd.ScopeGlobal
				cmd.ProjectID = ""
				if err := coreslashcmd.Validate(cmd); err != nil {
					// A malformed incoming command must not abort the rest
					// of the batch — log and skip, matching the
					// best-effort posture of the other appliers.
					logging.L().Warn("fleet.sync.slash_commands.apply.invalid",
						"name", cmd.Name, "err", err.Error())
					continue
				}
				if err := store.SaveUser(ctx, cmd); err != nil {
					return fmt.Errorf("slash_commands apply: save %q: %w", cmd.Name, err)
				}
			}
			return nil
		},
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// registerSlashCommandsSyncKind registers the slash_commands SyncKind onto
// registry + syncer. Safe to call with a nil syncer, registry, or store —
// no-ops, mirroring registerSyncCategories' nil-guard posture so the
// offline / feature-flagged-off path is preserved.
func registerSlashCommandsSyncKind(
	syncer *corefleet.Syncer,
	registry *corefleet.KindRegistry,
	store *coreslashcmd.Store,
) {
	if syncer == nil || registry == nil || store == nil {
		return
	}
	k := slashCommandsKind(store)
	if err := registry.Register(k); err != nil {
		logging.L().Error("rpc.settings_sync.kind_registration_failed",
			"kind", k.ID, "err", err.Error())
		return
	}
	syncer.RegisterCategory(SyncCategorySlashCommands, k.CategoryConfig())
	logging.L().Info("rpc.settings_sync.slash_commands_registered")
}
