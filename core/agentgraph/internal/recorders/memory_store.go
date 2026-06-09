package recorders

import (
	"context"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// MemoryWrite records one call to MemoryStore.Write.
type MemoryWriteCall struct {
	Ctx context.Context
	W   agentgraph.MemoryWrite
}

// MemoryReadCall records one call to MemoryStore.Read.
type MemoryReadCall struct {
	Ctx    context.Context
	Filter agentgraph.MemoryReadFilter
}

// MemoryStore is a recording fake that satisfies agentgraph.MemoryStore.
type MemoryStore struct {
	mu         sync.Mutex
	writes     []MemoryWriteCall
	reads      []MemoryReadCall
	nextID     string
	WriteErr   error
	ReadResult []agentgraph.MemoryHit
	ReadErr    error
}

// Write records the invocation and returns the configured id.
func (r *MemoryStore) Write(ctx context.Context, w agentgraph.MemoryWrite) (id string, deduped bool, err error) {
	r.mu.Lock()
	r.writes = append(r.writes, MemoryWriteCall{Ctx: ctx, W: w})
	nextID := r.nextID
	writeErr := r.WriteErr
	r.mu.Unlock()
	if writeErr != nil {
		return "", false, writeErr
	}
	if nextID == "" {
		nextID = "mem-1"
	}
	return nextID, false, nil
}

// Read records the invocation and returns the configured result.
func (r *MemoryStore) Read(ctx context.Context, f agentgraph.MemoryReadFilter) ([]agentgraph.MemoryHit, error) {
	r.mu.Lock()
	r.reads = append(r.reads, MemoryReadCall{Ctx: ctx, Filter: f})
	hits := r.ReadResult
	readErr := r.ReadErr
	r.mu.Unlock()
	if readErr != nil {
		return nil, readErr
	}
	return hits, nil
}

// Writes returns a snapshot of all recorded Write calls.
func (r *MemoryStore) Writes() []MemoryWriteCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MemoryWriteCall, len(r.writes))
	copy(out, r.writes)
	return out
}

// Reads returns a snapshot of all recorded Read calls.
func (r *MemoryStore) Reads() []MemoryReadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MemoryReadCall, len(r.reads))
	copy(out, r.reads)
	return out
}

// WriteCount returns the number of Write calls recorded.
func (r *MemoryStore) WriteCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writes)
}

// ReadCount returns the number of Read calls recorded.
func (r *MemoryStore) ReadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reads)
}

// RequireWriteCalledWith asserts that Write was called with a write
// matching the given matcher.
func (r *MemoryStore) RequireWriteCalledWith(t *testing.T, match func(MemoryWriteCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Writes() {
		if match(c) {
			return
		}
	}
	t.Fatalf("MemoryStore.Write: no call found matching %q", desc)
}

// RequireWriteWithScope asserts that at least one Write was made with
// the given scope.
func (r *MemoryStore) RequireWriteWithScope(t *testing.T, scope string) {
	t.Helper()
	r.RequireWriteCalledWith(t, func(c MemoryWriteCall) bool {
		return c.W.Scope == scope
	}, "scope="+scope)
}

// RequireReadCalledWith asserts that Read was called with a filter
// matching the given matcher.
func (r *MemoryStore) RequireReadCalledWith(t *testing.T, match func(MemoryReadCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Reads() {
		if match(c) {
			return
		}
	}
	t.Fatalf("MemoryStore.Read: no call found matching %q", desc)
}

// SetNextID configures the ID returned by Write.
func (r *MemoryStore) SetNextID(id string) {
	r.mu.Lock()
	r.nextID = id
	r.mu.Unlock()
}

// Reset clears recorded calls.
func (r *MemoryStore) Reset() {
	r.mu.Lock()
	r.writes = nil
	r.reads = nil
	r.mu.Unlock()
}
