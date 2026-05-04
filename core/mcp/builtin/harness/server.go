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
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// ServerName is the identifier this server registers under in the MCP
// pool. It must be stable so Cedar policy authors can target it.
const ServerName = "harness-self"

// ServerVersion is the build-time version reported in initialize results.
const ServerVersion = "0.1.0"

// ToolHandler is the Go-native shape of a single harness-self tool. The
// handler receives the parsed JSON arguments (or nil when the caller
// passed none) and returns a result object the server JSON-encodes into
// the MCP ToolsCallResult content.
//
// Returning a non-nil error surfaces as { isError: true } in the wire
// response with the error message in the content text block.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// ToolSpec wires a tool name + JSON-schema input + handler. Schema is
// passed through verbatim to MCP clients via tools/list.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     ToolHandler
	// Redact lists JSON-pointer-style argument names that must NOT
	// appear in audit emissions for this tool. add_provider's api_key
	// argument is the canonical example. Consumed by audit.go (WP10).
	Redact []string
}

// Server is the in-process MCP server. Tools are registered via Register
// at boot; HandleEnvelope dispatches JSON-RPC envelopes against the
// registered tool table.
type Server struct {
	mu    sync.RWMutex
	tools map[string]ToolSpec
}

// NewServer returns an empty Server. Callers should Register tools and
// then either embed via the inproc Transport (WP09) or call
// HandleEnvelope directly.
func NewServer() *Server {
	return &Server{tools: make(map[string]ToolSpec)}
}

// Register installs a tool. Re-registering the same name overwrites the
// previous handler (used by tests; production registers exactly once at
// boot).
func (s *Server) Register(spec ToolSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[spec.Name] = spec
}

// Tools returns a snapshot of every registered tool, sorted by name.
// Used by HandleEnvelope's tools/list path and by tests.
func (s *Server) Tools() []ToolSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolSpec, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	// Stable order so tools/list responses are deterministic.
	sortToolsByName(out)
	return out
}

// Lookup returns the tool spec for name, or false if unregistered.
func (s *Server) Lookup(name string) (ToolSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tools[name]
	return t, ok
}

// HandleEnvelope is the JSON-RPC entrypoint. It accepts a parsed
// RequestEnvelope and returns either a result object (to be stuffed
// into ResponseEnvelope.Result by the transport) or an *RPCError.
//
// Implements: initialize, tools/list, tools/call. Other methods return
// MethodNotFound.
func (s *Server) HandleEnvelope(ctx context.Context, env transport.RequestEnvelope) (any, *transport.RPCError) {
	switch env.Method {
	case transport.MethodInitialize:
		return transport.InitializeResult{
			ProtocolVersion: transport.SupportedProtocolVersion,
			Capabilities: transport.ServerCapabilities{
				Tools: &transport.ToolsCapability{},
			},
			ServerInfo: transport.Implementation{
				Name:    ServerName,
				Version: ServerVersion,
			},
		}, nil

	case transport.MethodToolsList:
		specs := s.Tools()
		out := transport.ToolsListResult{
			Tools: make([]transport.ToolDefinition, 0, len(specs)),
		}
		for _, sp := range specs {
			out.Tools = append(out.Tools, transport.ToolDefinition{
				Name:        sp.Name,
				Description: sp.Description,
				InputSchema: sp.InputSchema,
			})
		}
		return out, nil

	case transport.MethodToolsCall:
		var p transport.ToolsCallParams
		if len(env.Params.(json.RawMessage)) > 0 {
			// env.Params is `any`; transport always passes RawMessage.
			raw, ok := env.Params.(json.RawMessage)
			if !ok {
				return nil, &transport.RPCError{
					Code:    transport.ErrCodeInvalidParams,
					Message: "harness-self: tools/call params must be a JSON object",
				}
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &transport.RPCError{
					Code:    transport.ErrCodeInvalidParams,
					Message: fmt.Sprintf("harness-self: tools/call: %v", err),
				}
			}
		}
		spec, ok := s.Lookup(p.Name)
		if !ok {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeMethodNotFound,
				Message: fmt.Sprintf("harness-self: unknown tool %q", p.Name),
			}
		}
		result, err := spec.Handler(ctx, p.Arguments)
		if err != nil {
			// Surface tool errors through MCP's isError convention rather
			// than as JSON-RPC errors so the caller's tool plumbing sees
			// them like any other failed tool result.
			text := json.RawMessage(jsonText(err.Error()))
			return transport.ToolsCallResult{
				Content: []json.RawMessage{text},
				IsError: true,
			}, nil
		}
		// Success: marshal the result into a single text content block.
		// Tools that need richer content (images / binary) can return a
		// ToolsCallResult directly and we pass it through.
		if tc, ok := result.(transport.ToolsCallResult); ok {
			return tc, nil
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeInternalError,
				Message: fmt.Sprintf("harness-self: marshal result: %v", err),
			}
		}
		return transport.ToolsCallResult{
			Content: []json.RawMessage{json.RawMessage(jsonText(string(body)))},
		}, nil

	default:
		return nil, &transport.RPCError{
			Code:    transport.ErrCodeMethodNotFound,
			Message: fmt.Sprintf("harness-self: method %q not implemented", env.Method),
		}
	}
}

// jsonText wraps a string into the MCP `{type:"text", text:...}` content
// block, returning the marshalled bytes.
func jsonText(s string) []byte {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: s})
	return b
}

// sortToolsByName sorts a slice of ToolSpec in ascending Name order.
// Tiny insertion sort — tool counts are < 20.
func sortToolsByName(ts []ToolSpec) {
	for i := 1; i < len(ts); i++ {
		j := i
		for j > 0 && ts[j-1].Name > ts[j].Name {
			ts[j-1], ts[j] = ts[j], ts[j-1]
			j--
		}
	}
}
