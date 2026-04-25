// Package mcp defines the MCPAPI view-scoped accessor.
package mcp

import "context"

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

// MCPAPI is the view-scoped accessor for MCP server lifecycle and streams.
type MCPAPI interface {
	ListServers(ctx context.Context) ([]Server, error)
	StartStream(ctx context.Context, serverID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
