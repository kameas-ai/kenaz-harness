// Package mcp defines the MCPAPI view-scoped accessor.
package mcp

import (
	"context"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Server is reference-only metadata about a configured MCP server.
//
// Transport + Capabilities surface the structural information the
// /tools view renders today; richer metadata (last-seen handshake
// timestamps, latency, advertisement-vs-actual capability deltas)
// arrives once the mcp-client mission lands its concrete Registry.
type Server struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Version      string   `json:"version"`
	Transport    string   `json:"transport,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// HealthEntry is the per-recipe health snapshot returned by HealthSnapshot.
// It mirrors transport.RecipeStatus with a simplified shape suitable for
// the Tools panel and recipe detail page (mcp-server-health-ui WP01).
type HealthEntry struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	LastError       string `json:"last_error,omitempty"`
	RestartAttempts int    `json:"restart_attempts"`
	StderrTail      string `json:"stderr_tail,omitempty"`
	ToolCount       int    `json:"tool_count"`
	ServerName      string `json:"server_name,omitempty"`
	ServerVersion   string `json:"server_version,omitempty"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
}

// MCPAPI is the view-scoped accessor for MCP server lifecycle and streams.
type MCPAPI interface {
	ListServers(ctx context.Context) ([]Server, error)
	StartStream(ctx context.Context, serverID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
	// TestRecipe runs a one-shot connection test against the recipe
	// identified by id. env and config override the recipe's stored
	// values (used by the "Custom" and "From registry" Add modal tabs
	// before the user saves the recipe). Both are nil-tolerant.
	// Returns a TestResult regardless of outcome; err is only set for
	// pre-flight failures (recipe not found, empty catalog).
	TestRecipe(ctx context.Context, recipeID string, env map[string]string, config map[string]any) (coremcp.TestResult, error)
	// HealthSnapshot returns the current health snapshot for every installed
	// recipe. The result is a map of recipe-id → HealthEntry so the frontend
	// can merge it by key without full list diffing.
	// (mcp-server-health-ui WP01)
	HealthSnapshot(ctx context.Context) (map[string]HealthEntry, error)
	// SubscribeHealthChanges registers a subscriber for mcp:health-changed
	// events. Each event is a HealthEntry for the changed recipe. The
	// subscription is torn down via StopStream with the returned id.
	// (mcp-server-health-ui WP02)
	SubscribeHealthChanges(ctx context.Context) (subscriptionID string, err error)
	// SaveCustomRecipe persists a user-authored recipe (the Custom tab in
	// AddMCPServerModal) through the configured RecipeSaver
	// (mcp-connector-lifecycle-01PMMC01 WP06). Returns the saved Recipe
	// (Source stamped "user") or ErrRecipeSaverNotConfigured when no
	// saver is wired.
	SaveCustomRecipe(ctx context.Context, req SaveCustomRecipeRequest) (recipes.Recipe, error)

	// SetToolPolicy upserts a permission rule for (server, tool) into the
	// static permission source (<DataDir>/mcp_servers.json). This is the
	// writer trust-surfaces-that-fire-01PMZ202 WP24 (finding CHAT-05)
	// found missing: toolloop.NewStaticResolverFromDataDir has read that
	// file since confirm-each-enforcement-01PMAG05, but nothing in the
	// tree ever produced it, so no shipped surface could make a tool
	// call resolve to "confirm_each". policy is one of "auto_allow",
	// "confirm_each" or "deny"; tool may be "*" for a whole-server rule.
	//
	// The static resolver is read ONCE at chassis boot
	// (core/rpc/api.go), not per call, so a write here changes behaviour
	// starting with the next restart — it does not hot-reload a running
	// session. Returns ErrDataDirNotConfigured when no real DataDir is
	// wired (the rpc.New(nil) test harness).
	SetToolPolicy(ctx context.Context, server, tool, policy, reason string) error

	// ListToolPolicies returns every rule currently persisted in the
	// static permission source, for a settings surface to render. Empty
	// (not an error) when nothing has been written yet.
	ListToolPolicies(ctx context.Context) ([]toolloop.StaticRule, error)
}
