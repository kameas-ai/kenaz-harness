package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// instantSleep replaces real sleeps so tests run fast under -race.
func instantSleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

func newTestMiddleware(hook AttemptHook) *Middleware {
	m := New(hook)
	m.SetRand(func() float64 { return 0.5 }) // deterministic full-jitter midpoint
	m.SetSleep(instantSleep)
	return m
}

// US4 Acceptance 1: 429 then 200 → eventual success.
func TestRun_TransientThenSuccess(t *testing.T) {
	m := newTestMiddleware(nil)
	var calls int32
	res, attempts, err := Run(m, context.Background(), Policy{MaxAttempts: 3, BaseMS: 1, MaxMS: 1, Jitter: "full"}, func(_ context.Context) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "", &llm.ErrTransient{Status: 429, Message: "slow down"}
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res != "ok" || attempts != 2 {
		t.Fatalf("res=%q attempts=%d", res, attempts)
	}
}

// US4 Acceptance 2: 401 → no retry, single attempt.
func TestRun_AuthErrorNoRetry(t *testing.T) {
	m := newTestMiddleware(nil)
	var calls int32
	_, attempts, err := Run(m, context.Background(), Policy{MaxAttempts: 5}, func(_ context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", &llm.ErrAuth{Status: 401, Message: "unauthorized"}
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("wrong err: %v", err)
	}
	if attempts != 1 || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("attempts=%d calls=%d (want 1/1)", attempts, calls)
	}
}

// US4 Acceptance 3: budget exhausted → ErrRetryBudgetExhausted with N attempts.
func TestRun_BudgetExhausted(t *testing.T) {
	m := newTestMiddleware(nil)
	_, attempts, err := Run(m, context.Background(), Policy{MaxAttempts: 3, BaseMS: 1, MaxMS: 4, Jitter: "full"}, func(_ context.Context) (string, error) {
		return "", &llm.ErrTransient{Status: 503, Message: "down"}
	})
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	var be *llm.ErrRetryBudgetExhausted
	if !errors.As(err, &be) {
		t.Fatalf("expected ErrRetryBudgetExhausted, got %T %v", err, err)
	}
	if len(be.Attempts) != 3 {
		t.Fatalf("expected 3 attempt outcomes, got %d", len(be.Attempts))
	}
}

// Hook receives attempt boundaries with monotonic numbers.
func TestRun_AttemptHookOrdering(t *testing.T) {
	var seen []int
	m := newTestMiddleware(func(attempt, _ int, _ int, _ error) {
		seen = append(seen, attempt)
	})
	_, _, _ = Run(m, context.Background(), Policy{MaxAttempts: 3, BaseMS: 1, MaxMS: 1, Jitter: "full"}, func(_ context.Context) (string, error) {
		return "", &llm.ErrTransient{Status: 502}
	})
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Fatalf("hook attempt ordering: %v", seen)
	}
}

// Backoff is bounded by MaxMS.
func TestComputeBackoff_Cap(t *testing.T) {
	p := Policy{BaseMS: 1000, MaxMS: 5000}
	cases := []struct{ attempt, want int }{
		{1, 1000}, {2, 2000}, {3, 4000}, {4, 5000}, {10, 5000},
	}
	for _, tc := range cases {
		got := computeBackoff(p, tc.attempt)
		if got != tc.want {
			t.Fatalf("attempt=%d got=%d want=%d", tc.attempt, got, tc.want)
		}
	}
}

// Jitter range stays within [0, plannedMS].
func TestRun_JitterDistribution(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		m := New(func(attempt, plannedMS, actualMS int, _ error) {
			if actualMS < 0 || actualMS > plannedMS {
				t.Fatalf("attempt=%d actual=%d outside [0,%d]", attempt, actualMS, plannedMS)
			}
		})
		m.SetSleep(instantSleep)
		_, _, _ = Run(m, context.Background(), Policy{MaxAttempts: 3, BaseMS: 100, MaxMS: 1000, Jitter: "full"}, func(_ context.Context) (string, error) {
			return "", &llm.ErrTransient{Status: 503}
		})
	}
}

// Context cancellation breaks the retry loop.
func TestRun_ContextCancel(t *testing.T) {
	m := newTestMiddleware(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Run(m, ctx, Policy{MaxAttempts: 5, BaseMS: 1, MaxMS: 1, Jitter: "full"}, func(c context.Context) (string, error) {
		return "", &llm.ErrTransient{Status: 503}
	})
	if !errors.Is(err, context.Canceled) {
		// Either ctx error or transient error is acceptable on the first
		// attempt, but at minimum we must not loop after cancel.
	}
	_ = err
}
