package advice

// sidecar_test.go — SidecarAdvisor's proofs (laya-advisors-01LAYA001
// WP15). This file also DISCHARGES the LLMAdvisor deletion condition
// (design §9): the fault matrix the dormant LLMAdvisor double used to be
// the only in-repo proof of — timeout, caller-ctx independence, malformed
// response, out-of-range confidence, transport error, nil advisor, moot,
// dismissal, cache hit, cross-session isolation — is covered below
// against a SidecarEngine fake, and llmadvisor_dormant_test.go /
// llmadvisor_test.go are deleted in the same change.
//
// Timing discipline (see core/rpc/views/agentgraph/chat/
// advice_hook_budget_test.go's comments — wall-clock launch assertions
// flaked three times on shared CI): nothing here asserts a duration
// lower bound or a "returns within N ms" upper bound tighter than a
// decisive multiple. The budget tests force ORDERING instead — the engine
// fake hangs far longer than any budget, so Recommend can only return
// because the budget's ctx cancelled the call, and the fake records which
// branch released it.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// toyAdviceKind returns a minimal, fully-valid AdviceKind for tests that
// don't care about a real extractor/renderer — just that Recommend's
// mechanics (cache, budget, routing) work against SOME kind. (Moved here
// from the deleted llmadvisor_test.go; heuristic_test.go also uses it.)
func toyAdviceKind(id, promptVersion string, safety SafetyClass) AdviceKind {
	return AdviceKind{
		ID:            id,
		PromptVersion: promptVersion,
		SafetyClass:   safety,
		Extract:       func(input any) (Features, error) { return input, nil },
		RenderPrompt:  func(f Features) (string, string) { return "system", "user" },
	}
}

// toyFeatures is a JSON-object-shaped Features value (the wire carries
// objects; a bare string would not marshal to one).
type toyFeatures struct {
	N int `json:"n"`
}

// togglingProbe is a SidecarProbe whose health can flip mid-test.
type togglingProbe struct{ healthy atomic.Bool }

func (p *togglingProbe) Healthy() bool    { return p.healthy.Load() }
func (p *togglingProbe) Identity() string { return "kenaz-ml-sidecar@test" }

func healthyProbe() *togglingProbe {
	p := &togglingProbe{}
	p.healthy.Store(true)
	return p
}

// fakeEngine is a scripted SidecarEngine. Every field is guarded by mu
// (-race discipline); read counters through the snapshot helpers.
type fakeEngine struct {
	mu           sync.Mutex
	contracts    map[string]EngineKindContract
	contractsErr error
	onRecommend  func(ctx context.Context, req EngineRequest) (EngineResponse, error)

	contractCalls  int
	recommendCalls int
	lastReq        EngineRequest
}

func servedContracts(kindIDs ...string) map[string]EngineKindContract {
	m := map[string]EngineKindContract{}
	for _, id := range kindIDs {
		m[id] = EngineKindContract{ContractVersion: 3, Available: true}
	}
	return m
}

func boolp(b bool) *bool { return &b }

func okEngineResponse(kindID string) EngineResponse {
	return EngineResponse{Decision: boolp(true), Confidence: 88, KindID: kindID, Model: "kenaz-ml/classic-abc12345", Rung: "classic", Unbenchmarked: false}
}

func (e *fakeEngine) Contracts(ctx context.Context) (map[string]EngineKindContract, error) {
	e.mu.Lock()
	e.contractCalls++
	err, c := e.contractsErr, e.contracts
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (e *fakeEngine) Recommend(ctx context.Context, req EngineRequest) (EngineResponse, error) {
	e.mu.Lock()
	e.recommendCalls++
	e.lastReq = req
	fn := e.onRecommend
	e.mu.Unlock()
	if fn == nil {
		return okEngineResponse(req.KindID), nil
	}
	return fn(ctx, req)
}

func (e *fakeEngine) counts() (contracts, recommends int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.contractCalls, e.recommendCalls
}

func (e *fakeEngine) setRecommend(fn func(context.Context, EngineRequest) (EngineResponse, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onRecommend = fn
}

// sidecarFixture wires a SidecarAdvisor over a fakeEngine with a
// HeuristicAdvisor fallback that answers (false, 41, "heuristic/toy-v1").
type sidecarFixture struct {
	adv      *SidecarAdvisor
	engine   *fakeEngine
	probe    *togglingProbe
	fallback *HeuristicAdvisor
	fh       *fixedHeuristic
	kind     AdviceKind
}

func newSidecarFixture(kindID string, opts ...SidecarAdvisorOption) *sidecarFixture {
	kind := toyAdviceKind(kindID, "v1", SafetyReversible)
	engine := &fakeEngine{contracts: servedContracts(kindID)}
	probe := healthyProbe()
	fb := NewHeuristicAdvisor()
	fh := &fixedHeuristic{decision: false, confidence: 41, modelID: "heuristic/toy-v1"}
	fb.RegisterHeuristic(kindID, fh.fn)
	return &sidecarFixture{
		adv:      NewSidecarAdvisor(engine, probe, fb, opts...),
		engine:   engine,
		probe:    probe,
		fallback: fb,
		fh:       fh,
		kind:     kind,
	}
}

var sidecarSess = SessionContext{SessionID: "sess-1"}

func (f *sidecarFixture) recommend(t *testing.T, feat toyFeatures, sess SessionContext) (Recommendation, error) {
	t.Helper()
	return f.adv.Recommend(context.Background(), f.kind, feat, sess)
}

// assertFallbackServed fails unless rec is the HeuristicAdvisor's answer.
func assertFallbackServed(t *testing.T, rec Recommendation, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("want the fallback's recommendation, got error %v", err)
	}
	if rec.Model != "heuristic/toy-v1" || rec.Rung != RungHeuristic || !rec.Unbenchmarked {
		t.Fatalf("want the local heuristic's answer, got %+v", rec)
	}
}

// ---- routing ----

func TestSidecarAdvisor_HappyPath_EngineServes_HonestFields(t *testing.T) {
	f := newSidecarFixture("sc_happy")
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision || rec.Confidence != 88 {
		t.Errorf("got Decision=%v Confidence=%d, want true/88", rec.Decision, rec.Confidence)
	}
	// Honest fields come from the ENGINE — never the harness's guess.
	if rec.Model != "kenaz-ml/classic-abc12345" || rec.Rung != ModelRung("classic") || rec.Unbenchmarked {
		t.Errorf("engine's Model/Rung/Unbenchmarked not carried verbatim: %+v", rec)
	}
	if rec.KindID != "sc_happy" || rec.PromptVersion != "v1" {
		t.Errorf("KindID/PromptVersion = %q/%q", rec.KindID, rec.PromptVersion)
	}
	if f.fh.calls != 0 {
		t.Errorf("the local heuristic ran %d times though the engine served", f.fh.calls)
	}
	f.engine.mu.Lock()
	req := f.engine.lastReq
	f.engine.mu.Unlock()
	if req.FeatureContractVersion != 3 || req.SessionID != "sess-1" || req.KindID != "sc_happy" || req.Features["n"] != float64(1) {
		t.Errorf("engine request = %+v, want contract version 3 from /v1/contracts, the session, and the marshaled features", req)
	}
}

func TestSidecarAdvisor_EmptyEngineRungIsHonestlyLocalLaya(t *testing.T) {
	f := newSidecarFixture("sc_norung")
	f.engine.setRecommend(func(_ context.Context, req EngineRequest) (EngineResponse, error) {
		r := okEngineResponse(req.KindID)
		r.Rung = ""
		return r, nil
	})
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if err != nil || rec.Rung != RungLocalLaya {
		t.Fatalf("got %+v, %v; want Rung=%s when the engine names none", rec, err, RungLocalLaya)
	}
}

func TestSidecarAdvisor_UnhealthySidecar_NoEngineTraffic_FallbackServes(t *testing.T) {
	f := newSidecarFixture("sc_unhealthy")
	f.probe.healthy.Store(false)
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)
	if c, r := f.engine.counts(); c != 0 || r != 0 {
		t.Errorf("an unhealthy sidecar received traffic: contracts=%d recommend=%d", c, r)
	}
}

func TestSidecarAdvisor_NilProbeMeansNeverHealthy(t *testing.T) {
	kind := toyAdviceKind("sc_nilprobe", "v1", SafetyReversible)
	engine := &fakeEngine{contracts: servedContracts(kind.ID)}
	fb := NewHeuristicAdvisor()
	fh := &fixedHeuristic{confidence: 41, modelID: "heuristic/toy-v1"}
	fb.RegisterHeuristic(kind.ID, fh.fn)
	adv := NewSidecarAdvisor(engine, nil, fb)
	rec, err := adv.Recommend(context.Background(), kind, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)
	if c, r := engine.counts(); c != 0 || r != 0 {
		t.Errorf("nil probe still reached the engine: %d/%d", c, r)
	}
}

func TestSidecarAdvisor_KindNotListedByContracts_RoutesToFallback_NoRecommendCall(t *testing.T) {
	f := newSidecarFixture("sc_unlisted")
	f.engine.contracts = servedContracts("some_other_kind")
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)
	if _, r := f.engine.counts(); r != 0 {
		t.Errorf("Recommend was sent for a kind /v1/contracts does not serve (%d calls)", r)
	}
}

func TestSidecarAdvisor_ContractsSayUnavailable_RoutesToFallback(t *testing.T) {
	f := newSidecarFixture("sc_unavail")
	f.engine.contracts = map[string]EngineKindContract{"sc_unavail": {ContractVersion: 1, Available: false}}
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)
	if _, r := f.engine.counts(); r != 0 {
		t.Errorf("Recommend sent for an unavailable kind (%d calls)", r)
	}
}

// TestSidecarAdvisor_KindNotServedRefusal_IsFallthroughNotError is
// Amendment A3.2's cell: the engine refuses an unserved kind with the
// typed error; the harness falls through to its heuristic and returns a
// normal recommendation — no error, no ErrNoAdvice — and drops the
// contracts view that misled it.
func TestSidecarAdvisor_KindNotServedRefusal_IsFallthroughNotError(t *testing.T) {
	f := newSidecarFixture("sc_refused")
	f.engine.setRecommend(func(context.Context, EngineRequest) (EngineResponse, error) {
		return EngineResponse{}, ErrEngineKindNotServed
	})
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)
	if errors.Is(err, ErrNoAdvice) {
		t.Error("kind_not_served surfaced as ErrNoAdvice")
	}
	// The contracts view was dropped: the next (different-features) call
	// re-fetches instead of trusting the view the refusal contradicted.
	c0, _ := f.engine.counts()
	if _, err := f.recommend(t, toyFeatures{N: 2}, sidecarSess); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c1, _ := f.engine.counts(); c1 != c0+1 {
		t.Errorf("contracts fetched %d times after the refusal, want a re-fetch (%d)", c1, c0+1)
	}
}

// TestSidecarAdvisor_NeverStickyBroken: an engine that fails once costs
// exactly that one call; the very next call uses it again.
func TestSidecarAdvisor_NeverStickyBroken(t *testing.T) {
	f := newSidecarFixture("sc_sticky")
	var fail atomic.Bool
	fail.Store(true)
	f.engine.setRecommend(func(_ context.Context, req EngineRequest) (EngineResponse, error) {
		if fail.Load() {
			return EngineResponse{}, errors.New("engine exploded")
		}
		return okEngineResponse(req.KindID), nil
	})
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)

	fail.Store(false)
	rec, err = f.recommend(t, toyFeatures{N: 2}, sidecarSess)
	if err != nil || rec.Model != "kenaz-ml/classic-abc12345" {
		t.Fatalf("after recovery got %+v, %v; want the engine again", rec, err)
	}

	// And health flips are per-call too: down, then up.
	f.probe.healthy.Store(false)
	rec, err = f.recommend(t, toyFeatures{N: 3}, sidecarSess)
	assertFallbackServed(t, rec, err)
	f.probe.healthy.Store(true)
	rec, err = f.recommend(t, toyFeatures{N: 4}, sidecarSess)
	if err != nil || rec.Model != "kenaz-ml/classic-abc12345" {
		t.Fatalf("after the sidecar came back got %+v, %v", rec, err)
	}
}

// ---- the AC-02 fault matrix (what LLMAdvisor's double used to prove) ----

// Every cell: the engine misbehaves; the caller still gets the fallback's
// answer (never an error, never a clamp, never the bad value).
func TestSidecarAdvisor_FaultMatrix_FallsThroughToHeuristic(t *testing.T) {
	bad := func(mutate func(*EngineResponse)) func(context.Context, EngineRequest) (EngineResponse, error) {
		return func(_ context.Context, req EngineRequest) (EngineResponse, error) {
			r := okEngineResponse(req.KindID)
			mutate(&r)
			return r, nil
		}
	}
	cells := []struct {
		name string
		fn   func(context.Context, EngineRequest) (EngineResponse, error)
	}{
		{"transport error", func(context.Context, EngineRequest) (EngineResponse, error) {
			return EngineResponse{}, errors.New("connection refused")
		}},
		{"malformed: no decision", bad(func(r *EngineResponse) { r.Decision = nil })},
		{"malformed: wrong kind_id", bad(func(r *EngineResponse) { r.KindID = "some_other_kind" })},
		{"malformed: empty kind_id", bad(func(r *EngineResponse) { r.KindID = "" })},
		{"malformed: no model id", bad(func(r *EngineResponse) { r.Model = "" })},
		{"out-of-range confidence: 150 (never clamp)", bad(func(r *EngineResponse) { r.Confidence = 150 })},
		{"out-of-range confidence: 101", bad(func(r *EngineResponse) { r.Confidence = 101 })},
		{"out-of-range confidence: -1", bad(func(r *EngineResponse) { r.Confidence = -1 })},
	}
	for i, c := range cells {
		c := c
		t.Run(c.name, func(t *testing.T) {
			f := newSidecarFixture("sc_fault_" + string(rune('a'+i)))
			f.engine.setRecommend(c.fn)
			rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
			assertFallbackServed(t, rec, err)
			if rec.Confidence != 41 {
				t.Errorf("bad engine value leaked through: %+v", rec)
			}
		})
	}
}

func TestSidecarAdvisor_ContractsFetchFailure_FallsThrough_AndIsNotCached(t *testing.T) {
	f := newSidecarFixture("sc_contracts_err")
	f.engine.mu.Lock()
	f.engine.contractsErr = errors.New("contracts unreachable")
	f.engine.mu.Unlock()
	rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	assertFallbackServed(t, rec, err)

	f.engine.mu.Lock()
	f.engine.contractsErr = nil
	f.engine.mu.Unlock()
	rec, err = f.recommend(t, toyFeatures{N: 2}, sidecarSess)
	if err != nil || rec.Model != "kenaz-ml/classic-abc12345" {
		t.Fatalf("a failed contracts fetch was remembered: %+v, %v", rec, err)
	}
}

func TestSidecarAdvisor_NonObjectFeatures_FallThrough(t *testing.T) {
	f := newSidecarFixture("sc_nonobject")
	rec, err := f.adv.Recommend(context.Background(), f.kind, "a-bare-string", sidecarSess)
	assertFallbackServed(t, rec, err)
	if _, r := f.engine.counts(); r != 0 {
		t.Errorf("unserializable features still reached the engine")
	}
}

// ---- the 800ms budget ----

// TestSidecarAdvisor_DefaultBudgetIs800ms pins the constant the spec names.
func TestSidecarAdvisor_DefaultBudgetIs800ms(t *testing.T) {
	if DefaultAdvisorBudget != 800*time.Millisecond {
		t.Fatalf("DefaultAdvisorBudget = %v, want 800ms (spec §2)", DefaultAdvisorBudget)
	}
	if a := NewSidecarAdvisor(nil, nil, nil); a.budget != DefaultAdvisorBudget {
		t.Fatalf("a default-constructed SidecarAdvisor's budget = %v, want %v", a.budget, DefaultAdvisorBudget)
	}
}

// hangingEngine blocks until ctx is cancelled OR hang elapses, recording
// which branch released it. The hang is far longer than any budget, so the
// ONLY way Recommend can return promptly is the budget's ctx reaching the
// call — the ordering property, no wall-clock launch assertion.
func hangingRecommend(released chan<- string, hang time.Duration) func(context.Context, EngineRequest) (EngineResponse, error) {
	return func(ctx context.Context, _ EngineRequest) (EngineResponse, error) {
		select {
		case <-ctx.Done():
			released <- "ctx"
			return EngineResponse{}, ctx.Err()
		case <-time.After(hang):
			released <- "hang"
			return EngineResponse{Decision: boolp(true), Confidence: 99}, nil
		}
	}
}

// TestSidecarAdvisor_Budget_BoundsTheEngineCall_DefaultBudget drives the
// REAL default budget against an engine that would hang for 5s (6x the
// budget): the call must be released by ctx cancellation, and Recommend
// must still hand back the fallback's answer.
func TestSidecarAdvisor_Budget_BoundsTheEngineCall_DefaultBudget(t *testing.T) {
	f := newSidecarFixture("sc_budget") // default budget: 800ms
	released := make(chan string, 1)
	f.engine.setRecommend(hangingRecommend(released, 5*time.Second))

	type result struct {
		rec Recommendation
		err error
	}
	done := make(chan result, 1)
	go func() {
		rec, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
		done <- result{rec, err}
	}()

	select {
	case r := <-done:
		assertFallbackServed(t, r.rec, r.err)
	case <-time.After(4 * time.Second): // strictly below the 5s hang, generous against CI stalls
		t.Fatal("Recommend did not return within 4s — the 800ms budget is not bounding the engine call")
	}
	select {
	case how := <-released:
		if how != "ctx" {
			t.Fatalf("the engine call was released by %q, not by the budget's ctx cancellation", how)
		}
	default:
		t.Fatal("the engine call never observed cancellation")
	}
}

// TestSidecarAdvisor_Budget_ContractsFetchIsInsideTheBudgetToo: a hung
// /v1/contracts is bounded by the same budget (one ctx covers both calls).
func TestSidecarAdvisor_Budget_ContractsFetchIsInsideTheBudgetToo(t *testing.T) {
	kind := toyAdviceKind("sc_budget_contracts", "v1", SafetyReversible)
	released := make(chan string, 1)
	engine := &hangingContractsEngine{released: released}
	fb := NewHeuristicAdvisor()
	fh := &fixedHeuristic{confidence: 41, modelID: "heuristic/toy-v1"}
	fb.RegisterHeuristic(kind.ID, fh.fn)
	adv := NewSidecarAdvisor(engine, healthyProbe(), fb, WithSidecarBudget(40*time.Millisecond))

	done := make(chan struct{})
	var rec Recommendation
	var err error
	go func() {
		rec, err = adv.Recommend(context.Background(), kind, toyFeatures{N: 1}, sidecarSess)
		close(done)
	}()
	select {
	case <-done:
		assertFallbackServed(t, rec, err)
	case <-time.After(4 * time.Second):
		t.Fatal("a hung /v1/contracts was not bounded by the budget")
	}
	if how := <-released; how != "ctx" {
		t.Fatalf("contracts call released by %q, want ctx", how)
	}
}

type hangingContractsEngine struct{ released chan string }

func (e *hangingContractsEngine) Contracts(ctx context.Context) (map[string]EngineKindContract, error) {
	select {
	case <-ctx.Done():
		e.released <- "ctx"
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		e.released <- "hang"
		return nil, errors.New("hung")
	}
}
func (e *hangingContractsEngine) Recommend(context.Context, EngineRequest) (EngineResponse, error) {
	return EngineResponse{}, errors.New("unreachable")
}

// TestSidecarAdvisor_BudgetIgnoresCallerCtx mirrors LLMAdvisor's
// TimeoutIgnoresCallerCtx (risk.LLMRater's v0.78.2 rationale): a caller
// ctx that is already cancelled must not produce a false instant timeout.
func TestSidecarAdvisor_BudgetIgnoresCallerCtx(t *testing.T) {
	f := newSidecarFixture("sc_cancelled_ctx")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rec, err := f.adv.Recommend(cancelled, f.kind, toyFeatures{N: 1}, sidecarSess)
	if err != nil || rec.Model != "kenaz-ml/classic-abc12345" {
		t.Fatalf("an already-cancelled caller ctx produced %+v, %v; the engine call must still complete", rec, err)
	}
}

// ---- port guarantees ----

func TestSidecarAdvisor_CacheHit_OneEngineCall(t *testing.T) {
	f := newSidecarFixture("sc_cache")
	first, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if err != nil || first.CacheHit {
		t.Fatalf("first call: %+v, %v", first, err)
	}
	second, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if err != nil || !second.CacheHit {
		t.Fatalf("second identical call: %+v, %v; want a cache hit", second, err)
	}
	if _, r := f.engine.counts(); r != 1 {
		t.Errorf("engine Recommend called %d times for two identical calls, want 1", r)
	}
	// The stored entry was not mutated by the hit.
	third, _ := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if !third.CacheHit || first.CacheHit {
		t.Errorf("CacheHit leaked into the stored entry")
	}
}

func TestSidecarAdvisor_DismissSkipsFutureCalls_AcrossBothLayers(t *testing.T) {
	f := newSidecarFixture("sc_dismiss")
	if _, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess); err != nil {
		t.Fatal(err)
	}
	f.adv.Dismiss(sidecarSess, f.kind, toyFeatures{N: 1})
	_, r0 := f.engine.counts()

	_, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess)
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("a dismissed recommendation came back: %v", err)
	}
	if _, r1 := f.engine.counts(); r1 != r0 {
		t.Error("the engine was called for a dismissed recommendation")
	}

	// The dismissal also holds when the engine is DOWN and the fallback
	// would otherwise answer (Dismiss fed the fallback's tombstone too).
	f.probe.healthy.Store(false)
	if _, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("dismissal did not survive an engine outage: %v", err)
	}
	if f.fh.calls != 0 {
		t.Errorf("fallback heuristic ran %d times for a dismissed recommendation", f.fh.calls)
	}

	// Dismissing before the engine ever answered still suppresses the
	// fallback when the engine is down.
	g := newSidecarFixture("sc_dismiss_fb")
	g.probe.healthy.Store(false)
	g.adv.Dismiss(sidecarSess, g.kind, toyFeatures{N: 7})
	if _, err := g.recommend(t, toyFeatures{N: 7}, sidecarSess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("fallback-layer dismissal lost: %v", err)
	}
}

func TestSidecarAdvisor_MootSkipsEverything(t *testing.T) {
	f := newSidecarFixture("sc_moot")
	_, err := f.recommend(t, toyFeatures{N: 1}, SessionContext{SessionID: "s", Moot: true})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("moot Recommend returned %v, want ErrNoAdvice", err)
	}
	if c, r := f.engine.counts(); c != 0 || r != 0 || f.fh.calls != 0 {
		t.Errorf("moot skip still did work: contracts=%d recommend=%d heuristic=%d", c, r, f.fh.calls)
	}
}

func TestSidecarAdvisor_KindGateDisablesIndependently(t *testing.T) {
	f := newSidecarFixture("sc_gate")
	var on atomic.Bool
	f.adv.SetKindGate("sc_gate", on.Load)
	if _, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("a disabled kind was served: %v", err)
	}
	if c, r := f.engine.counts(); c != 0 || r != 0 || f.fh.calls != 0 {
		t.Errorf("a disabled kind still did work: %d/%d/%d", c, r, f.fh.calls)
	}
	on.Store(true)
	if _, err := f.recommend(t, toyFeatures{N: 1}, sidecarSess); err != nil {
		t.Fatalf("re-enabled kind: %v", err)
	}
	f.adv.SetKindGate("sc_gate", nil)
	if _, err := f.recommend(t, toyFeatures{N: 2}, sidecarSess); err != nil {
		t.Fatalf("nil gate = always enabled: %v", err)
	}
}

func TestSidecarAdvisor_CacheNeverCrossesSessions(t *testing.T) {
	f := newSidecarFixture("sc_sessions")
	a, _ := f.recommend(t, toyFeatures{N: 1}, SessionContext{SessionID: "A"})
	b, _ := f.recommend(t, toyFeatures{N: 1}, SessionContext{SessionID: "B"})
	if a.CacheHit || b.CacheHit {
		t.Fatal("a cache entry crossed sessions")
	}
	if _, r := f.engine.counts(); r != 2 {
		t.Errorf("engine calls = %d, want 2 (one per session)", r)
	}
	f.adv.Dismiss(SessionContext{SessionID: "A"}, f.kind, toyFeatures{N: 1})
	if _, err := f.recommend(t, toyFeatures{N: 1}, SessionContext{SessionID: "B"}); err != nil {
		t.Errorf("session A's dismissal suppressed session B: %v", err)
	}
}

func TestSidecarAdvisor_NilAdvisorDegradesToNoAdvice(t *testing.T) {
	var a *SidecarAdvisor
	_, err := a.Recommend(context.Background(), toyAdviceKind("sc_nil", "v1", SafetyReversible), toyFeatures{}, sidecarSess)
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("nil advisor: %v", err)
	}
	a.Dismiss(sidecarSess, toyAdviceKind("sc_nil", "v1", SafetyReversible), toyFeatures{}) // must not panic
	a.SetKindGate("x", nil)
}

func TestSidecarAdvisor_NoFallback_EngineMissDegradesToNoAdvice(t *testing.T) {
	kind := toyAdviceKind("sc_nofb", "v1", SafetyReversible)
	engine := &fakeEngine{contracts: servedContracts(kind.ID)}
	engine.setRecommend(func(context.Context, EngineRequest) (EngineResponse, error) {
		return EngineResponse{}, errors.New("down")
	})
	adv := NewSidecarAdvisor(engine, healthyProbe(), nil)
	if _, err := adv.Recommend(context.Background(), kind, toyFeatures{N: 1}, sidecarSess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("engine miss with no fallback: %v, want ErrNoAdvice", err)
	}
}

func TestSidecarAdvisor_ContractsView_IsCachedWithinTTL_AndRefreshedAfter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	f := newSidecarFixture("sc_ttl", WithSidecarContractsTTL(time.Minute), withSidecarClock(clock))
	for i := 1; i <= 3; i++ {
		if _, err := f.recommend(t, toyFeatures{N: i}, sidecarSess); err != nil {
			t.Fatal(err)
		}
	}
	if c, _ := f.engine.counts(); c != 1 {
		t.Errorf("contracts fetched %d times within the TTL, want 1", c)
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	if _, err := f.recommend(t, toyFeatures{N: 9}, sidecarSess); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.engine.counts(); c != 2 {
		t.Errorf("contracts fetched %d times after the TTL, want a refresh (2)", c)
	}
}

func TestSidecarAdvisor_RaceSafe(t *testing.T) {
	f := newSidecarFixture("sc_race")
	// fixedHeuristic counts without synchronization; swap in a race-safe
	// fallback (mutex+snapshot discipline) for the concurrent run.
	fb := NewHeuristicAdvisor()
	var hcalls atomic.Int32
	fb.RegisterHeuristic("sc_race", func(Features) (bool, int, string, error) {
		hcalls.Add(1)
		return false, 41, "heuristic/toy-v1", nil
	})
	f.adv.fallback = fb
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess := SessionContext{SessionID: "s"}
			_, _ = f.adv.Recommend(context.Background(), f.kind, toyFeatures{N: i % 4}, sess)
			if i%5 == 0 {
				f.adv.Dismiss(sess, f.kind, toyFeatures{N: i % 4})
			}
			f.probe.healthy.Store(i%3 != 0)
		}(i)
	}
	wg.Wait()
}
