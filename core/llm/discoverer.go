package llm

import "context"

// ToolDiscoverer projects the MCP-pool tool catalog into the
// connector-shaped ToolSpec slice the registry serializes onto each
// GenerationRequest. Implementations live outside core/llm (the
// concrete adapter wraps mcp.Pool + toolloop.PermissionResolver in
// core/rpc/views/llm) so the connector stays free of MCP / permission
// imports — keeping that boundary intact is the only reason the
// interface lives here rather than in core/toolloop.
//
// sessionID is forwarded so per-session permission overrides can filter
// the discovery list (a deny rule should make the tool invisible to
// the model, not just block dispatch).
type ToolDiscoverer interface {
	Tools(ctx context.Context, sessionID string) ([]ToolSpec, error)
}

// NoopDiscoverer returns an empty tool list. Used as a default when no
// pool is wired (test paths, pre-MCP builds) so callers never have to
// nil-check.
type NoopDiscoverer struct{}

// Tools always returns (nil, nil).
func (NoopDiscoverer) Tools(_ context.Context, _ string) ([]ToolSpec, error) { return nil, nil }
