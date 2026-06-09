package recorders

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// BashGetCall records one call to BashOutputStore.Get.
type BashGetCall struct {
	Ctx   context.Context
	RunID string
}

// BashOutputStore is a recording fake that satisfies
// agentgraph.BashOutputStore. Registered run IDs return the configured
// output; unregistered IDs return an error.
type BashOutputStore struct {
	mu      sync.Mutex
	calls   []BashGetCall
	outputs map[string]agentgraph.BashOutput
	// Err is returned for any Get call when set (overrides per-run outputs).
	Err error
}

// RegisterOutput configures the store to return the given output for the
// given run ID.
func (r *BashOutputStore) RegisterOutput(runID string, out agentgraph.BashOutput) {
	r.mu.Lock()
	if r.outputs == nil {
		r.outputs = make(map[string]agentgraph.BashOutput)
	}
	r.outputs[runID] = out
	r.mu.Unlock()
}

// Get records the call and returns the configured output.
func (r *BashOutputStore) Get(ctx context.Context, runID string) (agentgraph.BashOutput, error) {
	r.mu.Lock()
	r.calls = append(r.calls, BashGetCall{Ctx: ctx, RunID: runID})
	globalErr := r.Err
	out, ok := r.outputs[runID]
	r.mu.Unlock()
	if globalErr != nil {
		return agentgraph.BashOutput{}, globalErr
	}
	if !ok {
		return agentgraph.BashOutput{}, fmt.Errorf("recorder: bash run %q not registered", runID)
	}
	return out, nil
}

// Calls returns a snapshot of all recorded Get calls.
func (r *BashOutputStore) Calls() []BashGetCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]BashGetCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Get calls recorded.
func (r *BashOutputStore) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledWithRunID asserts at least one Get call used the given run ID.
func (r *BashOutputStore) RequireCalledWithRunID(t *testing.T, runID string) {
	t.Helper()
	for _, c := range r.Calls() {
		if c.RunID == runID {
			return
		}
	}
	t.Fatalf("BashOutputStore.Get: no call with runID=%q", runID)
}

// RequireNotCalled asserts Get was never invoked.
func (r *BashOutputStore) RequireNotCalled(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("BashOutputStore.Get: want 0 calls, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *BashOutputStore) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
