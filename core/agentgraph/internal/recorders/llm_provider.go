// Package recorders provides recording fake implementations for every
// interface in core/agentgraph/seams.go. Each recorder captures full
// call parameters with typed assertion helpers so seam-fanout tests can
// verify the kernel calls the right methods with the right values.
//
// All recorders are goroutine-safe (sync.Mutex around the call log) so
// they can be shared across parallel kernel execution paths.
package recorders

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// LLMCall records one call to LLMProvider.Generate.
type LLMCall struct {
	Ctx context.Context
	Req agentgraph.LLMRequest
}

// LLMProvider is a recording fake that satisfies agentgraph.LLMProvider.
// It returns the configured resp and err on every call, and records every
// invocation so tests can assert the parameters the kernel passed.
type LLMProvider struct {
	mu    sync.Mutex
	calls []LLMCall
	// Resp is returned on every Generate call.
	Resp agentgraph.LLMResponse
	// Err is returned on every Generate call. Overrides Resp when non-nil.
	Err error
	// RespFn, when non-nil, is called instead of Resp/Err to produce the
	// response. Useful for call-index-dependent behaviour.
	RespFn func(req agentgraph.LLMRequest) (agentgraph.LLMResponse, error)
}

// Generate satisfies agentgraph.LLMProvider. Records the call and returns
// the configured response.
func (r *LLMProvider) Generate(ctx context.Context, req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	r.mu.Lock()
	r.calls = append(r.calls, LLMCall{Ctx: ctx, Req: req})
	fn := r.RespFn
	resp := r.Resp
	err := r.Err
	r.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return resp, err
}

// Calls returns a snapshot of all recorded calls.
func (r *LLMProvider) Calls() []LLMCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LLMCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Generate calls recorded so far.
func (r *LLMProvider) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledOnce asserts that exactly one Generate call was recorded
// and returns it.
func (r *LLMProvider) RequireCalledOnce(t *testing.T) LLMCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("LLMProvider.Generate: want 1 call, got %d", len(calls))
	}
	return calls[0]
}

// RequireCalledWith asserts that Generate was called at least once with
// a request matching the given matcher function.
func (r *LLMProvider) RequireCalledWith(t *testing.T, match func(LLMCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("LLMProvider.Generate: no call found matching %q", desc)
}

// RequireProvider asserts that every Generate call used the given provider.
func (r *LLMProvider) RequireProvider(t *testing.T, provider string) {
	t.Helper()
	for i, c := range r.Calls() {
		if c.Req.Provider != provider {
			t.Errorf("LLMProvider.Generate call[%d]: Provider = %q, want %q", i, c.Req.Provider, provider)
		}
	}
}

// RequireModel asserts that every Generate call used the given model.
func (r *LLMProvider) RequireModel(t *testing.T, model string) {
	t.Helper()
	for i, c := range r.Calls() {
		if c.Req.Model != model {
			t.Errorf("LLMProvider.Generate call[%d]: Model = %q, want %q", i, c.Req.Model, model)
		}
	}
}

// RequireMessageCount asserts that the most recent Generate call had
// exactly n messages.
func (r *LLMProvider) RequireMessageCount(t *testing.T, n int) {
	t.Helper()
	calls := r.Calls()
	if len(calls) == 0 {
		t.Fatal("LLMProvider.Generate: no calls recorded")
	}
	last := calls[len(calls)-1]
	if len(last.Req.Messages) != n {
		t.Errorf("LLMProvider.Generate last call: Messages count = %d, want %d", len(last.Req.Messages), n)
	}
}

// RequireSystemPrompt asserts that the most recent Generate call had the
// given system prompt.
func (r *LLMProvider) RequireSystemPrompt(t *testing.T, want string) {
	t.Helper()
	calls := r.Calls()
	if len(calls) == 0 {
		t.Fatal("LLMProvider.Generate: no calls recorded")
	}
	last := calls[len(calls)-1]
	if last.Req.SystemPrompt != want {
		t.Errorf("LLMProvider.Generate last call: SystemPrompt = %q, want %q", last.Req.SystemPrompt, want)
	}
}

// RequireToolInAllowList asserts that the most recent Generate call's tool
// allow-list contains the given tool name.
func (r *LLMProvider) RequireToolInAllowList(t *testing.T, toolName string) {
	t.Helper()
	calls := r.Calls()
	if len(calls) == 0 {
		t.Fatal("LLMProvider.Generate: no calls recorded")
	}
	last := calls[len(calls)-1]
	for _, tool := range last.Req.Tools {
		if tool == toolName {
			return
		}
	}
	t.Errorf("LLMProvider.Generate last call: tool %q not in allow-list %v", toolName, last.Req.Tools)
}

// Reset clears the recorded calls.
func (r *LLMProvider) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// Sprintf produces a human-readable summary of one LLMCall for diff output.
func (c LLMCall) Sprintf() string {
	return fmt.Sprintf("LLMCall{Provider:%q, Model:%q, SystemPrompt:%q, Messages:%d, Tools:%v}",
		c.Req.Provider, c.Req.Model, c.Req.SystemPrompt, len(c.Req.Messages), c.Req.Tools)
}
