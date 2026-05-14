package recorders

import (
	"context"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// CompactCall records one call to Compactor.Compact.
type CompactCall struct {
	Ctx context.Context
	In  agentgraph.CompactionInput
}

// Compactor is a recording fake that satisfies agentgraph.Compactor.
// It records every Compact call and returns the configured output.
type Compactor struct {
	mu    sync.Mutex
	calls []CompactCall
	// TransformFn, when non-nil, is called to produce the output for each
	// Compact call. If nil, messages are returned unchanged (Skipped=false).
	TransformFn func(in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error)
	// Err, when non-nil, is returned on every Compact call.
	Err error
}

// Compact records the call and returns the configured output.
func (r *Compactor) Compact(ctx context.Context, in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
	r.mu.Lock()
	r.calls = append(r.calls, CompactCall{Ctx: ctx, In: in})
	fn := r.TransformFn
	err := r.Err
	r.mu.Unlock()
	if err != nil {
		return agentgraph.CompactionOutput{}, err
	}
	if fn != nil {
		return fn(in)
	}
	return agentgraph.CompactionOutput{Messages: in.Messages}, nil
}

// Calls returns a snapshot of all recorded Compact calls.
func (r *Compactor) Calls() []CompactCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CompactCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Compact calls recorded.
func (r *Compactor) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledOnce asserts exactly one Compact call was recorded.
func (r *Compactor) RequireCalledOnce(t *testing.T) CompactCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("Compactor.Compact: want 1 call, got %d", len(calls))
	}
	return calls[0]
}

// RequireCalledWith asserts at least one Compact call matches the given matcher.
func (r *Compactor) RequireCalledWith(t *testing.T, match func(CompactCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("Compactor.Compact: no call found matching %q", desc)
}

// RequireCalledAtSite asserts at least one Compact call was fired at the
// given compaction site.
func (r *Compactor) RequireCalledAtSite(t *testing.T, site agentgraph.CompactionSite) {
	t.Helper()
	r.RequireCalledWith(t, func(c CompactCall) bool {
		return c.In.Site == site
	}, "site="+string(site))
}

// RequireNotCalled asserts Compact was never invoked.
func (r *Compactor) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("Compactor.Compact: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *Compactor) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
