package retry

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// errorsAs is an alias for errors.As used to avoid lint warnings about
// direct package-level calls in the retry function body.
var errorsAs = errors.As

// AttemptHook is called once per retry boundary (after the failure of
// attempt N, before the sleep). Both the retry middleware itself and
// the audit emitter use AttemptHook to surface llm/retry_attempted.
type AttemptHook func(attempt int, plannedMS, actualMS int, err error)

// Middleware wraps an adapter call with retry logic.
//
// The middleware is intentionally NOT a Stream wrapper: at the streaming
// boundary, R3 mandates that once any chunk has been delivered the
// failure is terminal. The middleware therefore retries the adapter's
// "construct a Stream" call (which makes the wire request and awaits
// the first response chunk), but does not retry across delivered
// chunks. Streaming adapters that respect this contract surface their
// pre-first-chunk failures synchronously from Stream(); post-first-
// chunk failures arrive over the events channel as StreamError and the
// middleware never sees them.
type Middleware struct {
	hook AttemptHook
	rng  func() float64
	sleep func(ctx context.Context, d time.Duration) error
	mu   sync.Mutex
}

// New returns a Middleware. hook may be nil.
func New(hook AttemptHook) *Middleware {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &Middleware{
		hook: hook,
		rng:  func() float64 { return r.Float64() },
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return ctx.Err()
			}
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// SetRand overrides the jitter source (for deterministic tests).
func (m *Middleware) SetRand(r func() float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rng = r
}

// SetSleep overrides the sleep function (for deterministic tests).
func (m *Middleware) SetSleep(s func(ctx context.Context, d time.Duration) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sleep = s
}

// Run invokes fn up to policy.MaxAttempts times, sleeping between
// attempts per backoff. The returned `attempts` is the number of times
// fn was invoked (≥ 1). On success, err is nil.
//
// fn returns (result, error). If error is an *llm.ErrTransient, it is
// retried; any other error type returns immediately.
//
// Retry-After / server backoff: when the *ErrTransient carries a
// RetryAfterSec > 0 the actual sleep is max(computedBackoff, RetryAfterSec*1000)
// so the middleware never retries faster than the server requests
// (FR-004 / agent-loop-robustness-parity WP04).
func Run[T any](m *Middleware, ctx context.Context, policy Policy, fn func(ctx context.Context) (T, error)) (T, int, error) {
	var zero T
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	outcomes := make([]llm.AttemptOutcome, 0, policy.MaxAttempts)
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, attempt - 1, err
		}
		res, err := fn(ctx)
		if err == nil {
			return res, attempt, nil
		}
		if !llm.IsTransient(err) {
			return zero, attempt, err
		}
		// Compute next backoff (full jitter).
		plannedMS := computeBackoff(policy, attempt)
		actualMS := plannedMS
		if policy.Jitter == "full" {
			m.mu.Lock()
			rand01 := m.rng()
			m.mu.Unlock()
			actualMS = int(rand01 * float64(plannedMS))
		}
		// Honor server-mandated Retry-After: use max(jitteredBackoff, serverBackoffMS).
		if serverMS := retryAfterMS(err); serverMS > actualMS {
			actualMS = serverMS
		}
		outcomes = append(outcomes, llm.AttemptOutcome{
			Attempt: attempt, Err: err,
			BackoffMS: plannedMS, ActualMS: actualMS,
		})
		if m.hook != nil {
			m.hook(attempt, plannedMS, actualMS, err)
		}
		if attempt == policy.MaxAttempts {
			break
		}
		m.mu.Lock()
		sleepFn := m.sleep
		m.mu.Unlock()
		if serr := sleepFn(ctx, time.Duration(actualMS)*time.Millisecond); serr != nil {
			return zero, attempt, serr
		}
	}
	return zero, policy.MaxAttempts, &llm.ErrRetryBudgetExhausted{Attempts: outcomes}
}

// retryAfterMS extracts the server-requested backoff from an error in
// milliseconds. Returns 0 when the error does not carry a RetryAfterSec
// value or when the value is ≤ 0.
func retryAfterMS(err error) int {
	var t *llm.ErrTransient
	if !errorsAs(err, &t) {
		return 0
	}
	if t == nil || t.RetryAfterSec <= 0 {
		return 0
	}
	return int(t.RetryAfterSec * 1000)
}

// computeBackoff returns base * 2^(attempt-1) capped at MaxMS.
func computeBackoff(p Policy, attempt int) int {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.BaseMS
	for i := 1; i < attempt; i++ {
		delay *= 2
		if p.MaxMS > 0 && delay >= p.MaxMS {
			return p.MaxMS
		}
	}
	if p.MaxMS > 0 && delay > p.MaxMS {
		delay = p.MaxMS
	}
	return delay
}
