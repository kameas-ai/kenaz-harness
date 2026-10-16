// Package sync is the view-scoped RPC surface for per-category settings sync
// (fleet-share-and-sync-01NDFSEX14 WP05).
//
// The frontend SyncPanel.vue binds here.
// Fork-removal recipe: delete this package + SyncPanel.vue + the "Sync" tab.
package sync

import (
	"context"
	"errors"
)

// SyncStatusView is the wire-safe view of one category's sync state.
type SyncStatusView struct {
	Category   string `json:"category"`
	Enabled    bool   `json:"enabled"`
	LastPushAt string `json:"last_push_at,omitempty"` // RFC3339
	LastPullAt string `json:"last_pull_at,omitempty"` // RFC3339
	LastError  string `json:"last_error,omitempty"`
	// Scopes lists which layers this kind participates in ("user", "org",
	// "team" — fleet.Scope values, stringified), read from the SyncKind
	// registry (fleet-generic-sync-framework-01NSYNC02 WP06, FR-007).
	// Empty when the registry has no registration for this category
	// (fleet disabled, or a category the registry-based refactor hasn't
	// reached) — the frontend treats an empty Scopes the same as
	// user-only, matching pre-WP01 behavior for the five categories.
	Scopes []string `json:"scopes,omitempty"`
	// OrgAppliedAt is the RFC3339 timestamp of this kind's most recent
	// successful org_config apply (fleet.KindRegistry.OrgAppliedAt), or
	// empty if this kind has never been org-provisioned on this device.
	// This is the WP03 generic provenance signal FR-007 asks for
	// ("... last push/pull, provenance, ...").
	OrgAppliedAt string `json:"org_applied_at,omitempty"`
	// AwaitingConfirmation is true when the category is enabled but its
	// upload is held until the user confirms settings sync once
	// (Sync_ConfirmUploads). Pulls still run.
	AwaitingConfirmation bool `json:"awaiting_confirmation,omitempty"`
	// LocalOnly is true when Fleet does not accept this category
	// (fleet.FleetAcceptsSyncCategory), so it never leaves this device.
	LocalOnly bool `json:"local_only,omitempty"`
	// Conflict is true when another device changed this category after
	// this one last synced, so this device's change was not uploaded.
	// Sync_ForcePush keeps this device's settings; Sync_ForcePull takes
	// the other device's.
	Conflict bool `json:"conflict,omitempty"`
	// NotSyncingYet is true when personal sync for this category is not
	// built yet (fleet.SyncKind.UserSyncNotBuilt): nothing is uploaded or
	// applied, so the UI offers no toggle and never lists it as uploading.
	NotSyncingYet bool `json:"not_syncing_yet,omitempty"`
	// UploadsBlockedReason is non-empty when this category's uploads are
	// held because the settings that record the upload confirmation could
	// not be read (UploadConsent.UploadState). Confirming cannot fix that,
	// so AwaitingConfirmation is false on such a row.
	UploadsBlockedReason string `json:"uploads_blocked_reason,omitempty"`
	// SettingsUnreadable is true when UploadsBlockedReason is set because
	// the settings file itself cannot be read (not because no store is
	// wired). The UI then offers the "Reset settings" recovery
	// (Settings_ResetUnreadable).
	SettingsUnreadable bool `json:"settings_unreadable,omitempty"`
}

// UploadConsent persists the one-time "settings may be uploaded" answer
// (Settings.SettingsSyncUploadsConfirmed). The rpc layer implements it over
// the settings store.
type UploadConsent interface {
	UploadsConfirmed() bool
	ConfirmUploads() error
	// UploadState reads the answer and reports why it could not be read.
	// A non-nil error means uploads are blocked because the settings are
	// unreadable (or missing), not because the user has not confirmed;
	// Sync_Status surfaces it instead of the "Start syncing" prompt.
	UploadState() (confirmed bool, err error)
}

// OptInStore persists the user's per-category opt-ins
// (Settings.SettingsSyncEnabledCategories). The rpc layer implements it over
// the settings store. Without one, toggles last only until the app quits.
type OptInStore interface {
	EnabledCategories() ([]string, error)
	SetCategoryEnabled(category string, enabled bool) error
}

// ErrUploadConsentUnavailable is returned by Sync_ConfirmUploads when no
// settings store is wired, so the answer could not be saved.
var ErrUploadConsentUnavailable = errors.New("sync: settings are unavailable, so the confirmation could not be saved")

// PendingMCPSecret describes an MCP that arrived via sync but needs
// credentials before it can start. Shown in the SyncPanel banner.
type PendingMCPSecret struct {
	// MCPID is the install-unique ID of the MCP.
	MCPID string `json:"mcp_id"`
	// RecipeID is the recipe this MCP was installed from.
	RecipeID string `json:"recipe_id"`
	// RequiresSecretKeys lists the env var names the user must supply.
	RequiresSecretKeys []string `json:"requires_secret_keys"`
}

// SyncAPI is the RPC surface exposed to the Wails frontend.
type SyncAPI interface {
	// Sync_Toggle enables or disables sync for the given category.
	// On toggle-on, triggers an immediate push of current local state.
	Sync_Toggle(ctx context.Context, category string, enabled bool) error

	// Sync_Status returns the sync status for all categories.
	Sync_Status(ctx context.Context) ([]SyncStatusView, error)

	// Sync_ForcePush immediately pushes local state for the category to fleet.
	Sync_ForcePush(ctx context.Context, category string) error

	// Sync_ForcePull immediately pulls remote state for the category and applies.
	Sync_ForcePull(ctx context.Context, category string) error

	// Sync_PendingMCPSecrets returns the list of MCPs that arrived via sync
	// but need credentials from the user before they can start.
	Sync_PendingMCPSecrets(ctx context.Context) ([]PendingMCPSecret, error)

	// Sync_ConfirmUploads records the user's one-time confirmation that
	// enabled categories may upload to Fleet, then uploads every enabled
	// category that was held. Upload failures after the confirmation is
	// saved show per category in Sync_Status, not here.
	Sync_ConfirmUploads(ctx context.Context) error
}
