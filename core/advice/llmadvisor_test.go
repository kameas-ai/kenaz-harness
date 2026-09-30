package advice

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeAdviceStream is a minimal corellm.Stream implementation for tests,
// mirroring risk's fakeStream (core/policy/risk/llmrater_test.go).
type fakeAdviceStream struct {
	events chan corellm.StreamEvent
	resp   corellm.Response
	err    error
}

func newFakeAdviceStream(jsonText string) *fakeAdviceStream {
	ch := make(chan corellm.StreamEvent, 1)
	close(ch)
	return &fakeAdviceStream{
		events: ch,
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: jsonText}},
			FinishReason: "end_turn",
			Usage:        corellm.Usage{InputTokens: 50, OutputTokens: 10},
			Cost:         corellm.Cost{Currency: "USD", Total: 0.0001},
		},
	}
}

func (f *fakeAdviceStream) Events() <-chan corellm.StreamEvent { return f.events }
func (f *fakeAdviceStream) Cancel() error                      { return nil }
func (f *fakeAdviceStream) Final() (corellm.Response, error) {
	if f.err != nil {
		return corellm.Response{}, f.err
	}
	return f.resp, nil
}

// countingAdviceRegistry records call counts, mirroring risk's
// countingRegistry.
type countingAdviceRegistry struct {
	calls    int32
	stream   *fakeAdviceStream
	err      error
	lastReq  corellm.GenerationRequest
	onStream func(ctx context.Context)
}

func (r *countingAdviceRegistry) Stream(ctx context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	atomic.AddInt32(&r.calls, 1)
	r.lastReq = req
	if r.onStream != nil {
		r.onStream(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.stream, nil
}

func (r *countingAdviceRegistry) callCount() int { return int(atomic.LoadInt32(&r.calls)) }

func fixedAdvisorResolver(profileID, model string, rung ModelRung, unbenchmarked bool) ProfileResolver {
	return func(context.Context) (string, string, ModelRung, bool, bool) {
		return profileID, model, rung, unbenchmarked, true
	}
}

// toyAdviceKind returns a minimal, fully-valid AdviceKind for tests that
// don't care about a real extractor/renderer — just that Recommend's
// mechanics (cache, timeout, parsing) work against SOME kind.
func toyAdviceKind(id, promptVersion string, safety SafetyClass) AdviceKind {
	return AdviceKind{
		ID:            id,
		PromptVersion: promptVersion,
		SafetyClass:   safety,
		Extract:       func(input any) (Features, error) { return input, nil },
		RenderPrompt:  func(f Features) (string, string) { return "system", "user" },
	}
}

func TestLLMAdvisor_HappyPath(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 72}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("happy_kind", "v1", SafetyReversible)

	rec, err := adv.Recommend(context.Background(), kind, "features-a", SessionContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision || rec.Confidence != 72 {
		t.Errorf("got Decision=%v Confidence=%d, want true/72", rec.Decision, rec.Confidence)
	}
	if rec.Model != "m1" || rec.Rung != RungLocalLaya || !rec.Unbenchmarked {
		t.Errorf("got %+v", rec)
	}
	if reg.callCount() != 1 {
		t.Fatalf("registry called %d times, want 1", reg.callCount())
	}
}

// TestLLMAdvisor_CacheHit is WP01's proof requirement: two identical
// Recommend calls (same session, kind, features) must produce exactly
// one real advisor call.
func TestLLMAdvisor_CacheHit(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": false, "confidence": 30}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("cache_kind", "v1", SafetyReversible)
	sess := SessionContext{SessionID: "sess-cache"}

	r1, err := adv.Recommend(context.Background(), kind, "same-features", sess)
	if err != nil {
		t.Fatalf("first Recommend: %v", err)
	}
	r2, err := adv.Recommend(context.Background(), kind, "same-features", sess)
	if err != nil {
		t.Fatalf("second Recommend: %v", err)
	}
	if reg.callCount() != 1 {
		t.Fatalf("registry called %d times for two identical dispatches, want 1 (cache should have hit)", reg.callCount())
	}
	if r1.CacheHit {
		t.Errorf("first Recommend() reported CacheHit=true; it was the fresh compute")
	}
	if !r2.CacheHit {
		t.Errorf("second Recommend() reported CacheHit=false; it should have been served from cache")
	}
	r2.CacheHit = r1.CacheHit
	if r1 != r2 {
		t.Errorf("cached recommendation differs beyond CacheHit: %+v vs %+v", r1, r2)
	}
}

// TestLLMAdvisor_DismissSkipsFutureCalls is AC-03's direct proof: a
// dismissed recommendation is not re-shown for materially identical
// features within the session, and — critically — dismissing it makes
// NO further advisor call for the same (kind, features).
func TestLLMAdvisor_DismissSkipsFutureCalls(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 90}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("dismiss_kind", "v1", SafetyReversible)
	sess := SessionContext{SessionID: "sess-dismiss"}

	if _, err := adv.Recommend(context.Background(), kind, "features-x", sess); err != nil {
		t.Fatalf("first Recommend: %v", err)
	}
	adv.Dismiss(sess, kind, "features-x")

	_, err := adv.Recommend(context.Background(), kind, "features-x", sess)
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend after Dismiss: err = %v, want ErrNoAdvice", err)
	}
	if reg.callCount() != 1 {
		t.Fatalf("registry called %d times across dismiss+re-ask, want 1 (no re-show, no re-call)", reg.callCount())
	}
}

// TestLLMAdvisor_MootSkipsCallEntirely is the second skip path spec §2
// requires: when the caller has already determined the kind is moot
// (autonomy tier makes its action unreachable), Recommend must make ZERO
// advisor calls — not even a cache lookup that could later collide.
func TestLLMAdvisor_MootSkipsCallEntirely(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 50}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("moot_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "features-moot", SessionContext{SessionID: "sess-moot", Moot: true})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend with Moot=true: err = %v, want ErrNoAdvice", err)
	}
	if reg.callCount() != 0 {
		t.Fatalf("registry called %d times for a moot session, want 0", reg.callCount())
	}
}

// TestLLMAdvisor_Timeout is WP01's proof requirement: an advisor call
// that never returns must fail within the configured 800ms-class bound,
// derived from context.Background() — not the caller's ctx.
func TestLLMAdvisor_Timeout(t *testing.T) {
	blocked := make(chan struct{})
	reg := &countingAdviceRegistry{
		onStream: func(ctx context.Context) {
			<-ctx.Done()
			close(blocked)
		},
	}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true), WithLLMAdvisorTimeout(30*time.Millisecond))
	kind := toyAdviceKind("timeout_kind", "v1", SafetyReversible)

	start := time.Now()
	_, err := adv.Recommend(context.Background(), kind, "features-timeout", SessionContext{SessionID: "sess-timeout"})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice on timeout, got %v", err)
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("registry's ctx was never cancelled — timeout did not propagate")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Recommend took %s, want well under 2s for a 30ms configured timeout", elapsed)
	}
}

// TestLLMAdvisor_TimeoutIgnoresCallerCtx proves the timeout is derived
// from context.Background(), not forwarded from the caller — mirrors
// risk.TestLLMRater_TimeoutIgnoresCallerCtx's v0.78.2 rationale exactly.
func TestLLMAdvisor_TimeoutIgnoresCallerCtx(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 10}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true), WithLLMAdvisorTimeout(time.Second))
	kind := toyAdviceKind("cancelled_ctx_kind", "v1", SafetyReversible)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	rec, err := adv.Recommend(cancelledCtx, kind, "features-cancelled", SessionContext{SessionID: "sess-cancelled"})
	if err != nil {
		t.Fatalf("Recommend with an already-cancelled caller ctx should still complete via the real call; got error: %v", err)
	}
	if rec.Confidence != 10 {
		t.Errorf("Confidence = %d, want 10", rec.Confidence)
	}
	if reg.callCount() != 1 {
		t.Fatalf("registry called %d times, want 1", reg.callCount())
	}
}

func TestLLMAdvisor_UnparseableResponseDegradesToNoAdvice(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream("I refuse to answer in JSON.")}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("garbage_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "features-garbage", SessionContext{SessionID: "sess-garbage"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice for an unparseable response, got %v", err)
	}
}

func TestLLMAdvisor_OutOfRangeConfidenceDegradesToNoAdvice(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 150}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("oor_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "features-oor", SessionContext{SessionID: "sess-oor"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice for an out-of-range confidence (never clamp), got %v", err)
	}
}

func TestLLMAdvisor_RegistryErrorDegradesToNoAdvice(t *testing.T) {
	reg := &countingAdviceRegistry{err: errors.New("provider down")}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("regerr_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "features-regerr", SessionContext{SessionID: "sess-regerr"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice, got %v", err)
	}
}

func TestLLMAdvisor_NoModelResolvedDegradesToNoAdvice(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 1}`)}
	adv := NewLLMAdvisor(reg, nil) // no resolver wired
	kind := toyAdviceKind("noprofile_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "features-noprofile", SessionContext{SessionID: "sess-noprofile"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice when no model resolves (AC-01: behave exactly as today), got %v", err)
	}
}

func TestLLMAdvisor_NilRegistryReturnsNil(t *testing.T) {
	adv := NewLLMAdvisor(nil, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	if adv != nil {
		t.Errorf("NewLLMAdvisor(nil, ...) = non-nil, want nil")
	}
}

func TestLLMAdvisor_NilAdvisorRecommendDegradesToNoAdvice(t *testing.T) {
	var adv *LLMAdvisor
	kind := toyAdviceKind("nil_advisor_kind", "v1", SafetyReversible)
	_, err := adv.Recommend(context.Background(), kind, "x", SessionContext{SessionID: "sess-nil"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("expected ErrNoAdvice from a nil *LLMAdvisor, got %v", err)
	}
}

// TestLLMAdvisor_Overhead_TagsCostKindAndKindID verifies the running
// overhead tally updates and is separable both from other LLM-call
// classes (cost.KindAdvice) and per advice kind (ByKind) — WP01's
// "token/cost attribution separable in the usage readout" proof
// requirement.
func TestLLMAdvisor_Overhead_TagsCostKindAndKindID(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": true, "confidence": 60}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("cost_kind", "v1", SafetyReversible)

	if _, err := adv.Recommend(context.Background(), kind, "features-cost", SessionContext{SessionID: "sess-cost"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	overhead := adv.Overhead()
	if overhead.Calls != 1 {
		t.Errorf("Calls = %d, want 1", overhead.Calls)
	}
	if overhead.InputTokens != 50 || overhead.OutputTokens != 10 {
		t.Errorf("tokens = (%d, %d), want (50, 10)", overhead.InputTokens, overhead.OutputTokens)
	}
	if overhead.ByKind["cost_kind"] != 1 {
		t.Errorf("ByKind[%q] = %d, want 1: %+v", "cost_kind", overhead.ByKind["cost_kind"], overhead.ByKind)
	}
}

func TestLLMAdvisor_CacheNeverCrossesSessions(t *testing.T) {
	reg := &countingAdviceRegistry{stream: newFakeAdviceStream(`{"decision": false, "confidence": 20}`)}
	adv := NewLLMAdvisor(reg, fixedAdvisorResolver("p1", "m1", RungLocalLaya, true))
	kind := toyAdviceKind("crosssess_kind", "v1", SafetyReversible)

	if _, err := adv.Recommend(context.Background(), kind, "same-features", SessionContext{SessionID: "sess-a"}); err != nil {
		t.Fatalf("Recommend (sess-a): %v", err)
	}
	if _, err := adv.Recommend(context.Background(), kind, "same-features", SessionContext{SessionID: "sess-b"}); err != nil {
		t.Fatalf("Recommend (sess-b): %v", err)
	}
	if reg.callCount() != 2 {
		t.Fatalf("registry called %d times across two DIFFERENT sessions with identical features, want 2", reg.callCount())
	}
}
