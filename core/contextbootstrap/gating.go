package contextbootstrap

import (
	"context"
	"fmt"
)

// gating.go implements WP08: MCP install/connect gating.
//
// Each chosen connector must have its MCP server installed AND authorized
// (i.e. currently live in the pool) before extraction begins. Connectors
// that fail gating are placed in GatingResult.Blocked; they are excluded
// from the extraction run.
//
// The user is not blocked from running extraction on the approved connectors
// while blocked ones are being set up. This lets the run proceed incrementally.

// MCPInstallStatus describes the liveness of one MCP recipe.
type MCPInstallStatus string

const (
	MCPStatusRunning     MCPInstallStatus = "running"
	MCPStatusNotInstalled MCPInstallStatus = "not_installed"
	MCPStatusNotAuthorized MCPInstallStatus = "not_authorized"
	MCPStatusUnknown     MCPInstallStatus = "unknown"
)

// Gater checks connector MCP liveness and produces a GatingResult.
type Gater struct {
	pool    MCPPool
	catalog []ConnectorDef
}

// newGater constructs a Gater.
func newGater(pool MCPPool, catalog []ConnectorDef) *Gater {
	return &Gater{pool: pool, catalog: catalog}
}

// Gate checks each connector in selectedIDs. Returns a GatingResult with
// approved (live) and blocked (missing/unauthorized) connectors.
//
// A connector is "approved" when:
//   - It has a ConnectorDef in the catalog.
//   - Its MCP recipe is currently live in the pool (IsRunning returns true).
//   - It has at least one ReadOnlyTool configured (so extraction can run).
//
// A connector is "blocked" when any of the above conditions fail.
func (g *Gater) Gate(ctx context.Context, selectedIDs []string) GatingResult {
	catalogByID := make(map[string]ConnectorDef, len(g.catalog))
	for _, c := range g.catalog {
		catalogByID[c.ID] = c
	}

	var approved []string
	var blocked []BlockedConnector

	for _, id := range selectedIDs {
		def, ok := catalogByID[id]
		if !ok {
			blocked = append(blocked, BlockedConnector{
				ConnectorID: id,
				Label:       id,
				Reason:      "not_found",
				GuidanceURL: "",
			})
			continue
		}

		if len(def.ReadOnlyTools) == 0 {
			// Connector exists in the catalog but has no extraction tools yet.
			// Treat as skipped rather than blocked (no guidance needed).
			continue
		}

		status := g.checkMCPStatus(ctx, def)
		switch status {
		case MCPStatusRunning:
			approved = append(approved, id)
		case MCPStatusNotInstalled:
			blocked = append(blocked, BlockedConnector{
				ConnectorID: id,
				Label:       def.Label,
				Reason:      "not_installed",
				GuidanceURL: guidanceURL(def),
			})
		case MCPStatusNotAuthorized:
			blocked = append(blocked, BlockedConnector{
				ConnectorID: id,
				Label:       def.Label,
				Reason:      "not_authorized",
				GuidanceURL: guidanceURL(def),
			})
		default:
			blocked = append(blocked, BlockedConnector{
				ConnectorID: id,
				Label:       def.Label,
				Reason:      fmt.Sprintf("unknown_status:%s", status),
				GuidanceURL: guidanceURL(def),
			})
		}
	}

	return GatingResult{
		Approved: approved,
		Blocked:  blocked,
	}
}

// checkMCPStatus checks whether the MCP server for the given connector is live.
// It first checks IsRunning on the pool; if not running it checks whether any
// tool with the recipe's server prefix is listed (which would indicate it's
// installed but authorization failed).
func (g *Gater) checkMCPStatus(ctx context.Context, def ConnectorDef) MCPInstallStatus {
	if def.MCPRecipeID == "" {
		return MCPStatusUnknown
	}

	// Primary: ask the pool whether the recipe's server is running.
	if g.pool.IsRunning(ctx, def.MCPRecipeID) {
		return MCPStatusRunning
	}

	// Secondary: check whether any tool from this server is listed.
	// This can happen when the pool lists tools from a partially-initialized server.
	tools, err := g.pool.Tools(ctx)
	if err != nil {
		return MCPStatusUnknown
	}
	for _, t := range tools {
		if t.Server == def.MCPRecipeID {
			// Server is present but not fully authorized/running.
			return MCPStatusNotAuthorized
		}
	}

	// No tools found for this server at all — not installed.
	return MCPStatusNotInstalled
}

// guidanceURL returns a deep-link or documentation URL to help the user
// connect the given MCP recipe.
// TODO: populate from the recipe's DocsURL when recipe catalog integration lands.
func guidanceURL(def ConnectorDef) string {
	// DEFERRED: when the MCP recipe catalog is available, return the recipe's
	// DocsURL here. For now, return an empty string.
	_ = def
	return ""
}
