package labels

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// CaptureAdvisor decorates a production advice.Advisor so every
// Recommend outcome — including below-75 and decision=false
// recommendations the WP07 chip gate never shows — lands an
// advice_labels row (spec §4, design §5.2's label-capture bridge). It
// implements advice.Advisor itself, so a caller (core/rpc/api.go's
// newLLMStack) can substitute it for the raw HeuristicAdvisor
// transparently — every existing Recommend/Dismiss call site keeps
// working unchanged.
//
// AC-06 ("capture toggle OFF => zero rows written, call-count proof")
// is enforced here, not in Store: Enabled() is checked BEFORE any Store
// method is ever called, so a disabled capture toggle produces zero
// calls into the store, not merely zero rows persisted by a no-op body.
type CaptureAdvisor struct {
	inner   advice.Advisor
	store   Store
	enabled func() bool
	now     func() time.Time
	// afterWrite, when set, fires after each successful label write (see
	// WithAfterWrite).
	afterWrite func()
}

// CaptureAdvisorOption tunes a CaptureAdvisor at construction time.
type CaptureAdvisorOption func(*CaptureAdvisor)

// WithClock overrides the wall-clock source (tests only).
func WithClock(now func() time.Time) CaptureAdvisorOption {
	return func(c *CaptureAdvisor) {
		if now != nil {
			c.now = now
		}
	}
}

// NewCaptureAdvisor wraps inner. enabled is read fresh on every call
// (mirrors every other Settings-backed KindGate closure in
// core/rpc/api.go — a toggle flip takes effect on the very next call,
// no restart) — nil enabled defaults to "always enabled" so a caller
// that has no Settings surface yet still captures, matching
// AdviceLabelCaptureDisabled's spec default (ON).
func NewCaptureAdvisor(inner advice.Advisor, store Store, enabled func() bool, opts ...CaptureAdvisorOption) *CaptureAdvisor {
	c := &CaptureAdvisor{
		inner:   inner,
		store:   store,
		enabled: enabled,
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// WithAfterWrite registers fn to be called after every label write this
// CaptureAdvisor performs (a captured Recommend row or a recorded user
// action) — and ONLY when capture is on, so a disabled toggle never
// fires it. The WP14 push lane hangs its coalescing Nudge here: an
// event-driven trigger with no goroutine lifecycle of its own. fn must
// be cheap and non-blocking (it runs on the caller's Recommend/click
// path); nil is ignored.
func WithAfterWrite(fn func()) CaptureAdvisorOption {
	return func(c *CaptureAdvisor) {
		if fn != nil {
			c.afterWrite = fn
		}
	}
}

// compile-time witness that *CaptureAdvisor satisfies advice.Advisor.
var _ advice.Advisor = (*CaptureAdvisor)(nil)

// Inner returns the wrapped advisor — the layer CaptureAdvisor decorates.
// Exposed so the production composition (core/rpc's newLLMStack:
// Capture -> Sidecar -> Heuristic) can be asserted by a wiring test
// rather than trusted from a comment.
func (c *CaptureAdvisor) Inner() advice.Advisor {
	if c == nil {
		return nil
	}
	return c.inner
}

func (c *CaptureAdvisor) captureEnabled() bool {
	if c == nil || c.enabled == nil {
		return true
	}
	return c.enabled()
}

// Recommend implements advice.Advisor. Every non-error, non-cache-hit
// outcome is captured with UserAction=ActionIgnored (the default —
// updated in place by RecordAction/Dismiss when the user, or an
// auto-act call site, later acts on it). Cache hits are NOT re-captured:
// a cache hit reuses the identical features/decision this session
// already captured once (AC-03's cache proof — "one advisor call, one
// render"), so a second row would duplicate the exact same training
// example rather than add information. Recommend failures (ErrNoAdvice
// for any reason — moot, disabled, dismissed, no backend) produce no
// row: there is no recommendation to attribute a label to.
func (c *CaptureAdvisor) Recommend(ctx context.Context, kind advice.AdviceKind, features advice.Features, sess advice.SessionContext) (advice.Recommendation, error) {
	if c == nil || c.inner == nil {
		return advice.Recommendation{}, ErrCaptureDisabled
	}
	// The decision instant is stamped BEFORE the inner call and handed
	// down on ctx: an engine-routed advisor sends it as the request's ts,
	// and the row below records the SAME instant as created_at — the
	// engine's shadow join keys on (features_hash, ts), so the two must be
	// one value, not two clock reads.
	start := c.now()
	rec, err := c.inner.Recommend(advice.WithDecisionTime(ctx, start), kind, features, sess)
	latencyMS := c.now().Sub(start).Milliseconds()
	if err != nil {
		return rec, err
	}
	if rec.CacheHit {
		return rec, nil
	}
	if !c.captureEnabled() || c.store == nil {
		return rec, nil
	}
	featuresJSON, jerr := MarshalFeatures(features)
	if jerr != nil {
		// A features value that cannot marshal is already a defensive
		// dead end for advice.FeaturesHash upstream of this call (the
		// HeuristicAdvisor would have failed before ever returning a
		// Recommendation) — this branch exists for defense in depth
		// only. Skip the row rather than corrupt the corpus with an
		// empty feature vector; never fail the caller's Recommend.
		return rec, nil
	}
	row := Row{
		KindID:           kind.ID,
		PromptVersion:    rec.PromptVersion,
		FeaturesHash:     mustHash(features),
		FeaturesJSON:     featuresJSON,
		FeaturesComplete: featuresComplete(features),
		ModelID:          rec.Model,
		Rung:             string(rec.Rung),
		Decision:         rec.Decision,
		Confidence:       rec.Confidence,
		Shown:            advice.ShouldShowChip(rec),
		UserAction:       ActionIgnored,
		LatencyMS:        latencyMS,
		SessionID:        sess.SessionID,
		CreatedAt:        start,
	}
	if ierr := c.store.Insert(ctx, row); ierr == nil && c.afterWrite != nil {
		c.afterWrite()
	}
	return rec, nil
}

// Dismiss implements advice.Advisor. Calls through to inner.Dismiss
// (preserving the cache-tombstone contract, AC-03) and then records the
// dismissal against the matching captured row, when capture is on.
func (c *CaptureAdvisor) Dismiss(sess advice.SessionContext, kind advice.AdviceKind, features advice.Features) {
	if c == nil {
		return
	}
	if c.inner != nil {
		c.inner.Dismiss(sess, kind, features)
	}
	c.RecordAction(context.Background(), sess, kind, features, ActionDismissed)
}

// RecordAction updates the captured row matching (sess.SessionID,
// kind.ID, hash(features)) to action. Production callers: WP07's chip
// accept handler (ActionAccepted) and the branch_now auto-act call site
// (ActionAutoActed) — both go through the SAME CaptureAdvisor instance
// chatAdvisor already is, so no separate wiring is needed beyond a type
// assertion at the call site. A no-op when capture is off or the
// features value cannot hash — never an error the caller must handle,
// matching every other Advisor-adjacent failure mode in this package
// (degrade silently, never block the user's click).
func (c *CaptureAdvisor) RecordAction(ctx context.Context, sess advice.SessionContext, kind advice.AdviceKind, features advice.Features, action UserAction) {
	if c == nil || c.store == nil || !c.captureEnabled() {
		return
	}
	hash, err := advice.FeaturesHash(features)
	if err != nil {
		return
	}
	if uerr := c.store.UpdateAction(ctx, sess.SessionID, kind.ID, hash, action); uerr == nil && c.afterWrite != nil {
		c.afterWrite()
	}
}

// RecordActionIfSupported is the call-site-agnostic entry point WP07's
// chip-accept RPC handler and the branch_now auto-act call site both use
// (core/rpc/api.go and its RPC views hold `advice.Advisor`, not the
// concrete *CaptureAdvisor — this helper does the type assertion once,
// here, so neither call site needs to know whether the production
// Advisor happens to be capture-wrapped). A no-op when advisor is not a
// *CaptureAdvisor (e.g. a test double, or a future backend that hasn't
// been wrapped) — never an error the caller must handle.
func RecordActionIfSupported(ctx context.Context, advisor advice.Advisor, sess advice.SessionContext, kind advice.AdviceKind, features advice.Features, action UserAction) {
	c, ok := advisor.(*CaptureAdvisor)
	if !ok || c == nil {
		return
	}
	c.RecordAction(ctx, sess, kind, features, action)
}

func mustHash(features advice.Features) string {
	h, err := advice.FeaturesHash(features)
	if err != nil {
		return ""
	}
	return h
}

// featuresComplete is the placeholder-row discriminator's bridge-side
// half (review promotion, laya-advisors-01LAYA001 WP07/WP08 review
// round, 2026-09-29): a type assertion against the kind-owned
// advice.FeaturesCompleteness interface, never a hardcoded kind id. A
// Features value that does not implement the interface (branch_now
// today — its decision-bearing fields are all real data) is treated as
// complete, matching the DB column's own DEFAULT 1.
func featuresComplete(features advice.Features) bool {
	fc, ok := features.(advice.FeaturesCompleteness)
	if !ok {
		return true
	}
	return fc.FeaturesComplete()
}
