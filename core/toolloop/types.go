package toolloop

import (
	"context"
	"encoding/json"
)

// Resolution is the result of mapping a ToolUse.Name to a concrete
// (server, tool) pair plus the per-call permission verdict. The
// chassis-side discoverer + chat-runner permission gate consume this
// shape so a deny verdict can short-circuit dispatch with the
// `Reason` text surfaced in audit / UI.
type Resolution struct {
	Server string
	Tool   string
	Policy ToolPolicy
	Reason string
}

// MCPPool is the narrow surface the chassis-side chat runner adapts
// onto the kernel's ToolRegistry seam. It mirrors the relevant subset
// of mcp.Pool — defining it here lets tests inject a pure in-memory
// fake without dragging the wire MCP transport in.
type MCPPool interface {
	Tools(ctx context.Context) ([]Tool, error)
	Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)
}

// Tool is this package's projection of mcp.Tool — only the fields the
// resolver needs. (Pre-cutover it was the chassis loop's projection;
// post-cutover the chassis discoverer + chat runner adapter consume
// the same shape so tool catalog assembly stays uniform.)
type Tool struct {
	Server      string
	Name        string
	Description string
	InputSchema json.RawMessage
}
