package advice

import (
	"context"
	"fmt"
	"sync"
)

// HeuristicFunc computes a Recommendation directly from a kind's already-
// extracted Features — no model call, no I/O, no ctx — for the v1 advice
// trio's rung-R1 backends (tasks.md's DESIGN-LOCKED REVISION,
// laya-advisors-01LAYA001 WP04-06): branch_now adapts the shipped
// core/branchadvisor detector; compact_now is a fill-threshold rule;
// escalate_model is a failure-streak rule. modelID is the honest
// "heuristic/<name>-v1" string the spec requires (never a real model
// id — HeuristicAdvisor stamps Rung=RungHeuristic and Unbenchmarked=true
// unconditionally, so a HeuristicFunc does not need to report either).
// An error here degrades to ErrNoAdvice exactly like every other Advisor
// failure mode — a HeuristicFunc that cannot type-assert features to its
// own expected shape should not happen (kind.Extract already produced
// it), but this stays defensive for the same reason FeatureExtractor's
// own contract is.
type HeuristicFunc func(features Features) (decision bool, confidence int, modelID string, err error)

// KindGate is a per-kind runtime enable check a caller may wire onto a
// HeuristicAdvisor via SetKindGate — spec AC-02's "kinds are
// independently disableable," implemented as a real branch rather than
// registry-level conditional registration (the registry itself is a
// process-global, populate-once-at-init structure; disabling a kind at
// runtime — e.g. a Settings toggle a user can flip without restarting —
// has to live at the Advisor call boundary instead). A nil or absent
// gate for a kind means "always enabled," matching the zero-value
// default a Settings bool field carries when unconfigured.
type KindGate func() bool

// HeuristicAdvisor is the production Advisor for laya-advisors-01LAYA001's
// rung-R1 trio: a bounded, session-scoped-cached dispatch to a
// per-kind HeuristicFunc, mirroring LLMAdvisor's skip-path and caching
// contract exactly (Moot, a dismissed cache entry, a cache hit) but
// replacing "resolve a model, call it, parse its reply" with "look up
// the kind's registered rule and call it directly." See Advisor's doc
// comment (advisor.go) and this package's doc comment for the shared
// failure contract every Advisor implementation honors.
type HeuristicAdvisor struct {
	mu    sync.RWMutex
	fns   map[string]HeuristicFunc
	gates map[string]KindGate
	cache *adviceCache
}

// HeuristicAdvisorOption tunes a HeuristicAdvisor at construction time.
type HeuristicAdvisorOption func(*HeuristicAdvisor)

// WithHeuristicAdvisorCacheCapacity overrides defaultAdviceCacheCapacity.
// n <= 0 is ignored.
func WithHeuristicAdvisorCacheCapacity(n int) HeuristicAdvisorOption {
	return func(a *HeuristicAdvisor) {
		if n > 0 {
			a.cache = newAdviceCache(n)
		}
	}
}

// NewHeuristicAdvisor constructs an empty HeuristicAdvisor — no kind has
// a registered backend until RegisterHeuristic is called for it.
func NewHeuristicAdvisor(opts ...HeuristicAdvisorOption) *HeuristicAdvisor {
	a := &HeuristicAdvisor{
		fns:   map[string]HeuristicFunc{},
		gates: map[string]KindGate{},
		cache: newAdviceCache(defaultAdviceCacheCapacity),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// compile-time witness that *HeuristicAdvisor satisfies Advisor.
var _ Advisor = (*HeuristicAdvisor)(nil)

// RegisterHeuristic wires fn as kindID's rung-R1 backend. Panics on a
// duplicate kindID — a startup-time programmer error, the same
// MustRegister contract AdviceKind registration itself uses (kind.go),
// since two competing heuristics silently overwriting one another is
// exactly the "shipped a bug at boot, degraded silently" failure mode
// this seam otherwise refuses everywhere else.
func (a *HeuristicAdvisor) RegisterHeuristic(kindID string, fn HeuristicFunc) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.fns[kindID]; exists {
		panic(fmt.Sprintf("advice: HeuristicAdvisor already has a backend registered for kind %q", kindID))
	}
	a.fns[kindID] = fn
}

// SetKindGate wires (or replaces) kindID's runtime enable check. Passing
// a nil gate is equivalent to never calling SetKindGate for that kind
// (always enabled) — callers do this to represent "no Settings field
// exists for this kind" without a separate code path.
func (a *HeuristicAdvisor) SetKindGate(kindID string, gate KindGate) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if gate == nil {
		delete(a.gates, kindID)
		return
	}
	a.gates[kindID] = gate
}

func (a *HeuristicAdvisor) heuristic(kindID string) (HeuristicFunc, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	fn, ok := a.fns[kindID]
	return fn, ok
}

func (a *HeuristicAdvisor) kindEnabled(kindID string) bool {
	a.mu.RLock()
	gate, ok := a.gates[kindID]
	a.mu.RUnlock()
	if !ok || gate == nil {
		return true
	}
	return gate()
}

// Recommend implements Advisor. Every failure mode — nil advisor, no
// heuristic registered for kind, the kind disabled via its KindGate, a
// moot session, a dismissed cache entry, an unhashable Features value, an
// out-of-range confidence — returns an error wrapping ErrNoAdvice and a
// zero Recommendation, exactly like LLMAdvisor.Recommend's contract.
// Unlike LLMAdvisor there is no timeout and no context derivation
// subtlety to document: a HeuristicFunc is pure Go arithmetic, so ctx is
// used only (as with LLMAdvisor) for nothing on this path — it exists
// solely to satisfy the Advisor interface shape.
func (a *HeuristicAdvisor) Recommend(_ context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	if a == nil {
		return Recommendation{}, fmt.Errorf("%w: no advisor configured", ErrNoAdvice)
	}
	if sess.Moot {
		// Skip path 1 (spec §2): identical semantics to LLMAdvisor.
		return Recommendation{}, fmt.Errorf("%w: moot for this session", ErrNoAdvice)
	}
	if !a.kindEnabled(kind.ID) {
		// Independent per-kind disable (AC-02): treated exactly like
		// "moot" — zero heuristic calls, zero cache activity, because a
		// disabled kind's answer cannot change anything either.
		return Recommendation{}, fmt.Errorf("%w: kind %q disabled", ErrNoAdvice, kind.ID)
	}

	hash, herr := FeaturesHash(features)
	if herr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, herr)
	}
	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}

	if entry, ok := a.cache.get(key); ok {
		if entry.dismissed {
			// Skip path 2 (AC-03): identical semantics to LLMAdvisor.
			return Recommendation{}, fmt.Errorf("%w: dismissed for materially identical features", ErrNoAdvice)
		}
		rec := entry.rec
		rec.CacheHit = true
		return rec, nil
	}

	fn, ok := a.heuristic(kind.ID)
	if !ok {
		return Recommendation{}, fmt.Errorf("%w: no heuristic backend registered for kind %q", ErrNoAdvice, kind.ID)
	}

	decision, confidence, modelID, ferr := fn(features)
	if ferr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, ferr)
	}
	if verr := ValidateConfidence(confidence); verr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, verr)
	}

	rec := Recommendation{
		Decision:      decision,
		Confidence:    confidence,
		KindID:        kind.ID,
		PromptVersion: kind.PromptVersion,
		Model:         modelID,
		Rung:          RungHeuristic,
		Unbenchmarked: true,
	}
	a.cache.put(key, rec)
	return rec, nil
}

// Dismiss implements Advisor. Identical contract to LLMAdvisor.Dismiss.
func (a *HeuristicAdvisor) Dismiss(sess SessionContext, kind AdviceKind, features Features) {
	if a == nil {
		return
	}
	hash, err := FeaturesHash(features)
	if err != nil {
		return
	}
	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}
	a.cache.dismiss(key)
}
