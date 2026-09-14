// hooks_mcp_invoker_test.go — finding #71 end-to-end wiring proof.
//
// core/hooks/mcp_dispatch_test.go proves hooks.Runner dispatches
// kind=mcp hooks to whatever MCPInvoker Config.MCP holds — but that is
// a core/hooks-package unit test; it says nothing about whether
// core/rpc's production constructor (newHooksStack) actually sets
// Config.MCP to a real invoker. That plumbing bug is exactly what
// finding #71 was: the runner-level dispatch code was already
// correct, Config.MCP was simply never populated at the one production
// call site. This file tests THAT seam: newHooksStack +
// mcpHookInvokerAdapter's backfill pattern (constructed empty, then
// setPool'd once the MCP pool exists — see hooks_mcp_invoker.go and
// the construction-ordering comment on newHooksStack) using a fake
// coremcp.Pool, exactly mirroring how api.New really wires it.
package rpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/hooks"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

// fakeHookMCPPool is a minimal coremcp.Pool fake recording every Call.
// Guarded by mu: Call runs on hooks.Runner's async worker pool
// goroutine (RunPostSend), the test body reads via snapshot() from the
// main goroutine — the exact cross-goroutine shape CLAUDE.md's
// "Race-safe test fakes" section requires a mutex + snapshot for.
type fakeHookMCPPool struct {
	mu    sync.Mutex
	calls []struct {
		server, tool string
		args         string
	}
	result json.RawMessage
	err    error
}

func (p *fakeHookMCPPool) Open(context.Context, []coremcp.ServerSpec) error { return nil }
func (p *fakeHookMCPPool) Close(context.Context) error                      { return nil }
func (p *fakeHookMCPPool) Tools(context.Context) ([]coremcp.Tool, error)    { return nil, nil }
func (p *fakeHookMCPPool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.calls = append(p.calls, struct {
		server, tool string
		args         string
	}{server: server, tool: tool, args: string(args)})
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return p.result, nil
}

func (p *fakeHookMCPPool) snapshot() []struct {
	server, tool string
	args         string
} {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]struct {
		server, tool string
		args         string
	}, len(p.calls))
	copy(out, p.calls)
	return out
}

var _ coremcp.Pool = (*fakeHookMCPPool)(nil)

// TestNewHooksStack_KindMCPHook_DispatchesThroughRealWiring reconstructs
// the exact production sequence api.New follows: build the invoker
// adapter empty, hand it to newHooksStack (so Config.MCP is set before
// the pool exists), then backfill the adapter's pool once it's
// available — and proves a kind=mcp post_send hook added to the
// registry newHooksStack returns actually reaches the fake pool's
// Call with the split (server, tool) pair.
//
// This is the test that would have caught finding #71: it fails if
// newHooksStack stops threading its mcpInvoker parameter into
// hooks.Config.MCP (see the mutation run below/report), which is
// exactly the bug that shipped — Config.MCP was simply never set.
func TestNewHooksStack_KindMCPHook_DispatchesThroughRealWiring(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "memory.gob")
	memStore, err := corememory.NewChromemStore(storePath)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = memStore.Close() })

	// Mirrors api.New: construct the adapter empty, pass it into
	// newHooksStack (the pool doesn't exist yet at this point in real
	// boot), backfill afterward.
	invokerAdapter := &mcpHookInvokerAdapter{}
	hooksRunner, hookRegistry, _, hookRunnerImpl := newHooksStack(nil, nil, memStore, nil, invokerAdapter)
	if hooksRunner == nil || hookRegistry == nil || hookRunnerImpl == nil {
		t.Fatalf("newHooksStack returned nil with a non-nil memStore: runner=%v registry=%v impl=%v",
			hooksRunner, hookRegistry, hookRunnerImpl)
	}

	pool := &fakeHookMCPPool{result: json.RawMessage(`{}`)}
	invokerAdapter.setPool(pool) // the backfill step, run after "newLLMStack" in production

	if err := hookRegistry.Add(hooks.Hook{
		ID: "mcp-hook-1", Name: "test mcp hook", Event: hooks.EventPostSend, Kind: hooks.KindMCP,
		Enabled: true, MCPTool: "notion__create_page",
	}); err != nil {
		t.Fatalf("hookRegistry.Add: %v", err)
	}

	hookRunnerImpl.RunPostSend(context.Background(), hooks.PostSendEvent{
		SessionID: "sess-wiring", UserTurn: "u", AssistantTurn: "a", FinishReason: "stop",
	})
	hookRunnerImpl.Shutdown() // drains the async pool — safe to read pool.snapshot() after this returns

	calls := pool.snapshot()
	if len(calls) != 1 {
		t.Fatalf("fake pool Call count = %d, want 1 (calls=%+v)", len(calls), calls)
	}
	if calls[0].server != "notion" || calls[0].tool != "create_page" {
		t.Errorf("(server, tool) = (%q, %q), want (\"notion\", \"create_page\") — splitToolName's \"__\" convention", calls[0].server, calls[0].tool)
	}
}

// TestMCPHookInvokerAdapter_NoPool_FailsHonestly pins the pre-backfill
// (and pre-fix, before finding #71 was closed) shape: an invoker with
// no pool configured must return a clear error rather than a nil
// dereference or a silent no-op.
func TestMCPHookInvokerAdapter_NoPool_FailsHonestly(t *testing.T) {
	t.Parallel()
	a := &mcpHookInvokerAdapter{}
	_, err := a.InvokeTool(context.Background(), "notion__create_page", []byte(`{}`))
	if err == nil {
		t.Fatal("InvokeTool with no pool configured: want an error, got nil")
	}
}
