// Package harness implements the in-process "harness-self" MCP server
// (mission harness-self-mcp-onboarding-01KQ8TDU WP02). The server exposes
// harness configuration as MCP tools so the onboarding agent can configure
// the harness via natural language.
//
// Why in-process: the harness spawns external MCP servers as subprocesses
// today (stdio transport). The harness-self server is special — it
// reaches into the same process's managers (llm.Registry, recipes.Manager,
// session.Manager, etc.). Spawning a subprocess just to call back into
// the same binary would be wasteful. Instead the server exposes a Go
// handler interface and a thin in-process pipe transport that the
// supervisor can swap in for stdio when wiring this server.
//
// Status: WP02 ships the skeleton + Go-native handler dispatch. The
// JSON-RPC framing path through transport.RequestEnvelope /
// ResponseEnvelope is wired by HandleEnvelope so callers that already
// speak the MCP wire format can use the same server. Read/write tool
// handlers (WP04, WP05) and supervisor wiring (WP09 / register.go) are
// stubbed pending those WPs.
//
// One-release compat shim: the concrete types and NewServer constructor
// now live in core/mcp/builtin/toolserver. This package re-exports them
// via type aliases and a thin wrapper so existing callers continue to
// compile unchanged.
//
// New code should import core/mcp/builtin/toolserver directly.
// Mission: sites-mcp-server-01NSITE05 WP01 (extraction).
package harness

import (
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
)

// ServerName is the identifier this server registers under in the MCP
// pool. It must be stable so Cedar policy authors can target it.
const ServerName = "harness-self"

// ServerVersion is the build-time version reported in initialize results.
const ServerVersion = "0.1.0"

// Type aliases — one-release compat shim. Callers that already import
// this package keep compiling without any change.
type (
	// Server is the in-process MCP server.
	Server = toolserver.Server
	// ToolSpec wires a tool name + JSON-schema input + handler.
	ToolSpec = toolserver.ToolSpec
	// ToolHandler is the Go-native shape of a single tool handler.
	ToolHandler = toolserver.ToolHandler
)

// NewServer returns an empty Server pre-configured with the harness-self
// name and version. Callers should Register tools and then either embed
// via the inproc Transport or call HandleEnvelope directly.
//
// This is a thin wrapper so harness callers keep a zero-arg constructor.
func NewServer() *Server {
	return toolserver.NewServer(ServerName, ServerVersion)
}

// Compile-time assertion: Server must be *toolserver.Server.
var _ *toolserver.Server = (*Server)(nil)
