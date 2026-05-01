// Package mcp — TestResult wire type (WP07).
//
// TestResult lives here rather than in core/rpc/views/mcp so that
// the Wails binding layer (core/rpc/bindings.go) can import it
// without also pulling in the stdio transport package (which would
// create a cycle: core/mcp/transport/stdio imports core/mcp).
package mcp

import (
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// TestResult carries the outcome of a one-shot Test Connection run.
// Fields follow Go JSON tag conventions (snake_case). The frontend
// wire type is MCPTestResult in frontend/src/lib/types.ts.
type TestResult struct {
	// OK is true when initialize + at least tools/list succeeded.
	OK bool `json:"ok"`
	// ProtocolVersion is the value the server returned in its
	// initialize result.
	ProtocolVersion string `json:"protocol_version,omitempty"`
	// ServerInfo is the {name, version} block from the server's
	// initialize result.
	ServerInfo transport.Implementation `json:"server_info"`
	// Capabilities is the raw server capabilities from initialize.
	Capabilities transport.ServerCapabilities `json:"capabilities"`
	// ToolCount is len(tools/list) when the server advertises tools;
	// -1 means the server did not advertise the tools capability.
	ToolCount int `json:"tool_count"`
	// ResourceCount is len(resources/list) when the server advertises
	// resources; -1 when not advertised.
	ResourceCount int `json:"resource_count"`
	// PromptCount is len(prompts/list) when the server advertises
	// prompts; -1 when not advertised.
	PromptCount int `json:"prompt_count"`
	// StderrTail is up to 4 KiB of stderr captured from a stdio
	// server on failure. Empty for HTTP/SSE transports and on success.
	StderrTail string `json:"stderr_tail,omitempty"`
	// DurationMs is the wall time of the whole test run in milliseconds.
	DurationMs int64 `json:"duration_ms"`
	// Error is a human-readable description of the failure when OK=false.
	Error string `json:"error,omitempty"`
}
