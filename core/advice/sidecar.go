package advice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// DefaultAdvisorBudget is spec §2's hard bound on one Recommend call:
// "advice is worthless late — unlike the rater nothing WAITS on it." It
// was LLMAdvisor's defaultAdvisorTimeout; SidecarAdvisor is the first
// production Advisor whose call is genuinely network-shaped, so the bound
// now lives here as a real (non-test) constant. The caller-side dispatch
// budget (chat.AdviceRecommendBudget) is the same 800ms.
const DefaultAdvisorBudget = 800 * time.Millisecond

// defaultContractsTTL bounds how long a /v1/contracts answer is trusted
// before SidecarAdvisor asks again. Short on purpose: the engine flips a
// kind's serving state (graduation, flip-back — design D-B1/D-B5)
// engine-side, and "never sticky" means a flip is honored within seconds,
// while a chat turn's advice hook still almost always hits the cache and
// spends no extra round trip.
const defaultContractsTTL = 30 * time.Second

// ErrEngineKindNotServed is what a SidecarEngine returns when the engine
// refuses a kind it has no graduated model for (design Amendment A3.2:
// "REFUSES a kind with no graduated model, typed 'kind not served';
// falling back is the CLIENT's job"). SidecarAdvisor treats it as a
// fall-through to the local heuristic — never an error.
var ErrEngineKindNotServed = errors.New("advice: engine does not serve this kind")

// EngineKindContract is the slice of GET /v1/contracts a SidecarAdvisor
// routes on: which contract version the engine expects for a kind (its
// 16-hex feature-contract hash, echoed verbatim on the recommend call —
// the harness never computes it), and whether the engine serves the kind
// right now.
type EngineKindContract struct {
	ContractVersion string
	Available       bool
}

// EngineRequest is one POST /v1/recommend/{kind} call, advice-side shape
// (core/mlsidecar.AdviceEngine maps it onto the wire type, so this package
// never imports the lifecycle manager — advice stays the lighter package).
type EngineRequest struct {
	KindID                 string
	Features               map[string]any
	FeatureContractVersion string
	SessionID              string
}

// EngineResponse is the engine's answer, advice-side shape. Decision is a
// pointer because a binary kind's answer missing entirely is a MALFORMED
// response, not "false".
type EngineResponse struct {
	Decision      *bool
	Confidence    int
	KindID        string
	Model         string
	Rung          string
	Unbenchmarked bool
}

// SidecarEngine is the narrow engine port SidecarAdvisor drives.
// Production: core/mlsidecar.AdviceEngine over the loopback Client. Tests:
// a scripted fake covering the AC-02 fault matrix.
type SidecarEngine interface {
	Contracts(ctx context.Context) (map[string]EngineKindContract, error)
	Recommend(ctx context.Context, req EngineRequest) (EngineResponse, error)
}

// SidecarAdvisor is the WP15 Advisor: it routes a kind to the local ML
// engine when the sidecar is healthy AND /v1/contracts says the engine
// serves that kind, and otherwise — for any reason, per call — delegates
// to a fallback Advisor (the local HeuristicAdvisor in production).
//
// "Never sticky-broken": no failure is remembered. Each Recommend call
// re-evaluates health, the (short-TTL) contracts view, and the engine
// call from scratch, so an engine that recovers is used again on the very
// next call, and one that breaks costs exactly one call's fallback.
//
// Port guarantees it owns itself (the fallback has its own copies of the
// same ones, and Dismiss feeds both): Moot is a hard skip (zero engine
// calls, zero fallback calls, zero cache activity); a per-kind disable
// (SetKindGate) is the same; a dismissed cache entry suppresses
// everything; a cache hit is served on a copy with CacheHit set; a
// failure degrades to ErrNoAdvice only when the fallback ALSO has
// nothing; an out-of-range confidence is rejected, never clamped.
//
// Budget: one context derived from context.Background() — NOT the
// caller's ctx, LLMAdvisor's v0.78.2 rationale — bounds the contracts
// fetch and the engine call TOGETHER to the configured budget (default
// DefaultAdvisorBudget). The fallback then runs on the caller's own ctx:
// it is pure Go arithmetic, so it finishes regardless of how much of the
// budget the engine consumed.
//
// Honest fields: Model, Rung and Unbenchmarked on an engine-served
// Recommendation are the engine's own (it knows whether heuristic,
// classic or laya served it — D-B1/D-B5 flips are engine-side), so every
// label row the capture bridge writes says what actually answered.
type SidecarAdvisor struct {
	engine   SidecarEngine
	probe    SidecarProbe
	fallback Advisor
	budget   time.Duration
	ttl      time.Duration
	now      func() time.Time
	cache    *adviceCache

	mu        sync.RWMutex
	gates     map[string]KindGate
	contracts map[string]EngineKindContract
	fetchedAt time.Time
}

// SidecarAdvisorOption tunes a SidecarAdvisor at construction time.
type SidecarAdvisorOption func(*SidecarAdvisor)

// WithSidecarBudget overrides DefaultAdvisorBudget. d <= 0 is ignored.
// Production wiring does not call it; tests use it to exercise the
// timeout path quickly.
func WithSidecarBudget(d time.Duration) SidecarAdvisorOption {
	return func(a *SidecarAdvisor) {
		if d > 0 {
			a.budget = d
		}
	}
}

// WithSidecarCacheCapacity overrides defaultAdviceCacheCapacity. n <= 0
// is ignored.
func WithSidecarCacheCapacity(n int) SidecarAdvisorOption {
	return func(a *SidecarAdvisor) {
		if n > 0 {
			a.cache = newAdviceCache(n)
		}
	}
}

// WithSidecarContractsTTL overrides defaultContractsTTL. d <= 0 is
// ignored (use a tiny positive value in tests that need every call to
// re-fetch).
func WithSidecarContractsTTL(d time.Duration) SidecarAdvisorOption {
	return func(a *SidecarAdvisor) {
		if d > 0 {
			a.ttl = d
		}
	}
}

// withSidecarClock overrides the wall clock (tests only).
func withSidecarClock(now func() time.Time) SidecarAdvisorOption {
	return func(a *SidecarAdvisor) {
		if now != nil {
			a.now = now
		}
	}
}

// NewSidecarAdvisor wires engine behind probe, falling back to fallback.
// A nil probe means "never healthy" (the honest answer until something
// owns the sidecar lifecycle — WP13); a nil fallback means an engine miss
// degrades to ErrNoAdvice.
func NewSidecarAdvisor(engine SidecarEngine, probe SidecarProbe, fallback Advisor, opts ...SidecarAdvisorOption) *SidecarAdvisor {
	a := &SidecarAdvisor{
		engine:   engine,
		probe:    probe,
		fallback: fallback,
		budget:   DefaultAdvisorBudget,
		ttl:      defaultContractsTTL,
		now:      time.Now,
		cache:    newAdviceCache(defaultAdviceCacheCapacity),
		gates:    map[string]KindGate{},
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// compile-time witness that *SidecarAdvisor satisfies Advisor.
var _ Advisor = (*SidecarAdvisor)(nil)

// SetKindGate wires (or replaces) kindID's runtime enable check — the
// same per-kind disable (spec AC-02) HeuristicAdvisor carries, so a kind
// the user switched off is never sent to the engine either.
func (a *SidecarAdvisor) SetKindGate(kindID string, gate KindGate) {
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

func (a *SidecarAdvisor) kindEnabled(kindID string) bool {
	a.mu.RLock()
	gate := a.gates[kindID]
	a.mu.RUnlock()
	return gate == nil || gate()
}

// Recommend implements Advisor. See the type's doc comment for the full
// contract.
func (a *SidecarAdvisor) Recommend(ctx context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	if a == nil {
		return Recommendation{}, fmt.Errorf("%w: no advisor configured", ErrNoAdvice)
	}
	if sess.Moot {
		return Recommendation{}, fmt.Errorf("%w: moot for this session", ErrNoAdvice)
	}
	if !a.kindEnabled(kind.ID) {
		return Recommendation{}, fmt.Errorf("%w: kind %q disabled", ErrNoAdvice, kind.ID)
	}
	hash, herr := FeaturesHash(features)
	if herr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, herr)
	}
	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}
	if entry, ok := a.cache.get(key); ok {
		if entry.dismissed {
			return Recommendation{}, fmt.Errorf("%w: dismissed for materially identical features", ErrNoAdvice)
		}
		rec := entry.rec
		rec.CacheHit = true
		return rec, nil
	}

	// Route to the engine only while it is healthy. Healthy() is a cheap
	// cached read (SidecarProbe's contract) — never a probe, never a spawn.
	if a.engine != nil && a.probe != nil && a.probe.Healthy() {
		rec, err := a.engineRecommend(kind, features, sess)
		if err == nil {
			a.cache.put(key, rec)
			return rec, nil
		}
		if errors.Is(err, ErrEngineKindNotServed) {
			// Not a fault: the engine has no graduated model for this kind
			// (Amendment A3.2). Debug-level. Only a refusal the ENGINE
			// returned contradicts the cached contracts view, so only that
			// one drops it; a "not available" read straight OUT of the
			// view agrees with it and must keep it — dropping it there
			// turned the TTL cache into a /v1/contracts round trip on
			// every call for every unserved kind (all of them, on the
			// real engine's day 1).
			if !errors.Is(err, errNotServedByContractsView) {
				a.invalidateContracts()
			}
			logging.L().Debug("advice.sidecar.kind_not_served", "kind", kind.ID)
		} else {
			logging.L().Debug("advice.sidecar.engine_fallthrough", "kind", kind.ID, "err", err.Error())
		}
	}
	return a.fallbackRecommend(ctx, kind, features, sess)
}

func (a *SidecarAdvisor) fallbackRecommend(ctx context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	if a.fallback == nil {
		return Recommendation{}, fmt.Errorf("%w: no engine recommendation and no fallback", ErrNoAdvice)
	}
	return a.fallback.Recommend(ctx, kind, features, sess)
}

// errNotServedByContractsView marks a "not served" verdict read from the
// cached /v1/contracts view (as opposed to one the engine returned).
var errNotServedByContractsView = errors.New("advice: contracts view does not serve this kind")

// errNotServedByContract is the internal "contracts say no" signal; it
// wraps ErrEngineKindNotServed so both refusal sources take one branch,
// and errNotServedByContractsView so the view is not dropped over it.
func errNotServedByContract(kindID string) error {
	return fmt.Errorf("%w: %w: /v1/contracts does not list %q as available", ErrEngineKindNotServed, errNotServedByContractsView, kindID)
}

// engineRecommend performs the contracts check + engine call under ONE
// budget and validates the answer into a Recommendation. Any error means
// "fall through"; it never returns a partially-valid Recommendation.
func (a *SidecarAdvisor) engineRecommend(kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	callCtx, cancel := context.WithTimeout(context.Background(), a.budget)
	defer cancel()

	contracts, err := a.contractsView(callCtx)
	if err != nil {
		return Recommendation{}, fmt.Errorf("contracts: %w", err)
	}
	c, served := contracts[kind.ID]
	if !served || !c.Available {
		return Recommendation{}, errNotServedByContract(kind.ID)
	}
	fmap, err := featuresAsObject(features)
	if err != nil {
		return Recommendation{}, err
	}

	resp, err := a.engine.Recommend(callCtx, EngineRequest{
		KindID:                 kind.ID,
		Features:               fmap,
		FeatureContractVersion: c.ContractVersion,
		SessionID:              sess.SessionID,
	})
	if err != nil {
		return Recommendation{}, err
	}
	// Validate — every rejection is a fall-through, never a clamp or a
	// guess (AC-02's malformed / out-of-range cells).
	if resp.Decision == nil {
		return Recommendation{}, errors.New("malformed engine response: no decision")
	}
	if resp.KindID != kind.ID {
		return Recommendation{}, fmt.Errorf("malformed engine response: kind_id %q for a %q request", resp.KindID, kind.ID)
	}
	if resp.Model == "" {
		return Recommendation{}, errors.New("malformed engine response: no model id")
	}
	if verr := ValidateConfidence(resp.Confidence); verr != nil {
		return Recommendation{}, verr
	}
	rung := ModelRung(resp.Rung)
	if rung == "" {
		// The engine did not say which rung of ITS ladder answered; the
		// only honest statement is the harness-side one — the managed
		// local sidecar served this.
		rung = RungLocalLaya
	}
	return Recommendation{
		Decision:      *resp.Decision,
		Confidence:    resp.Confidence,
		KindID:        kind.ID,
		PromptVersion: kind.PromptVersion,
		Model:         resp.Model,
		Rung:          rung,
		Unbenchmarked: resp.Unbenchmarked,
	}, nil
}

// contractsView returns the engine's served-kind view, refetching when
// the cached one is older than the TTL. A failed fetch is NOT cached (and
// drops the stale view): "the engine could not say" must never be
// remembered as "the engine serves X".
func (a *SidecarAdvisor) contractsView(ctx context.Context) (map[string]EngineKindContract, error) {
	a.mu.RLock()
	view, at := a.contracts, a.fetchedAt
	a.mu.RUnlock()
	if view != nil && a.now().Sub(at) < a.ttl {
		return view, nil
	}
	fresh, err := a.engine.Contracts(ctx)
	if err != nil {
		a.invalidateContracts()
		return nil, err
	}
	if fresh == nil {
		fresh = map[string]EngineKindContract{}
	}
	a.mu.Lock()
	a.contracts, a.fetchedAt = fresh, a.now()
	a.mu.Unlock()
	return fresh, nil
}

func (a *SidecarAdvisor) invalidateContracts() {
	a.mu.Lock()
	a.contracts = nil
	a.mu.Unlock()
}

// featuresAsObject renders a kind's Features as the JSON object the wire
// carries — the same marshaling FeaturesHash and the label capture use, so
// the engine, the hash and the stored label all describe identical bytes.
func featuresAsObject(f Features) (map[string]any, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("features not serializable: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, errors.New("features do not marshal to a JSON object")
	}
	return m, nil
}

// Dismiss implements Advisor: tombstones the dismissal in this advisor's
// own cache AND forwards it to the fallback, so a dismissed
// recommendation stays suppressed whichever layer would answer next
// (the fallback's cache key is the identical (session, kind, hash,
// prompt version) tuple).
func (a *SidecarAdvisor) Dismiss(sess SessionContext, kind AdviceKind, features Features) {
	if a == nil {
		return
	}
	if hash, err := FeaturesHash(features); err == nil {
		a.cache.dismiss(cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion})
	}
	if a.fallback != nil {
		a.fallback.Dismiss(sess, kind, features)
	}
}
