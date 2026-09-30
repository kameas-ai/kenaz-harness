// LLMAdvisor (this file) is a DORMANT TEST DOUBLE, not a production
// backend.
//
// Owner ruling 2026-09-29 (spec §2e-0): "no LLMs — advisors use laya and
// other ML models only." The kenaz-ml integration design (research/
// kenaz-ml-integration-design.md §9 Phase 0) resolves WP01's deferred
// LLMAdvisor-disposition question this way: "demote to dormant test
// double now (it is the only in-repo Advisor exercising the port under
// fault injection); delete once SidecarAdvisor's fake covers the AC-02
// matrix." This file's suffix (_test.go) is that demotion, mechanically
// enforced by the Go toolchain rather than by convention: everything
// below compiles ONLY for `go test`, never into a production binary, so
// there is no way for core/rpc/api.go (or anything else) to accidentally
// reintroduce an LLM-backed advisor call site without a compile error.
//
// What's still here and why: LLMAdvisor remains the only Advisor
// implementation in this repo that exercises a REAL model call (timeout,
// registry error, malformed response, out-of-range confidence) — the
// exact AC-02-style fault-injection coverage risk.LLMRater's own test
// suite pioneered. HeuristicAdvisor (heuristic.go) needs none of that
// surface (no model, no I/O, nothing to time out or mis-parse), so this
// file's tests (llmadvisor_test.go) are the only place that contract is
// still proven end to end. Per the design doc, delete both files
// together once a future SidecarAdvisor ships a fake that covers the
// same matrix (laya-serve / kenaz-ml, Phase 1+) — not before, or that
// coverage class silently disappears.
//
// Production wiring: core/rpc/api.go's newLLMStack no longer constructs
// advice.NewLLMAdvisor for chatAdvisor — see the HeuristicAdvisor
// construction site (~newLLMStack, "laya-advisors-01LAYA001 WP04-06")
// for what chatAdvisor is today.
package advice

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/cost"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// defaultAdvisorTimeout is spec §2's deliberate deviation from
// risk.defaultRaterTimeout (5s): "advice is worthless late — unlike the
// rater nothing WAITS on it." 800ms bounds how long a single Recommend
// call may run before it degrades to ErrNoAdvice.
const defaultAdvisorTimeout = 800 * time.Millisecond

// LLMRegistry is the narrow slice of core/llm/registry.Registry an
// Advisor needs. Mirrors risk.LLMRegistry exactly (an interface, not the
// concrete registry type, so tests can inject a fake).
type LLMRegistry interface {
	Stream(ctx context.Context, req corellm.GenerationRequest) (corellm.Stream, error)
}

// ProfileResolver resolves the (profileID, model, rung, unbenchmarked)
// an LLMAdvisor's own LLM calls use. Mirrors risk.ProfileResolver's
// shape, widened to carry the ladder metadata Recommendation.Rung /
// Recommendation.Unbenchmarked stamp onto every fresh (non-cached)
// recommendation — the production wiring site (core/rpc/api.go) builds
// this by calling ResolveAdvisorModel against the live Settings.AdvisorModel
// value and provider profile list, exactly as newLLMStack's chatRiskRater
// closure calls risk.ResolveRaterModel.
type ProfileResolver func(ctx context.Context) (profileID, model string, rung ModelRung, unbenchmarked bool, ok bool)

// AdvisorOverhead is the running tally of an LLMAdvisor's own LLM
// cost/usage, mirrors risk.LLMRaterOverhead so the same per-session
// cost-panel machinery can surface advisor spend as a separate,
// filterable line (cost.KindAdvice) — WP01's "token/cost attribution
// separable in the usage readout" proof requirement. ByKind additionally
// breaks the call count out per AdviceKind.ID, since — unlike the single-
// purpose risk rater — one Advisor instance serves multiple independently
// disableable kinds (spec AC-02) and a dashboard needs to tell them apart
// without re-deriving it from raw audit events.
type AdvisorOverhead struct {
	Total              float64        `json:"total"`
	Currency           string         `json:"currency,omitempty"`
	Calls              int            `json:"calls"`
	IndeterminateCalls int            `json:"indeterminate_calls"`
	InputTokens        int            `json:"input_tokens"`
	OutputTokens       int            `json:"output_tokens"`
	ByKind             map[string]int `json:"by_kind,omitempty"`
}

// LLMAdvisor is the production Advisor: a bounded, session-scoped-cached
// LLM call generalized across every registered AdviceKind. See Advisor's
// doc comment (advisor.go) and this package's doc comment for the full
// failure contract.
type LLMAdvisor struct {
	reg      LLMRegistry
	resolver ProfileResolver
	timeout  time.Duration
	cache    *adviceCache
	overhead atomic.Pointer[AdvisorOverhead]
}

// LLMAdvisorOption tunes an LLMAdvisor at construction time.
type LLMAdvisorOption func(*LLMAdvisor)

// WithLLMAdvisorTimeout overrides defaultAdvisorTimeout. d <= 0 is
// ignored. Production wiring does not call this; tests use it to
// exercise the timeout path within a bounded wall-clock budget.
func WithLLMAdvisorTimeout(d time.Duration) LLMAdvisorOption {
	return func(a *LLMAdvisor) {
		if d > 0 {
			a.timeout = d
		}
	}
}

// WithLLMAdvisorCacheCapacity overrides defaultAdviceCacheCapacity.
// n <= 0 is ignored.
func WithLLMAdvisorCacheCapacity(n int) LLMAdvisorOption {
	return func(a *LLMAdvisor) {
		if n > 0 {
			a.cache = newAdviceCache(n)
		}
	}
}

// NewLLMAdvisor wraps reg with the given resolver + options. Returns nil
// when reg is nil, mirroring risk.NewLLMRater's nil-safety contract so
// callers can chain `advice.NewLLMAdvisor(reg, resolver)` into a
// nil-tolerant field without an extra nil check.
func NewLLMAdvisor(reg LLMRegistry, resolver ProfileResolver, opts ...LLMAdvisorOption) *LLMAdvisor {
	if reg == nil {
		return nil
	}
	a := &LLMAdvisor{
		reg:      reg,
		resolver: resolver,
		timeout:  defaultAdvisorTimeout,
		cache:    newAdviceCache(defaultAdviceCacheCapacity),
	}
	a.overhead.Store(&AdvisorOverhead{})
	for _, o := range opts {
		o(a)
	}
	return a
}

// compile-time witness that *LLMAdvisor satisfies Advisor.
var _ Advisor = (*LLMAdvisor)(nil)

// Recommend implements Advisor. Every failure mode — nil advisor, no
// route to a model, a moot session, a dismissed cache entry, a timeout, a
// transport error, an unparseable response, an out-of-range confidence —
// returns an error wrapping ErrNoAdvice and a zero Recommendation. Never
// blocks the caller beyond a.timeout (default 800ms), and the timed call
// runs against a context derived from context.Background(), NOT ctx —
// exactly risk.LLMRater.Rate's v0.78.2 ShutdownServedCore rationale: a
// caller ctx that is already cancelled or about to cancel (a turn just
// stopped, a run winding down) must not race the advisor's own bound
// away and produce a false "instant timeout" or, worse, a zero-duration
// call that looks like a real attempt. ctx is used only for the
// cache/skip fast path, which does no I/O.
func (a *LLMAdvisor) Recommend(ctx context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error) {
	if a == nil || a.reg == nil {
		return Recommendation{}, fmt.Errorf("%w: no advisor configured", ErrNoAdvice)
	}
	if sess.Moot {
		// Skip path 1 (spec §2): the caller has already determined this
		// kind's answer cannot change anything for this dispatch. Zero
		// LLM calls, zero cache activity.
		return Recommendation{}, fmt.Errorf("%w: moot for this session", ErrNoAdvice)
	}

	hash, herr := FeaturesHash(features)
	if herr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, herr)
	}
	key := cacheKey{sessionID: sess.SessionID, kindID: kind.ID, featuresHash: hash, promptVersion: kind.PromptVersion}

	if entry, ok := a.cache.get(key); ok {
		if entry.dismissed {
			// Skip path 2 (AC-03): a materially identical recommendation
			// was already shown and dismissed in this session. Zero LLM
			// calls.
			return Recommendation{}, fmt.Errorf("%w: dismissed for materially identical features", ErrNoAdvice)
		}
		rec := entry.rec
		rec.CacheHit = true
		return rec, nil
	}

	profileID, model, rung, unbenchmarked, ok := a.resolveModel(ctx)
	if !ok {
		// No route to a model — AC-01: "with no laya available anywhere,
		// every session behaves exactly as today."
		return Recommendation{}, fmt.Errorf("%w: no advisor model resolved", ErrNoAdvice)
	}

	// Hard timeout derived from Background(), never ctx.
	callCtx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()

	system, user := kind.RenderPrompt(features)
	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Model:     model,
		System:    system,
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: user}}},
		},
	}

	stream, serr := a.reg.Stream(callCtx, req)
	if serr != nil {
		return Recommendation{}, fmt.Errorf("%w: advisor call: %v", ErrNoAdvice, serr)
	}
	for range stream.Events() {
		// Advice is a synchronous backend call; nothing fans deltas
		// anywhere (mirrors risk.LLMRater.Rate).
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		return Recommendation{}, fmt.Errorf("%w: advisor call: %v", ErrNoAdvice, ferr)
	}
	a.recordOverhead(kind.ID, resp)

	decision, confidence, perr := ParseRecommendation(flattenAdviceText(resp.Content))
	if perr != nil {
		return Recommendation{}, fmt.Errorf("%w: %v", ErrNoAdvice, perr)
	}

	rec := Recommendation{
		Decision:      decision,
		Confidence:    confidence,
		KindID:        kind.ID,
		PromptVersion: kind.PromptVersion,
		Model:         model,
		Rung:          rung,
		Unbenchmarked: unbenchmarked,
	}
	a.cache.put(key, rec)
	return rec, nil
}

// Dismiss implements Advisor. Records that features (for kind, in sess)
// has been shown and dismissed, so a later Recommend for materially
// identical features skips the model call (AC-03). Silently no-ops if
// features cannot be hashed — a caller that cannot even identify what it
// is dismissing has nothing actionable to record, and Dismiss (unlike
// Recommend) has no error return to surface that through.
func (a *LLMAdvisor) Dismiss(sess SessionContext, kind AdviceKind, features Features) {
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

// Overhead returns the running advisor cost/usage tally. Safe to call
// from any goroutine; returns a copy (including a copied ByKind map) so
// callers cannot mutate the counter.
func (a *LLMAdvisor) Overhead() AdvisorOverhead {
	if a == nil {
		return AdvisorOverhead{}
	}
	p := a.overhead.Load()
	if p == nil {
		return AdvisorOverhead{}
	}
	out := *p
	if p.ByKind != nil {
		out.ByKind = make(map[string]int, len(p.ByKind))
		for k, v := range p.ByKind {
			out.ByKind[k] = v
		}
	}
	return out
}

func (a *LLMAdvisor) resolveModel(ctx context.Context) (profileID, model string, rung ModelRung, unbenchmarked bool, ok bool) {
	if a.resolver == nil {
		return "", "", RungNone, true, false
	}
	return a.resolver(ctx)
}

// recordOverhead folds one call's cost + usage into the running
// AdvisorOverhead, tagged with cost.KindAdvice (plus a per-call
// "advice_kind" debug-log field) so downstream dashboards can break out
// advisor spend — and each kind's share of it — from every other LLM-call
// class.
func (a *LLMAdvisor) recordOverhead(kindID string, resp corellm.Response) {
	prev := a.overhead.Load()
	if prev == nil {
		prev = &AdvisorOverhead{}
	}
	next := *prev
	next.ByKind = make(map[string]int, len(prev.ByKind)+1)
	for k, v := range prev.ByKind {
		next.ByKind[k] = v
	}
	next.ByKind[kindID]++
	next.Calls++
	next.InputTokens += resp.Usage.InputTokens
	next.OutputTokens += resp.Usage.OutputTokens
	if resp.Cost.Indeterminate {
		next.IndeterminateCalls++
	} else {
		next.Total += resp.Cost.Total
		if next.Currency == "" && resp.Cost.Currency != "" {
			next.Currency = resp.Cost.Currency
		}
	}
	a.overhead.Store(&next)
	logging.L().Debug("advice.overhead",
		"cost_kind", cost.KindAdvice,
		"advice_kind", kindID,
		"call_total", resp.Cost.Total,
		"running_total", next.Total,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)
}

// flattenAdviceText concatenates every text-typed content block in
// declaration order. Mirrors risk.flattenRaterText.
func flattenAdviceText(blocks []corellm.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "" && blk.Type != "text" {
			continue
		}
		b.WriteString(blk.Text)
	}
	return strings.TrimSpace(b.String())
}
