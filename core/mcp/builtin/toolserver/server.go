// Package toolserver provides the reusable JSON-RPC MCP server
// machinery extracted from core/mcp/builtin/harness/server.go.
//
// The harness package re-exports type aliases for one release cycle
// (see core/mcp/builtin/harness/server.go) so existing callers
// compile unchanged while new servers (e.g. core/mcp/builtin/sites/)
// import this package directly.
//
// Extracted from: sites-mcp-server-01NSITE05 WP01.
package toolserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// ToolHandler is the Go-native shape of a single tool handler. The
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
	// argument is the canonical example.
	Redact []string
}

// PromptSpec declares one MCP prompt exposed via prompts/list and prompts/get.
type PromptSpec struct {
	Name        string
	Description string
	Arguments   []transport.PromptArgument
	// GetMessages returns the prompt messages for the given arguments map.
	GetMessages func(args map[string]string) ([]json.RawMessage, error)
}

// Server is the in-process MCP server. Tools and prompts are registered via
// Register/RegisterPrompt at boot; HandleEnvelope dispatches JSON-RPC envelopes
// against the registered tables.
type Server struct {
	name    string
	version string

	mu      sync.RWMutex
	tools   map[string]ToolSpec
	prompts map[string]PromptSpec
}

// NewServer returns an empty Server with the given server name and version,
// which are reported in the initialize response.
func NewServer(name, version string) *Server {
	return &Server{
		name:    name,
		version: version,
		tools:   make(map[string]ToolSpec),
		prompts: make(map[string]PromptSpec),
	}
}

// Register installs a tool. Re-registering the same name overwrites the
// previous handler (used by tests; production registers exactly once at
// boot).
func (s *Server) Register(spec ToolSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[spec.Name] = spec
}

// RegisterPrompt installs a prompt.
func (s *Server) RegisterPrompt(spec PromptSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts[spec.Name] = spec
}

// Tools returns a snapshot of every registered tool, sorted by name.
func (s *Server) Tools() []ToolSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolSpec, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	sortToolsByName(out)
	return out
}

// Prompts returns a snapshot of every registered prompt, sorted by name.
func (s *Server) Prompts() []PromptSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PromptSpec, 0, len(s.prompts))
	for _, p := range s.prompts {
		out = append(out, p)
	}
	sortPromptsByName(out)
	return out
}

// Lookup returns the tool spec for name, or false if unregistered.
func (s *Server) Lookup(name string) (ToolSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tools[name]
	return t, ok
}

// LookupPrompt returns the prompt spec for name, or false if unregistered.
func (s *Server) LookupPrompt(name string) (PromptSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.prompts[name]
	return p, ok
}

// HandleEnvelope is the JSON-RPC entrypoint. It accepts a parsed
// RequestEnvelope and returns either a result object (to be stuffed
// into ResponseEnvelope.Result by the transport) or an *RPCError.
//
// Implements: initialize, tools/list, tools/call, prompts/list, prompts/get.
// Other methods return MethodNotFound.
func (s *Server) HandleEnvelope(ctx context.Context, env transport.RequestEnvelope) (any, *transport.RPCError) {
	switch env.Method {
	case transport.MethodInitialize:
		return transport.InitializeResult{
			ProtocolVersion: transport.SupportedProtocolVersion,
			Capabilities: transport.ServerCapabilities{
				Tools:   &transport.ToolsCapability{},
				Prompts: &transport.PromptsCapability{},
			},
			ServerInfo: transport.Implementation{
				Name:    s.name,
				Version: s.version,
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
		if env.Params != nil {
			raw, ok := env.Params.(json.RawMessage)
			if !ok {
				return nil, &transport.RPCError{
					Code:    transport.ErrCodeInvalidParams,
					Message: fmt.Sprintf("%s: tools/call params must be a JSON object", s.name),
				}
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, &transport.RPCError{
						Code:    transport.ErrCodeInvalidParams,
						Message: fmt.Sprintf("%s: tools/call: %v", s.name, err),
					}
				}
			}
		}
		spec, ok := s.Lookup(p.Name)
		if !ok {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeMethodNotFound,
				Message: fmt.Sprintf("%s: unknown tool %q", s.name, p.Name),
			}
		}
		result, err := spec.Handler(ctx, p.Arguments)
		if err != nil {
			// Surface tool errors through MCP's isError convention rather
			// than as JSON-RPC errors so the caller's tool plumbing sees
			// them like any other failed tool result.
			text := json.RawMessage(JSONText(err.Error()))
			return transport.ToolsCallResult{
				Content: []json.RawMessage{text},
				IsError: true,
			}, nil
		}
		// Success: marshal the result into a single text content block.
		// Tools that need richer content can return a ToolsCallResult directly.
		if tc, ok := result.(transport.ToolsCallResult); ok {
			return tc, nil
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeInternalError,
				Message: fmt.Sprintf("%s: marshal result: %v", s.name, err),
			}
		}
		return transport.ToolsCallResult{
			Content: []json.RawMessage{json.RawMessage(JSONText(string(body)))},
		}, nil

	case transport.MethodPromptsList:
		specs := s.Prompts()
		out := transport.PromptsListResult{
			Prompts: make([]transport.PromptDefinition, 0, len(specs)),
		}
		for _, sp := range specs {
			out.Prompts = append(out.Prompts, transport.PromptDefinition{
				Name:        sp.Name,
				Description: sp.Description,
				Arguments:   sp.Arguments,
			})
		}
		return out, nil

	case transport.MethodPromptsGet:
		var p transport.PromptsGetParams
		if env.Params != nil {
			raw, ok := env.Params.(json.RawMessage)
			if ok && len(raw) > 0 {
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, &transport.RPCError{
						Code:    transport.ErrCodeInvalidParams,
						Message: fmt.Sprintf("%s: prompts/get: %v", s.name, err),
					}
				}
			}
		}
		spec, ok := s.LookupPrompt(p.Name)
		if !ok {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeMethodNotFound,
				Message: fmt.Sprintf("%s: unknown prompt %q", s.name, p.Name),
			}
		}
		msgs, err := spec.GetMessages(p.Arguments)
		if err != nil {
			return nil, &transport.RPCError{
				Code:    transport.ErrCodeInternalError,
				Message: fmt.Sprintf("%s: prompt %q: %v", s.name, p.Name, err),
			}
		}
		return transport.PromptsGetResult{
			Description: spec.Description,
			Messages:    msgs,
		}, nil

	case transport.MethodPing:
		return transport.PingResult{}, nil

	default:
		return nil, &transport.RPCError{
			Code:    transport.ErrCodeMethodNotFound,
			Message: fmt.Sprintf("%s: method %q not implemented", s.name, env.Method),
		}
	}
}

// JSONText wraps a string into the MCP {type:"text", text:...} content
// block, returning the marshalled bytes.
func JSONText(s string) []byte {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: s})
	return b
}

// sortToolsByName sorts a slice of ToolSpec in ascending Name order.
func sortToolsByName(ts []ToolSpec) {
	for i := 1; i < len(ts); i++ {
		j := i
		for j > 0 && ts[j-1].Name > ts[j].Name {
			ts[j-1], ts[j] = ts[j], ts[j-1]
			j--
		}
	}
}

// sortPromptsByName sorts a slice of PromptSpec in ascending Name order.
func sortPromptsByName(ps []PromptSpec) {
	for i := 1; i < len(ps); i++ {
		j := i
		for j > 0 && ps[j-1].Name > ps[j].Name {
			ps[j-1], ps[j] = ps[j], ps[j-1]
			j--
		}
	}
}
