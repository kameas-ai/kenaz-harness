package toolloop

import (
	"context"
	"encoding/json"
	"testing"
)

// stubBuiltin is a minimal BuiltinTool the tests use to assert the
// registry + pool wire correctly without spinning up the real
// websearch / bash tools (those have their own test packages).
type stubBuiltin struct {
	name string
	desc string
	calls int
}

func (s *stubBuiltin) Name() string                  { return s.name }
func (s *stubBuiltin) Description() string           { return s.desc }
func (s *stubBuiltin) InputSchema() json.RawMessage  { return json.RawMessage(`{}`) }
func (s *stubBuiltin) Call(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	s.calls++
	return args, nil
}

// stubMCPPool is a MCPPool that records calls for assertion.
type stubMCPPool struct {
	tools []Tool
	calls []string
}

func (p *stubMCPPool) Tools(_ context.Context) ([]Tool, error) {
	return p.tools, nil
}
func (p *stubMCPPool) Call(_ context.Context, server, tool string, _ json.RawMessage) (json.RawMessage, error) {
	p.calls = append(p.calls, server+"/"+tool)
	return json.RawMessage(`{}`), nil
}

func TestBuiltinRegistry_RegisterLookup(t *testing.T) {
	t.Parallel()
	r := NewBuiltinRegistry()
	if !r.Empty() {
		t.Fatal("fresh registry should be empty")
	}
	r.Register(&stubBuiltin{name: "kenaz__web_search"})
	if r.Empty() {
		t.Fatal("registry should not be empty after Register")
	}
	got, ok := r.Lookup("kenaz__web_search")
	if !ok || got == nil {
		t.Fatalf("Lookup ok=%v got=%v", ok, got)
	}
	if got.Name() != "kenaz__web_search" {
		t.Errorf("Name = %q", got.Name())
	}
}

func TestBuiltinRegistry_Unregister(t *testing.T) {
	t.Parallel()
	r := NewBuiltinRegistry()
	r.Register(&stubBuiltin{name: "kenaz__bash"})
	r.Unregister("kenaz__bash")
	if !r.Empty() {
		t.Fatal("Unregister did not clear the registry")
	}
}

func TestEnabledFilter_GatesByPredicate(t *testing.T) {
	t.Parallel()
	r := NewBuiltinRegistry()
	r.Register(&stubBuiltin{name: "kenaz__bash"})
	r.Register(&stubBuiltin{name: "kenaz__web_search"})
	enabled := func(name string) bool {
		return name == "kenaz__bash"
	}
	f := NewEnabledFilter(r, enabled)
	if got := f.Names(); len(got) != 1 || got[0] != "kenaz__bash" {
		t.Errorf("Names = %v", got)
	}
	if _, ok := f.Lookup("kenaz__web_search"); ok {
		t.Error("disabled tool was returned by Lookup")
	}
	if _, ok := f.Lookup("kenaz__bash"); !ok {
		t.Error("enabled tool was hidden by Lookup")
	}
}

func TestBuiltinPool_TopologyAndCallDispatch(t *testing.T) {
	t.Parallel()
	mcp := &stubMCPPool{tools: []Tool{{Server: "real-server", Name: "fetch"}}}
	registry := NewBuiltinRegistry()
	bash := &stubBuiltin{name: "kenaz__bash"}
	registry.Register(bash)

	pool := NewBuiltinPool(mcp, registry)
	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	// Expect MCP tool first, builtin second; builtin name is stripped
	// of the "kenaz__" prefix to mirror what the loop's split path
	// produces.
	if len(tools) != 2 {
		t.Fatalf("Tools count = %d want 2 (got %v)", len(tools), tools)
	}
	if tools[0].Server != "real-server" || tools[0].Name != "fetch" {
		t.Errorf("Tools[0] = %+v want real-server/fetch", tools[0])
	}
	if tools[1].Server != BuiltinServerName || tools[1].Name != "bash" {
		t.Errorf("Tools[1] = %+v want kenaz/bash", tools[1])
	}

	// Dispatch a built-in: server="kenaz", tool="bash" — the pool
	// re-prefixes "bash" to "kenaz__bash" for registry lookup.
	if _, err := pool.Call(context.Background(), BuiltinServerName, "bash", json.RawMessage(`{"command":"ls"}`)); err != nil {
		t.Fatalf("Call builtin: %v", err)
	}
	if bash.calls != 1 {
		t.Errorf("bash.calls = %d want 1", bash.calls)
	}
	// MCP wasn't touched.
	if len(mcp.calls) != 0 {
		t.Errorf("mcp.calls = %v want empty", mcp.calls)
	}

	// Dispatch an MCP tool: forwards to the inner pool unchanged.
	if _, err := pool.Call(context.Background(), "real-server", "fetch", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Call mcp: %v", err)
	}
	if len(mcp.calls) != 1 || mcp.calls[0] != "real-server/fetch" {
		t.Errorf("mcp.calls = %v want [real-server/fetch]", mcp.calls)
	}
}

func TestBuiltinPool_MissingBuiltinFallsThrough(t *testing.T) {
	t.Parallel()
	mcp := &stubMCPPool{tools: []Tool{{Server: "x", Name: "y"}}}
	registry := NewBuiltinRegistry()
	pool := NewBuiltinPool(mcp, registry)
	// "kenaz/missing" isn't in the registry; pool falls through to MCP.
	if _, err := pool.Call(context.Background(), BuiltinServerName, "missing", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(mcp.calls) != 1 {
		t.Errorf("mcp.calls = %v", mcp.calls)
	}
}

func TestBuiltinPool_NilRegistryPassthrough(t *testing.T) {
	t.Parallel()
	mcp := &stubMCPPool{tools: []Tool{{Server: "x", Name: "y"}}}
	pool := NewBuiltinPool(mcp, nil)
	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("Tools count = %d", len(tools))
	}
	if _, err := pool.Call(context.Background(), "x", "y", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
}
