package recorders

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// ── ActivityCatalog ───────────────────────────────────────────────────────────

// ResolveActivityCall records one call to ActivityCatalog.Resolve.
type ResolveActivityCall struct {
	ActivityID string
	Version    string
}

// ActivityCatalog is a recording fake that satisfies
// agentgraph.ActivityCatalog. Registered activity IDs are resolved to the
// configured graphs; unregistered IDs return an error.
type ActivityCatalog struct {
	mu         sync.Mutex
	calls      []ResolveActivityCall
	activities map[string]*agentgraph.Graph // key: activityID+"@"+version (version "" = any)
}

// RegisterActivity configures the catalog to return the given graph for
// the given activity ID and version (use "" to match any version).
func (r *ActivityCatalog) RegisterActivity(activityID, version string, g *agentgraph.Graph) {
	r.mu.Lock()
	if r.activities == nil {
		r.activities = make(map[string]*agentgraph.Graph)
	}
	r.activities[activityID+"@"+version] = g
	r.mu.Unlock()
}

// Resolve records the call and returns the configured graph.
func (r *ActivityCatalog) Resolve(activityID, version string) (*agentgraph.Graph, error) {
	r.mu.Lock()
	r.calls = append(r.calls, ResolveActivityCall{ActivityID: activityID, Version: version})
	g, ok := r.activities[activityID+"@"+version]
	if !ok {
		g, ok = r.activities[activityID+"@"]
	}
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("recorder: activity %q@%q not registered", activityID, version)
	}
	return g, nil
}

// Calls returns a snapshot of all recorded Resolve calls.
func (r *ActivityCatalog) Calls() []ResolveActivityCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ResolveActivityCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the number of Resolve calls recorded.
func (r *ActivityCatalog) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// RequireCalledWith asserts Resolve was called with the given activityID.
func (r *ActivityCatalog) RequireCalledWith(t *testing.T, activityID string) {
	t.Helper()
	for _, c := range r.Calls() {
		if c.ActivityID == activityID {
			return
		}
	}
	t.Fatalf("ActivityCatalog.Resolve: no call with activityID=%q", activityID)
}

// Reset clears recorded calls.
func (r *ActivityCatalog) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// ── CorpusBackend ─────────────────────────────────────────────────────────────

// SearchCall records one call to CorpusBackend.Search.
type SearchCall struct {
	Ctx    context.Context
	IDs    []string
	Query  string
	TopK   int
}

// EnqueueCall records one call to CorpusBackend.Enqueue.
type EnqueueCall struct {
	Ctx        context.Context
	CorpusID   string
	SourcePath string
}

// CorpusBackend is a recording fake that satisfies agentgraph.CorpusBackend
// (which combines Reader + Writer).
type CorpusBackend struct {
	mu       sync.Mutex
	searches []SearchCall
	enqueues []EnqueueCall
	// SearchResult is returned on every Search call.
	SearchResult []agentgraph.CorpusHit
	// SearchErr is returned on every Search call.
	SearchErr error
	// NextJobID is returned as the job ID on Enqueue.
	NextJobID string
	// EnqueueErr is returned on every Enqueue call.
	EnqueueErr error
}

// Search records the call and returns the configured result.
func (r *CorpusBackend) Search(ctx context.Context, ids []string, query string, topK int) ([]agentgraph.CorpusHit, error) {
	r.mu.Lock()
	r.searches = append(r.searches, SearchCall{Ctx: ctx, IDs: ids, Query: query, TopK: topK})
	hits := r.SearchResult
	err := r.SearchErr
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// Enqueue records the call and returns the configured job ID.
func (r *CorpusBackend) Enqueue(ctx context.Context, corpusID, sourcePath string) (string, error) {
	r.mu.Lock()
	r.enqueues = append(r.enqueues, EnqueueCall{Ctx: ctx, CorpusID: corpusID, SourcePath: sourcePath})
	nextJobID := r.NextJobID
	err := r.EnqueueErr
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if nextJobID == "" {
		nextJobID = "job-1"
	}
	return nextJobID, nil
}

// Searches returns a snapshot of all recorded Search calls.
func (r *CorpusBackend) Searches() []SearchCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SearchCall, len(r.searches))
	copy(out, r.searches)
	return out
}

// Enqueues returns a snapshot of all recorded Enqueue calls.
func (r *CorpusBackend) Enqueues() []EnqueueCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EnqueueCall, len(r.enqueues))
	copy(out, r.enqueues)
	return out
}

// RequireSearchCalledWith asserts at least one Search call matches the matcher.
func (r *CorpusBackend) RequireSearchCalledWith(t *testing.T, match func(SearchCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Searches() {
		if match(c) {
			return
		}
	}
	t.Fatalf("CorpusBackend.Search: no call found matching %q", desc)
}

// RequireEnqueueCalledWith asserts at least one Enqueue call matches the matcher.
func (r *CorpusBackend) RequireEnqueueCalledWith(t *testing.T, match func(EnqueueCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Enqueues() {
		if match(c) {
			return
		}
	}
	t.Fatalf("CorpusBackend.Enqueue: no call found matching %q", desc)
}

// Reset clears recorded calls.
func (r *CorpusBackend) Reset() {
	r.mu.Lock()
	r.searches = nil
	r.enqueues = nil
	r.mu.Unlock()
}
