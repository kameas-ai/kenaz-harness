package chat

// advice_hook_budget_test.go — the review-promoted budget test
// (laya-advisors-01LAYA001 WP07/WP08 review round, 2026-09-29): proves
// AdviceRecommendBudget (800ms) actually bounds a slow Recommend call
// through the REAL goroutine+timeout dispatch path — not just that the
// constant has the right value (advice_hook.go's own doc comment already
// asserted that in prose; nothing exercised it end to end).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// slowFakeAdvisor is a deliberately slow Advisor test double: Recommend
// blocks until EITHER ctx is cancelled OR a long, budget-exceeding delay
// elapses — mirroring a real network/model call that respects ctx
// cancellation cooperatively (core/advice/doc.go's own contract: "ctx
// derived from context.Background()"). It records, per call, how long
// it waited before observing cancellation, so the test can assert that
// duration is bounded near AdviceRecommendBudget rather than the fake's
// own much-longer delay.
type slowFakeAdvisor struct {
	delay          time.Duration
	mu             sync.Mutex
	cancelledIn    []time.Duration
	recommendCalls int
}

func (f *slowFakeAdvisor) Recommend(ctx context.Context, _ advice.AdviceKind, _ advice.Features, _ advice.SessionContext) (advice.Recommendation, error) {
	f.mu.Lock()
	f.recommendCalls++
	f.mu.Unlock()

	start := time.Now()
	select {
	case <-ctx.Done():
		elapsed := time.Since(start)
		f.mu.Lock()
		f.cancelledIn = append(f.cancelledIn, elapsed)
		f.mu.Unlock()
		return advice.Recommendation{}, ctx.Err()
	case <-time.After(f.delay):
		// A real Advisor would never reach here inside the budget window
		// — this branch exists only so the fake has a well-defined return
		// if ctx somehow never cancels (which would itself be the bug
		// this test exists to catch).
		return advice.Recommendation{Decision: true, Confidence: 99}, nil
	}
}

func (f *slowFakeAdvisor) Dismiss(advice.SessionContext, advice.AdviceKind, advice.Features) {}

func (f *slowFakeAdvisor) snapshot() (calls int, cancelledIn []time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Duration, len(f.cancelledIn))
	copy(out, f.cancelledIn)
	return f.recommendCalls, out
}

var _ advice.Advisor = (*slowFakeAdvisor)(nil)

// TestFireAdvice_SlowAdvisor_BoundedByRecommendBudget drives the SAME
// dispatch shape StartStream's real HookPostLLM registration uses
// (chat_runner.go: `go r.fireAdvice(...)`) against a fake Advisor whose
// Recommend call would otherwise block for 5 seconds — 6x
// AdviceRecommendBudget. Two things must both be true:
//
//  1. Launching fireAdvice (the turn-side dispatch) returns immediately
//     — proven by the launch itself completing in well under the fake's
//     delay, exactly like the one-line `go r.fireAdvice(...)` hook
//     callback does in production.
//  2. The advisor's own goroutine observes ctx cancellation within a
//     bound close to AdviceRecommendBudget, not the fake's full 5s delay
//     — proving the 800ms timeout in fireAdvice's own
//     context.WithTimeout(context.Background(), AdviceRecommendBudget)
//     call actually reaches the Advisor.Recommend call, not just that
//     the constant exists.
func TestFireAdvice_SlowAdvisor_BoundedByRecommendBudget(t *testing.T) {
	fake := &slowFakeAdvisor{delay: 5 * time.Second}
	r := &ChatRunner{
		cfg: Config{
			Advisor: fake,
			Broker:  &fakeAdviceBroker{},
			AdviceDeps: &AdviceDeps{
				BranchCount: func(context.Context, string) (int, error) { return 0, nil },
			},
		},
	}

	launchReturned := make(chan struct{})
	fireAdviceDone := make(chan struct{})
	launchStart := time.Now()
	go func() {
		// Mirrors chat_runner.go's real HookPostLLM registration:
		//   env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(...) {
		//       go r.fireAdvice(sID, ...)
		//   })
		// The turn-side callback's only job is to start this goroutine
		// and return — modeled here by closing launchReturned the
		// instant the `go` statement itself has been issued.
		go func() {
			r.fireAdvice("sess-1", "profile-1", "model-1", "help me with this", autonomy.TierDefault)
			close(fireAdviceDone)
		}()
		close(launchReturned)
	}()

	select {
	case <-launchReturned:
		if elapsed := time.Since(launchStart); elapsed > 500*time.Millisecond { // load-tolerant; proves non-blocking vs the 5s hang
			t.Errorf("turn-side dispatch took %v to return, want near-instant (it only starts a goroutine)", elapsed)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("turn-side dispatch (launching fireAdvice) never returned")
	}

	// fireAdvice itself must complete near AdviceRecommendBudget, not the
	// fake's 5s delay — this is the real proof the timeout reaches the
	// Advisor call. Bound generously above budget (2s) so this is never
	// flaky on a loaded CI runner, while staying far below the fake's 5s
	// delay so a regression that stops honoring the timeout still fails.
	select {
	case <-fireAdviceDone:
		// good — fireAdvice returned; check WHEN below via cancelledIn.
	case <-time.After(2 * time.Second):
		t.Fatal("fireAdvice did not return within 2s — AdviceRecommendBudget is not bounding the real dispatch path")
	}

	calls, cancelledIn := fake.snapshot()
	if calls == 0 {
		t.Fatal("slowFakeAdvisor.Recommend was never called")
	}
	if len(cancelledIn) == 0 {
		t.Fatal("Recommend never observed ctx cancellation — it returned via the 5s delay branch instead, meaning AdviceRecommendBudget's timeout never reached the call")
	}
	first := cancelledIn[0]
	if first < AdviceRecommendBudget {
		t.Errorf("ctx cancellation observed after %v, want >= AdviceRecommendBudget (%v)", first, AdviceRecommendBudget)
	}
	// Load tolerance: the property under test is that cancellation is
	// BOUNDED — observed well before the fake's 5s hang — not that
	// goroutine scheduling is prompt. A budget+500ms bound flaked on the
	// shared CI runner (PR #358); 3s keeps a decisive 2s margin below
	// the hang while tolerating scheduler stalls.
	if first > 3*time.Second {
		t.Errorf("ctx cancellation observed after %v, want well under the fake's 5s hang (budget %v)", first, AdviceRecommendBudget)
	}
}
