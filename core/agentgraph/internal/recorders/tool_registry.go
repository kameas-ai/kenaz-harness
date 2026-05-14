package recorders

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// ToolCall records one call to ToolRegistry.Call.
type ToolCall struct {
	Ctx  context.Context
	Call agentgraph.ToolCall
}

// ToolRegistry is a recording fake that satisfies agentgraph.ToolRegistry.
// Registered tool names are consulted for Has(); Call is recorded and
// returns the configured response.
type ToolRegistry struct {
	mu        sync.Mutex
	registered map[string]toolEntry
	calls      []ToolCall
}

type toolEntry struct {
	result agentgraph.ToolResult
	err    error
}

// Register adds a tool name that Has() will return true for, with the
// given result to return on Call().
func (r *ToolRegistry) Register(name string, result agentgraph.ToolResult, err error) {
	r.mu.Lock()
	if r.registered == nil {
		r.registered = make(map[string]toolEntry)
	}
	r.registered[name] = toolEntry{result: result, err: err}
	r.mu.Unlock()
}

// Has reports whether the given tool was registered.
func (r *ToolRegistry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.registered[name]
	return ok
}

// Call records the invocation and returns the configured result for the
// tool. Returns an error for unregistered tools.
func (r *ToolRegistry) Call(ctx context.Context, call agentgraph.ToolCall) (agentgraph.ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, ToolCall{Ctx: ctx, Call: call})
	entry, ok := r.registered[call.Name]
	r.mu.Unlock()
	if !ok {
		return agentgraph.ToolResult{}, fmt.Errorf("recorder: tool %q not registered", call.Name)
	}
	return entry.result, entry.err
}

// Calls returns a snapshot of all recorded Call invocations.
func (r *ToolRegistry) Calls() []ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ToolCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Call invocations recorded so far.
func (r *ToolRegistry) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledWith asserts that Call was invoked at least once with a
// call matching the given matcher function.
func (r *ToolRegistry) RequireCalledWith(t *testing.T, match func(ToolCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("ToolRegistry.Call: no call found matching %q", desc)
}

// RequireCalledWithTool asserts that a Call with the given tool name was
// recorded.
func (r *ToolRegistry) RequireCalledWithTool(t *testing.T, name string) {
	t.Helper()
	r.RequireCalledWith(t, func(c ToolCall) bool {
		return c.Call.Name == name
	}, "name="+name)
}

// RequireNotCalled asserts that Call was never invoked.
func (r *ToolRegistry) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("ToolRegistry.Call: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls (registered tools are preserved).
func (r *ToolRegistry) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
