package retry

import (
	"context"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestRun_RetryAfterHonored verifies that when ErrTransient carries
// RetryAfterSec the middleware uses at least that many milliseconds as
// the actual backoff (FR-004 / WP04 acceptance: "a 429 with Retry-After
// waits the stated interval").
func TestRun_RetryAfterHonored(t *testing.T) {
	t.Parallel()

	const retryAfterSec = 2.0 // server requests 2 s

	// recordSleeps captures each sleep duration so we can assert the
	// Retry-After was honored.
	var sleeps []time.Duration
	m := New(nil)
	m.SetRand(func() float64 { return 0.0 }) // jitter factor = 0 → planned backoff
	m.SetSleep(func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})

	var attempt int
	_, _, _ = Run(m, context.Background(),
		Policy{MaxAttempts: 2, BaseMS: 50, MaxMS: 200, Jitter: "full"},
		func(_ context.Context) (string, error) {
			attempt++
			if attempt == 1 {
				// Return a 429 with Retry-After: 2 s.
				return "", &llm.ErrTransient{
					Status:        429,
					Message:       "rate limited",
					RetryAfterSec: retryAfterSec,
				}
			}
			return "ok", nil
		},
	)

	if len(sleeps) != 1 {
		t.Fatalf("expected 1 sleep, got %d: %v", len(sleeps), sleeps)
	}
	expectedMin := time.Duration(retryAfterSec * float64(time.Second))
	if sleeps[0] < expectedMin {
		t.Errorf("sleep = %v, want >= %v (Retry-After must be honored)", sleeps[0], expectedMin)
	}
}

// TestRun_RetryAfterZero verifies that when RetryAfterSec == 0 the
// middleware falls back to the computed jitter backoff without padding.
func TestRun_RetryAfterZero(t *testing.T) {
	t.Parallel()

	const baseMS = 100
	var sleeps []time.Duration
	m := New(nil)
	m.SetRand(func() float64 { return 1.0 }) // jitter = plannedMS
	m.SetSleep(func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})

	var attempt int
	_, _, _ = Run(m, context.Background(),
		Policy{MaxAttempts: 2, BaseMS: baseMS, MaxMS: 500, Jitter: "full"},
		func(_ context.Context) (string, error) {
			attempt++
			if attempt == 1 {
				// RetryAfterSec == 0 — no server hint.
				return "", &llm.ErrTransient{Status: 429, Message: "slow down"}
			}
			return "ok", nil
		},
	)

	if len(sleeps) != 1 {
		t.Fatalf("expected 1 sleep, got %d", len(sleeps))
	}
	// With jitter factor=1.0 (full = planned), backoff = baseMS * 2^0 = 100 ms.
	// No Retry-After → must not be padded to >= 2 s.
	if sleeps[0] >= 2*time.Second {
		t.Errorf("sleep = %v, should not be padded to 2 s when RetryAfterSec == 0", sleeps[0])
	}
}

// TestRetryAfterMS_ExtractsValue verifies the retryAfterMS helper.
func TestRetryAfterMS_ExtractsValue(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		wantMS int
	}{
		{"nil err", nil, 0},
		{"non-transient", &llm.ErrAuth{Status: 401}, 0},
		{"transient without header", &llm.ErrTransient{Status: 429}, 0},
		{"transient with 2s header", &llm.ErrTransient{Status: 429, RetryAfterSec: 2.0}, 2000},
		{"transient with 0.5s header", &llm.ErrTransient{Status: 429, RetryAfterSec: 0.5}, 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := retryAfterMS(tc.err)
			if got != tc.wantMS {
				t.Errorf("retryAfterMS() = %d ms, want %d ms", got, tc.wantMS)
			}
		})
	}
}
