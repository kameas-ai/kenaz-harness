// Package fixture provides an in-memory mcp.Pool implementation for
// tests and the toolloop scaffolding to consume until C1 (real MCP
// server lifecycle) lands. Production builds wire the toolloop against
// a concrete pool from core/mcp; the fixture is a stand-in with
// pre-registered (server, tool) → result mappings.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/mcp"
)

// Handler is a registered (server, tool) → response function. Returning
// an error surfaces as a Call failure to the caller.
type Handler func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// Pool is an in-memory mcp.Pool. Calls to unregistered (server, tool)
// pairs return an error that the toolloop surfaces as a synthetic tool
// failure.
type Pool struct {
	mu       sync.Mutex
	handlers map[string]Handler  // key: "server\x00tool"
	calls    []CallRecord
	tools    []mcp.Tool
}

// CallRecord captures one Call invocation for assertions in tests.
type CallRecord struct {
	Server string
	Tool   string
	Args   json.RawMessage
}

// New returns an empty Pool.
func New() *Pool {
	return &Pool{
		handlers: map[string]Handler{},
	}
}

// Register installs a handler for the (server, tool) pair. Last
// registration wins.
func (p *Pool) Register(server, tool string, h Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[key(server, tool)] = h
	p.tools = append(p.tools, mcp.Tool{Server: server, Name: tool})
}

// Calls returns a snapshot of every Call made against the pool. Tests
// assert against this to verify the toolloop dispatched correctly.
func (p *Pool) Calls() []CallRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]CallRecord, len(p.calls))
	copy(out, p.calls)
	return out
}

// Open is a no-op — there is no real transport to negotiate.
func (p *Pool) Open(_ context.Context, _ []mcp.ServerSpec) error { return nil }

// Close is a no-op.
func (p *Pool) Close(_ context.Context) error { return nil }

// Tools returns the list of registered tools so callers can probe what
// is wired without re-reading the registration map.
func (p *Pool) Tools(_ context.Context) ([]mcp.Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]mcp.Tool, len(p.tools))
	copy(out, p.tools)
	return out, nil
}

// Call dispatches to the handler registered for (server, tool). The
// args slice is recorded verbatim for inspection via Calls().
func (p *Pool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	h, ok := p.handlers[key(server, tool)]
	// Defensive copy — args buffers can be reused upstream and we want
	// a stable record.
	rec := CallRecord{Server: server, Tool: tool}
	if args != nil {
		rec.Args = append(json.RawMessage(nil), args...)
	}
	p.calls = append(p.calls, rec)
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mcp/fixture: no handler for %q.%q", server, tool)
	}
	return h(ctx, args)
}

func key(server, tool string) string { return server + "\x00" + tool }

// Compile-time witness that *Pool satisfies mcp.Pool.
var _ mcp.Pool = (*Pool)(nil)
