package toolloop

import (
	"context"
	"encoding/json"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Resolution is the result of mapping a ToolUse.Name to a concrete
// (server, tool) pair plus the per-call permission verdict. WP02 ships
// the permission gate; later WPs (hooks, redaction) extend Resolution
// with mutated args / redaction metadata via separate fields.
//
// Reason is optional human text surfaced in the synthetic deny tool
// result so the model and the audit log know *why* a call was blocked.
type Resolution struct {
	Server string
	Tool   string
	Policy ToolPolicy
	Reason string
}

// SessionHistoryRW is the narrowed slice of session.Manager the loop
// consumes. Defined here so core/toolloop has no dependency on
// core/session (DIRECTIVE_001 — keeps the import graph acyclic and
// lets tests substitute a fake without dragging the manager in).
//
// AppendMessage matches the manager's existing semantics: the loop
// passes a role + content string and the storage layer assigns
// sequence + timestamps.
type SessionHistoryRW interface {
	AppendMessage(ctx context.Context, sessionID, role, content string) error
}

// MCPPool is the surface the loop calls into. It mirrors the relevant
// subset of mcp.Pool — defining it here lets tests inject a pure
// in-memory fake without importing the fixture (which already imports
// mcp), and shields core/toolloop from future churn in the wire
// MCP transport.
type MCPPool interface {
	Tools(ctx context.Context) ([]Tool, error)
	Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)
}

// Tool is the loop-internal projection of mcp.Tool — only the fields
// the resolver needs.
type Tool struct {
	Server string
	Name   string
}

// recordedTurn is the loop's running record of a single round-trip:
// the assistant response that emitted tool calls, plus each tool
// invocation's result. The loop converts these into corellm.Message
// values when re-invoking the registry.
//
// Kept internal to WP01 — once we add structured tool_result content
// parts to corellm.ContentPart this collapses into Message construction.
type recordedTurn struct {
	Assistant corellm.Response
	Results   []toolResult
}

type toolResult struct {
	ToolUseID string
	Server    string
	Tool      string
	Output    string // serialized result; error message on failure
	IsError   bool
}
