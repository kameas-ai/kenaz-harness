package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// InProcessConnection is the minimal transport surface a Go-native,
// in-process MCP server must satisfy to be routed through the dispatch
// Pool's "inprocess" transport arm. *harness.Transport
// (core/mcp/builtin/harness/transport.go) is the first production
// implementation. It is a synchronous, single request/response pair per
// Send/Recv call — the same contract stdio and http already honour —
// so no notification or server-initiated-request support is required
// here (transport.go:17-20 documents that limit on the harness-self
// side; this interface just doesn't ask for more than that).
type InProcessConnection interface {
	Send(ctx context.Context, env transport.RequestEnvelope) error
	Recv(ctx context.Context) (transport.ResponseEnvelope, error)
	Close() error
}

// ErrInProcessServerExists is returned by RegisterServer when a server
// with that name is already registered — mirrors stdio.ErrServerExists
// so callers that already switch on the stdio sentinel have a matching
// shape to add for this arm.
var ErrInProcessServerExists = errors.New("dispatch: inprocess server already registered")

// ErrInProcessServerNotFound is returned by CloseOne / Call when no
// server with that name is registered — mirrors stdio.ErrServerNotFound.
var ErrInProcessServerNotFound = errors.New("dispatch: inprocess server not registered")

// InProcessSubPool routes Open/Tools/Call/Close to Go-native,
// in-process MCP servers that are registered directly by name via
// RegisterServer. Unlike the stdio/http/sse sub-pools, there is no
// ServerSpec.Command/URL to spawn a connection from — the server
// object already lives in this process — so the spec-driven Open /
// OpenOne surface is explicitly rejected (see Open below) rather than
// silently doing nothing. That is a deliberate choice, not an
// oversight: silently inheriting stdio/http/sse's "Open spawns from a
// spec" behaviour here would hide a caller's mistake instead of
// surfacing it.
type InProcessSubPool struct {
	mu        sync.RWMutex
	conns     map[string]InProcessConnection
	idCounter atomic.Int64
}

// NewInProcessSubPool returns an empty in-process sub-pool.
func NewInProcessSubPool() *InProcessSubPool {
	return &InProcessSubPool{conns: make(map[string]InProcessConnection)}
}

// Compile-time witness: *InProcessSubPool satisfies coremcp.Pool.
var _ coremcp.Pool = (*InProcessSubPool)(nil)

// RegisterServer adds a named in-process connection. Returns
// ErrInProcessServerExists if a server with that name is already
// registered — the caller must CloseOne first to replace it.
func (p *InProcessSubPool) RegisterServer(_ context.Context, name string, conn InProcessConnection) error {
	if name == "" {
		return errors.New("dispatch: inprocess server name required")
	}
	if conn == nil {
		return errors.New("dispatch: inprocess connection required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.conns[name]; exists {
		return fmt.Errorf("%w: %q", ErrInProcessServerExists, name)
	}
	p.conns[name] = conn
	return nil
}

// Open is not meaningful for the in-process transport — there is no
// ServerSpec.Command/URL to spawn a connection from. It fails loudly
// naming every rejected spec rather than silently accepting and doing
// nothing, so a caller that accidentally routes a stdio/http/sse spec
// through this arm sees the mistake immediately. Use RegisterServer.
func (p *InProcessSubPool) Open(_ context.Context, specs []coremcp.ServerSpec) error {
	if len(specs) == 0 {
		return nil
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	return fmt.Errorf("dispatch: inprocess transport does not support spec-driven Open; use RegisterServer (rejected: %v)", names)
}

// Close releases every registered connection. Returns the first
// non-nil Close error, if any, after attempting all of them.
func (p *InProcessSubPool) Close(_ context.Context) error {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]InProcessConnection)
	p.mu.Unlock()

	var first error
	for _, c := range conns {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// CloseOne removes and closes a single registered server. Returns
// ErrInProcessServerNotFound if id is not registered.
func (p *InProcessSubPool) CloseOne(_ context.Context, id string) error {
	p.mu.Lock()
	conn, ok := p.conns[id]
	if ok {
		delete(p.conns, id)
	}
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrInProcessServerNotFound, id)
	}
	return conn.Close()
}

// Tools fans tools/list out to every registered connection. A single
// dead connection's error is swallowed (matching http.Pool.Tools and
// dispatch.Pool.Tools) so it doesn't hide tools from the others.
func (p *InProcessSubPool) Tools(ctx context.Context) ([]coremcp.Tool, error) {
	p.mu.RLock()
	entries := make(map[string]InProcessConnection, len(p.conns))
	for k, v := range p.conns {
		entries[k] = v
	}
	p.mu.RUnlock()

	out := make([]coremcp.Tool, 0)
	for name, conn := range entries {
		tools, err := p.listTools(ctx, name, conn)
		if err != nil {
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

func (p *InProcessSubPool) listTools(ctx context.Context, name string, conn InProcessConnection) ([]coremcp.Tool, error) {
	id := p.idCounter.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsList,
	}
	if err := conn.Send(ctx, req); err != nil {
		return nil, err
	}
	msg, err := conn.Recv(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	result, err := decodeToolsList(msg.Result)
	if err != nil {
		return nil, err
	}
	out := make([]coremcp.Tool, 0, len(result.Tools))
	for _, td := range result.Tools {
		out = append(out, coremcp.Tool{
			Server:      name,
			Name:        td.Name,
			Description: td.Description,
			InputSchema: td.InputSchema,
		})
	}
	return out, nil
}

// Call dispatches tools/call against the named in-process server.
func (p *InProcessSubPool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.RLock()
	conn, ok := p.conns[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInProcessServerNotFound, server)
	}

	id := p.idCounter.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsCall,
		Params: transport.ToolsCallParams{
			Name:      tool,
			Arguments: args,
		},
	}
	if err := conn.Send(ctx, req); err != nil {
		return nil, err
	}
	msg, err := conn.Recv(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	b, err := json.Marshal(msg.Result)
	if err != nil {
		return nil, fmt.Errorf("dispatch: marshal inprocess tools/call result: %w", err)
	}
	return json.RawMessage(b), nil
}

// decodeToolsList projects a ResponseEnvelope.Result onto
// transport.ToolsListResult. In-process servers (harness.Transport)
// hand back the concrete Go value directly — no wire round-trip — so
// the fast path is a type assertion; a json.Marshal/Unmarshal
// round-trip is the fallback for any InProcessConnection
// implementation that instead hands back raw bytes or a map, so this
// sub-pool isn't coupled to harness.Transport's exact internal
// representation.
func decodeToolsList(result any) (transport.ToolsListResult, error) {
	if r, ok := result.(transport.ToolsListResult); ok {
		return r, nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return transport.ToolsListResult{}, fmt.Errorf("dispatch: marshal inprocess tools/list result: %w", err)
	}
	var out transport.ToolsListResult
	if err := json.Unmarshal(b, &out); err != nil {
		return transport.ToolsListResult{}, fmt.Errorf("dispatch: decode inprocess tools/list result: %w", err)
	}
	return out, nil
}
