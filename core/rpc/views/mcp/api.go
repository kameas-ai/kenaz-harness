// Package mcp defines the MCPAPI view-scoped accessor.
package mcp

import "context"

// Server is reference-only metadata about a configured MCP server.
type Server struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Version string `json:"version"`
}

// MCPAPI is the view-scoped accessor for MCP server lifecycle and streams.
type MCPAPI interface {
	ListServers(ctx context.Context) ([]Server, error)
	StartStream(ctx context.Context, serverID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
