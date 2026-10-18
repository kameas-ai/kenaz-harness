package risk

import (
	"context"
	"fmt"
	"sync"
)

// RateCall records one Rate invocation, for tests asserting call
// counts (the "rater never invoked on the layers-1-2 paths" property —
// plan.md's verification strategy — needs counts, not just outcomes).
type RateCall struct {
	Tool           string
	NormalizedArgs string
	SessionID      string
}

// fakeResult is the scripted outcome for one tool name.
type fakeResult struct {
	rating Rating
	err    error
}

// FakeRater is a deterministic RiskRater test double. Results are
// scripted per tool name (or a default for unscripted tools); every
// call is recorded for later assertion via Calls().
//
// Race-safe per CLAUDE.md's mutex + snapshot pattern: CI runs
// `go test -race`, and a fake written to from a dispatch goroutine while
// a test body reads Calls() concurrently needs this discipline.
type FakeRater struct {
	mu     sync.Mutex
	byTool map[string]fakeResult
	def    fakeResult
	calls  []RateCall
}

// NewFakeRater returns a FakeRater with no scripted results — every
// unscripted Rate call returns the zero Rating (Score 0) until
// ScriptFor/ScriptDefault/ScriptErrorFor is called.
func NewFakeRater() *FakeRater {
	return &FakeRater{byTool: map[string]fakeResult{}}
}

// ScriptFor scripts the Rating returned for calls naming this exact
// tool. Returns the receiver so calls can be chained at construction.
func (f *FakeRater) ScriptFor(tool string, r Rating) *FakeRater {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byTool[tool] = fakeResult{rating: r}
	return f
}

// ScriptErrorFor scripts an error returned for calls naming this exact
// tool — used to exercise RiskRater's failure contract (timeout,
// unparseable response, etc. all surface as errors, never a best-guess
// Rating).
func (f *FakeRater) ScriptErrorFor(tool string, err error) *FakeRater {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byTool[tool] = fakeResult{err: err}
	return f
}

// ScriptDefault sets the Rating returned for any tool with no specific
// script.
func (f *FakeRater) ScriptDefault(r Rating) *FakeRater {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.def = fakeResult{rating: r}
	return f
}

// Rate implements RiskRater. Honours ValidateScore even for scripted
// results: a test that scripts an invalid score is a test bug, and
// FakeRater surfacing that as an error (rather than silently returning
// it) keeps the fake truthful to the real contract.
func (f *FakeRater) Rate(_ context.Context, tool string, normalizedArgs string, sessCtx SessionContext) (Rating, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, RateCall{Tool: tool, NormalizedArgs: normalizedArgs, SessionID: sessCtx.SessionID})

	res, ok := f.byTool[tool]
	if !ok {
		res = f.def
	}
	if res.err != nil {
		return Rating{}, res.err
	}
	if err := ValidateScore(res.rating.Score); err != nil {
		return Rating{}, fmt.Errorf("risk: FakeRater scripted an invalid rating for tool %q: %w", tool, err)
	}
	return res.rating, nil
}

// Calls returns a snapshot of every Rate call recorded so far, in call
// order. Safe to call concurrently with Rate.
func (f *FakeRater) Calls() []RateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RateCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallCount is a convenience wrapper over len(Calls()) for the common
// "assert the rater was invoked exactly N times" assertion.
func (f *FakeRater) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}
