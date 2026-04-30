// Package mcp defines the MCPAPI view-scoped accessor.
package mcp

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
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

// TestResult is the outcome of a one-shot Test Connection RPC
// (mission mcp-server-install-01KQ8TDP, WP07). It carries the
// post-handshake capability summary the frontend renders in the
// "Test Connection" drawer, plus diagnostic fields (stderr tail,
// error message) for failure cases.
//
// All string fields may be empty on failure; numeric counts default
// to 0 when the server did not advertise the capability or the
// handshake did not complete.
type TestResult struct {
	// OK reports whether the full initialize + tools/list sequence
	// completed within the 30 s timeout without error.
	OK bool `json:"ok"`

	// ServerName / ServerVersion come from the server's initialize
	// response (serverInfo block).
	ServerName    string `json:"serverName"`
	ServerVersion string `json:"serverVersion"`

	// ProtocolVersion is the protocolVersion the server echoed back.
	ProtocolVersion string `json:"protocolVersion"`

	// ToolCount / ResourceCount / PromptCount reflect the sizes of
	// the respective list responses. 0 when the server did not
	// advertise the capability in its initialize response, or when the
	// handshake failed before the lists could be fetched.
	ToolCount     int `json:"toolCount"`
	ResourceCount int `json:"resourceCount"`
	PromptCount   int `json:"promptCount"`

	// StderrTail captures up to 4 KiB of the most recent stderr
	// output from the child process (stdio transport only). Empty for
	// HTTP/SSE transports and for stdio recipes whose servers emitted
	// nothing on stderr.
	StderrTail string `json:"stderrTail"`

	// ErrorMessage is the human-readable error when OK is false.
	// Empty when OK is true.
	ErrorMessage string `json:"errorMessage"`

	// DurationMs is the wall-clock elapsed time from connection Open
	// to Close, in milliseconds.
	DurationMs int64 `json:"durationMs"`
}

// MCPAPI is the view-scoped accessor for MCP server lifecycle and streams.
type MCPAPI interface {
	ListServers(ctx context.Context) ([]Server, error)
	StartStream(ctx context.Context, serverID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// TestRecipe opens a one-shot connection to the server described
	// by recipe, performs the initialize + capability listing
	// handshake, and returns a TestResult summary. The connection is
	// closed before this method returns; the recipe is NOT registered
	// with the production MCP pool.
	//
	// A 30 s context deadline is imposed internally; callers may pass
	// a shorter deadline via ctx to enforce a tighter budget.
	TestRecipe(ctx context.Context, recipe recipes.Recipe) (TestResult, error)
}
