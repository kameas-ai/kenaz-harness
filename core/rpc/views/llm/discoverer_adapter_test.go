package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// stubPool is a minimal mcp.Pool fake. The discoverer only reads
// Tools(); Open/Close/Call panic to make accidental use during
// discovery loud.
type stubPool struct {
	tools []mcp.Tool
	err   error
}

func (s *stubPool) Open(context.Context, []mcp.ServerSpec) error { return nil }
func (s *stubPool) Close(context.Context) error                  { return nil }
func (s *stubPool) Tools(context.Context) ([]mcp.Tool, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]mcp.Tool, len(s.tools))
	copy(out, s.tools)
	return out, nil
}
func (s *stubPool) Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	panic("stubPool.Call: not used during discovery")
}

// stubResolver returns deterministic verdicts for (server, tool) pairs.
// Anything unmapped resolves to auto_allow.
type stubResolver struct {
	policies map[string]toolloop.ToolPolicy
	err      error
}

func (r *stubResolver) Resolve(_ context.Context, _, server, tool string) (toolloop.Resolution, error) {
	if r.err != nil {
		return toolloop.Resolution{}, r.err
	}
	policy := toolloop.PolicyAutoAllow
	if r.policies != nil {
		if p, ok := r.policies[server+"::"+tool]; ok {
			policy = p
		}
	}
	return toolloop.Resolution{Server: server, Tool: tool, Policy: policy}, nil
}

func TestMCPToolDiscoverer_EmptyPool(t *testing.T) {
	d := NewMCPToolDiscoverer(&stubPool{}, nil)
	got, err := d.Tools(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(got))
	}
}

func TestMCPToolDiscoverer_NilPool(t *testing.T) {
	d := NewMCPToolDiscoverer(nil, nil)
	got, err := d.Tools(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil list, got %#v", got)
	}
}

func TestMCPToolDiscoverer_NamespacingAndPassthrough(t *testing.T) {
	pool := &stubPool{tools: []mcp.Tool{
		{
			Server:      "brave-search",
			Name:        "brave_web_search",
			Description: "Search the web",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		},
		{
			Server:      "filesystem",
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}}
	d := NewMCPToolDiscoverer(pool, nil)
	got, err := d.Tools(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[0].Name != "brave-search__brave_web_search" {
		t.Fatalf("entry[0] name = %q, want brave-search__brave_web_search", got[0].Name)
	}
	if got[0].Description != "Search the web" {
		t.Fatalf("entry[0] description = %q", got[0].Description)
	}
	if string(got[0].InputSchema) != `{"type":"object","properties":{"q":{"type":"string"}}}` {
		t.Fatalf("entry[0] input schema mismatch: %s", got[0].InputSchema)
	}
	if got[1].Name != "filesystem__read_file" {
		t.Fatalf("entry[1] name = %q", got[1].Name)
	}
}

func TestMCPToolDiscoverer_DenyFiltersTool(t *testing.T) {
	pool := &stubPool{tools: []mcp.Tool{
		{Server: "fs", Name: "read", Description: "r", InputSchema: json.RawMessage(`{}`)},
		{Server: "fs", Name: "delete", Description: "d", InputSchema: json.RawMessage(`{}`)},
		{Server: "search", Name: "web", Description: "w", InputSchema: json.RawMessage(`{}`)},
	}}
	resolver := &stubResolver{policies: map[string]toolloop.ToolPolicy{
		"fs::delete": toolloop.PolicyDeny,
	}}
	d := NewMCPToolDiscoverer(pool, resolver)
	got, err := d.Tools(context.Background(), "sess-deny")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tools after deny filter, got %d", len(got))
	}
	for _, sp := range got {
		if sp.Name == "fs__delete" {
			t.Fatalf("denied tool leaked into discovery list: %q", sp.Name)
		}
	}
}

func TestMCPToolDiscoverer_NilResolverNoFiltering(t *testing.T) {
	pool := &stubPool{tools: []mcp.Tool{
		{Server: "fs", Name: "read", InputSchema: json.RawMessage(`{}`)},
		{Server: "fs", Name: "delete", InputSchema: json.RawMessage(`{}`)},
	}}
	d := NewMCPToolDiscoverer(pool, nil)
	got, err := d.Tools(context.Background(), "sess-2")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tools (no filtering), got %d", len(got))
	}
}

func TestMCPToolDiscoverer_ResolverErrorDoesNotBlock(t *testing.T) {
	// The adapter treats a resolver error as "not denied" so a flaky
	// resolver does not silently strip every tool from the model's
	// catalog. The dispatch-time gate still enforces the verdict.
	pool := &stubPool{tools: []mcp.Tool{
		{Server: "fs", Name: "read", InputSchema: json.RawMessage(`{}`)},
	}}
	resolver := &stubResolver{err: errors.New("kaboom")}
	d := NewMCPToolDiscoverer(pool, resolver)
	got, err := d.Tools(context.Background(), "sess-3")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(got))
	}
}

func TestMCPToolDiscoverer_PoolErrorPropagates(t *testing.T) {
	pool := &stubPool{err: errors.New("pool unavailable")}
	d := NewMCPToolDiscoverer(pool, nil)
	_, err := d.Tools(context.Background(), "sess-4")
	if err == nil {
		t.Fatal("expected pool error to propagate")
	}
}
