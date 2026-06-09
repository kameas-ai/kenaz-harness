// Package sync is the view-scoped RPC surface for per-category settings sync
// (fleet-share-and-sync-01NDFSEX14 WP05).
//
// The frontend SyncPanel.vue binds here.
// Fork-removal recipe: delete this package + SyncPanel.vue + the "Sync" tab.
package sync

import "context"

// SyncStatusView is the wire-safe view of one category's sync state.
type SyncStatusView struct {
	Category   string `json:"category"`
	Enabled    bool   `json:"enabled"`
	LastPushAt string `json:"last_push_at,omitempty"` // RFC3339
	LastPullAt string `json:"last_pull_at,omitempty"` // RFC3339
	LastError  string `json:"last_error,omitempty"`
}

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
}
