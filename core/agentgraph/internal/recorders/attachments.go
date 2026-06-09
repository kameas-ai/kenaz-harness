package recorders

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// ResolveCall records one call to AttachmentResolver.Resolve.
type ResolveCall struct {
	Ctx          context.Context
	AttachmentID string
}

// RegisterCall records one call to AttachmentRegistrar.Register.
type RegisterCall struct {
	Ctx   context.Context
	Block agentgraph.AttachmentBlock
}

// AttachmentResolver is a recording fake that satisfies
// agentgraph.AttachmentResolver. Registered attachment IDs are resolved
// to the configured block; unregistered IDs return an error.
type AttachmentResolver struct {
	mu          sync.Mutex
	calls       []ResolveCall
	attachments map[string]agentgraph.AttachmentBlock
}

// Register configures the resolver to return the given block for the
// given attachment ID.
func (r *AttachmentResolver) Register(id string, block agentgraph.AttachmentBlock) {
	r.mu.Lock()
	if r.attachments == nil {
		r.attachments = make(map[string]agentgraph.AttachmentBlock)
	}
	r.attachments[id] = block
	r.mu.Unlock()
}

// Resolve records the call and returns the configured attachment block.
func (r *AttachmentResolver) Resolve(ctx context.Context, attachmentID string) (agentgraph.AttachmentBlock, error) {
	r.mu.Lock()
	r.calls = append(r.calls, ResolveCall{Ctx: ctx, AttachmentID: attachmentID})
	block, ok := r.attachments[attachmentID]
	r.mu.Unlock()
	if !ok {
		return agentgraph.AttachmentBlock{}, fmt.Errorf("recorder: attachment %q not registered", attachmentID)
	}
	return block, nil
}

// Calls returns a snapshot of all recorded Resolve calls.
func (r *AttachmentResolver) Calls() []ResolveCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ResolveCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Resolve calls recorded.
func (r *AttachmentResolver) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledWith asserts Resolve was called with the given attachment ID.
func (r *AttachmentResolver) RequireCalledWith(t *testing.T, attachmentID string) {
	t.Helper()
	for _, c := range r.Calls() {
		if c.AttachmentID == attachmentID {
			return
		}
	}
	t.Fatalf("AttachmentResolver.Resolve: no call with attachmentID=%q", attachmentID)
}

// Reset clears recorded calls.
func (r *AttachmentResolver) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// AttachmentRegistrar is a recording fake that satisfies
// agentgraph.AttachmentRegistrar.
type AttachmentRegistrar struct {
	mu     sync.Mutex
	calls  []RegisterCall
	// NextID is returned as the attachment ID on Register.
	NextID string
	// Err is returned on every Register call.
	Err    error
}

// Register records the call and returns the configured attachment ID.
func (r *AttachmentRegistrar) Register(ctx context.Context, block agentgraph.AttachmentBlock) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, RegisterCall{Ctx: ctx, Block: block})
	nextID := r.NextID
	err := r.Err
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if nextID == "" {
		nextID = "att-1"
	}
	return nextID, nil
}

// Calls returns a snapshot of all recorded Register calls.
func (r *AttachmentRegistrar) Calls() []RegisterCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RegisterCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Register calls recorded.
func (r *AttachmentRegistrar) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledOnce asserts exactly one Register call was recorded.
func (r *AttachmentRegistrar) RequireCalledOnce(t *testing.T) RegisterCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("AttachmentRegistrar.Register: want 1 call, got %d", len(calls))
	}
	return calls[0]
}

// Reset clears recorded calls.
func (r *AttachmentRegistrar) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}
