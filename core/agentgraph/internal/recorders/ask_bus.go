package recorders

import (
	"context"
	"sync"
	"testing"
)

// PendingCall records one call to AskBus.Pending.
type PendingCall struct {
	Ctx      context.Context
	RunID    string
	NodeID   string
	Question string
}

// LookupCall records one call to AskBus.LookupAnswer.
type LookupCall struct {
	Ctx    context.Context
	RunID  string
	NodeID string
}

// AskBus is a recording fake that satisfies agentgraph.AskBus.
// Answers can be pre-loaded via SetAnswer so LookupAnswer returns them.
type AskBus struct {
	mu       sync.Mutex
	pending  []PendingCall
	lookups  []LookupCall
	answers  map[string]string // key: runID+":"+nodeID
	PendErr  error
}

func (r *AskBus) key(runID, nodeID string) string { return runID + ":" + nodeID }

// Pending records the call.
func (r *AskBus) Pending(ctx context.Context, runID, nodeID, question string) error {
	r.mu.Lock()
	r.pending = append(r.pending, PendingCall{Ctx: ctx, RunID: runID, NodeID: nodeID, Question: question})
	pendErr := r.PendErr
	r.mu.Unlock()
	return pendErr
}

// LookupAnswer records the call and returns the pre-loaded answer if any.
func (r *AskBus) LookupAnswer(ctx context.Context, runID, nodeID string) (string, bool) {
	r.mu.Lock()
	r.lookups = append(r.lookups, LookupCall{Ctx: ctx, RunID: runID, NodeID: nodeID})
	ans, ok := r.answers[r.key(runID, nodeID)]
	r.mu.Unlock()
	return ans, ok
}

// SetAnswer pre-loads an answer for the given (runID, nodeID) pair.
func (r *AskBus) SetAnswer(runID, nodeID, answer string) {
	r.mu.Lock()
	if r.answers == nil {
		r.answers = make(map[string]string)
	}
	r.answers[r.key(runID, nodeID)] = answer
	r.mu.Unlock()
}

// PendingCalls returns a snapshot of all recorded Pending calls.
func (r *AskBus) PendingCalls() []PendingCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PendingCall, len(r.pending))
	copy(out, r.pending)
	return out
}

// LookupCalls returns a snapshot of all recorded LookupAnswer calls.
func (r *AskBus) LookupCalls() []LookupCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LookupCall, len(r.lookups))
	copy(out, r.lookups)
	return out
}

// RequirePendingCalledWith asserts at least one Pending call matches the
// given matcher.
func (r *AskBus) RequirePendingCalledWith(t *testing.T, match func(PendingCall) bool, desc string) {
	t.Helper()
	for _, c := range r.PendingCalls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("AskBus.Pending: no call found matching %q", desc)
}

// RequirePendingQuestion asserts that Pending was called with the given question.
func (r *AskBus) RequirePendingQuestion(t *testing.T, question string) {
	t.Helper()
	r.RequirePendingCalledWith(t, func(c PendingCall) bool {
		return c.Question == question
	}, "question="+question)
}

// RequireLookupCalled asserts at least one LookupAnswer call was recorded.
func (r *AskBus) RequireLookupCalled(t *testing.T) {
	t.Helper()
	if len(r.LookupCalls()) == 0 {
		t.Fatal("AskBus.LookupAnswer: never called")
	}
}

// Reset clears recorded calls (pre-loaded answers are preserved).
func (r *AskBus) Reset() {
	r.mu.Lock()
	r.pending = nil
	r.lookups = nil
	r.mu.Unlock()
}
