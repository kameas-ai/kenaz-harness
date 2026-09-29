package advice

import (
	"context"
	"fmt"
	"sync"
)

// RecommendCall records one Recommend invocation, for tests asserting
// call counts — mirrors risk.RateCall's "the advisor never invoked on a
// moot/dismissed path" verification need.
type RecommendCall struct {
	KindID       string
	FeaturesHash string
	SessionID    string
}

// fakeResult is the scripted outcome for one kind id.
type fakeResult struct {
	rec Recommendation
	err error
}

// FakeAdvisor is a deterministic Advisor test double, mirroring
// risk.FakeRater's shape exactly. Results are scripted per kind id (or a
// default for unscripted kinds); every Recommend call is recorded. Unlike
// FakeRater, FakeAdvisor ALSO implements the real skip semantics
// (SessionContext.Moot and a Dismiss'd key both short-circuit before a
// call is recorded) so a kind's own tests (WP04-06) exercise the same
// AC-03 / moot-skip behaviour against the fake that LLMAdvisor provides
// against a real model, without needing a fake LLM registry of their own.
//
// Race-safe per CLAUDE.md's mutex + snapshot pattern.
type FakeAdvisor struct {
	mu        sync.Mutex
	byKind    map[string]fakeResult
	def       fakeResult
	calls     []RecommendCall
	dismissed map[cacheKey]bool
}

// NewFakeAdvisor returns a FakeAdvisor with no scripted results — every
// unscripted Recommend call returns the zero Recommendation until
// ScriptFor/ScriptDefault/ScriptErrorFor is called.
func NewFakeAdvisor() *FakeAdvisor {
	return &FakeAdvisor{byKind: map[string]fakeResult{}, dismissed: map[cacheKey]bool{}}
}

// compile-time witness that *FakeAdvisor satisfies Advisor.
var _ Advisor = (*FakeAdvisor)(nil)

// ScriptFor scripts the Recommendation returned for calls naming this
// exact kind id. Returns the receiver so calls can be chained at
// construction.
func (f *FakeAdvisor) ScriptFor(kindID string, r Recommendation) *FakeAdvisor {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byKind[kindID] = fakeResult{rec: r}
	return f
}

// ScriptErrorFor scripts an error returned for calls naming this exact
// kind id — used to exercise Advisor's failure contract (every failure
// degrades to ErrNoAdvice, never a guess).
func (f *FakeAdvisor) ScriptErrorFor(kindID string, err error) *FakeAdvisor {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byKind[kindID] = fakeResult{err: err}
	return f
}

// ScriptDefault sets the Recommendation returned for any kind with no
// specific script.
func (f *FakeAdvisor) ScriptDefault(r Recommendation) *FakeAdvisor {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.def = fakeResult{rec: r}
	return f
}

// Recommend implements Advisor.
func (f *FakeAdvisor) Recommend(_ context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	if sess.Moot {
		return Recommendation{}, fmt.Errorf("%w: moot for this session", ErrNoAdvice)
	}

	hash, herr := FeaturesHash(features)
	if herr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, herr)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}
	if f.dismissed[key] {
		return Recommendation{}, fmt.Errorf("%w: dismissed for materially identical features", ErrNoAdvice)
	}

	f.calls = append(f.calls, RecommendCall{KindID: kind.ID, FeaturesHash: hash, SessionID: sess.SessionID})

	res, ok := f.byKind[kind.ID]
	if !ok {
		res = f.def
	}
	if res.err != nil {
		return Recommendation{}, res.err
	}
	if err := ValidateConfidence(res.rec.Confidence); err != nil {
		return Recommendation{}, fmt.Errorf("advice: FakeAdvisor scripted an invalid recommendation for kind %q: %w", kind.ID, err)
	}
	return res.rec, nil
}

// Dismiss implements Advisor.
func (f *FakeAdvisor) Dismiss(sess SessionContext, kind AdviceKind, features Features) {
	hash, err := FeaturesHash(features)
	if err != nil {
		return
	}
	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissed[key] = true
}

// Calls returns a snapshot of every Recommend call recorded so far
// (dismissed/moot short-circuits are NOT recorded — they are the
// zero-call proof), in call order. Safe to call concurrently with
// Recommend.
func (f *FakeAdvisor) Calls() []RecommendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RecommendCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallCount is a convenience wrapper over len(Calls()).
func (f *FakeAdvisor) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}
