package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// ─────────────────────────────────────────────────────────────────────────────
// test helpers
// ─────────────────────────────────────────────────────────────────────────────

// fakeStream is a controllable llm.Stream for tests.
type fakeStream struct {
	events  []llm.StreamEvent
	finalR  llm.Response
	finalE  error

	ch   chan llm.StreamEvent
	once sync.Once
}

func newFakeStream(events []llm.StreamEvent, finalErr error) *fakeStream {
	return &fakeStream{events: events, finalE: finalErr}
}

func (f *fakeStream) Events() <-chan llm.StreamEvent {
	f.once.Do(func() {
		f.ch = make(chan llm.StreamEvent, len(f.events)+1)
		for _, ev := range f.events {
			f.ch <- ev
		}
		close(f.ch)
	})
	return f.ch
}

func (f *fakeStream) Cancel() error { return nil }
func (f *fakeStream) Final() (llm.Response, error) {
	// Drain any remaining events so the channel is properly consumed
	// (callers are expected to drain Events() first).
	f.once.Do(func() {
		f.ch = make(chan llm.StreamEvent, len(f.events)+1)
		for _, ev := range f.events {
			f.ch <- ev
		}
		close(f.ch)
	})
	// Wait for events to be drained (or drain them ourselves).
	for range f.ch {
	}
	return f.finalR, f.finalE
}

// instantPolicy returns a policy that sleeps 0 ms (no backoff) for fast tests.
func instantPolicy(maxAttempts int) StreamPolicy {
	return StreamPolicy{
		MaxAttempts: maxAttempts,
		BaseDelay:   0,
		JitterPct:   0,
	}
}

// drainStream drains all events from s.Events() and then returns Final().
func drainStream(s llm.Stream) ([]llm.StreamEvent, llm.Response, error) {
	var evs []llm.StreamEvent
	for ev := range s.Events() {
		evs = append(evs, ev)
	}
	resp, err := s.Final()
	return evs, resp, err
}

// ─────────────────────────────────────────────────────────────────────────────
// Pre-stream retry tests
// ─────────────────────────────────────────────────────────────────────────────

// 503 → retried after ~500ms, succeeds on attempt 2.
func TestRetryStream_TransientThenSuccess(t *testing.T) {
	var calls int32
	successStream := newFakeStream([]llm.StreamEvent{{Kind: llm.StreamText, Text: "hello"}}, nil)
	fn := func() (llm.Stream, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return nil, &llm.ErrTransient{Status: 503, Message: "service unavailable"}
		}
		return successStream, nil
	}
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("RetryStream returned unexpected error: %v", err)
	}
	_, _, ferr := drainStream(s)
	if ferr != nil {
		t.Fatalf("Final() returned unexpected error: %v", ferr)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 fn calls (1 failure + 1 success), got %d", calls)
	}
}

// 401 (ErrAuth) → no retry, propagates immediately.
func TestRetryStream_AuthErrorNoRetry(t *testing.T) {
	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llm.ErrAuth{Status: 401, Message: "unauthorized"}
	}
	policy := instantPolicy(5) // high maxAttempts to prove no retry
	_, err := RetryStream(context.Background(), policy, fn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *ErrAuth, got %T: %v", err, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly 1 fn call, got %d", calls)
	}
}

// 400 (ErrInvalidRequest) → no retry, propagates.
func TestRetryStream_InvalidRequestNoRetry(t *testing.T) {
	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llm.ErrInvalidRequest{Status: 400, Message: "bad request"}
	}
	policy := instantPolicy(5)
	_, err := RetryStream(context.Background(), policy, fn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ie *llm.ErrInvalidRequest
	if !errors.As(err, &ie) {
		t.Fatalf("expected *ErrInvalidRequest, got %T: %v", err, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly 1 fn call, got %d", calls)
	}
}

// Context-cancelled mid-backoff → returns error fast.
func TestRetryStream_ContextCancelledMidBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the first sleep sees cancellation.
	cancel()

	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llm.ErrTransient{Status: 503, Message: "down"}
	}
	policy := StreamPolicy{
		MaxAttempts: 5,
		BaseDelay:   10 * time.Second, // long delay so cancellation is the bottleneck
		JitterPct:   0,
	}
	start := time.Now()
	_, err := RetryStream(ctx, policy, fn)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("RetryStream took too long (%v); expected context cancellation to short-circuit", elapsed)
	}
	// Either context error or transient on attempt 1 is acceptable;
	// the important thing is it doesn't loop.
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// 3 consecutive ErrTransient → final error propagates (last one).
func TestRetryStream_ThreeConsecutiveTransient(t *testing.T) {
	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llm.ErrTransient{Status: 503, Message: "down"}
	}
	policy := instantPolicy(3)
	_, err := RetryStream(context.Background(), policy, fn)
	if err == nil {
		t.Fatal("expected error after 3 transient failures")
	}
	var et *llm.ErrTransient
	if !errors.As(err, &et) {
		t.Fatalf("expected *ErrTransient, got %T: %v", err, err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("expected 3 fn calls, got %d", calls)
	}
}

// Jitter is in range [base*(1-jitter), base*(1+jitter)] across ~10 attempts.
func TestRetryStream_JitterInRange(t *testing.T) {
	const base = 500 * time.Millisecond
	const jitterPct = 0.10
	lo := time.Duration(float64(base) * (1 - jitterPct))
	hi := time.Duration(float64(base) * (1 + jitterPct))

	// Run 20 trials, each of which makes fn fail once so we see backoff.
	for trial := 0; trial < 20; trial++ {
		var calls int32
		fn := func() (llm.Stream, error) {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return nil, &llm.ErrTransient{Status: 503, Message: "down"}
			}
			return newFakeStream(nil, nil), nil
		}

		// Capture the actual backoff through the Logger hook.
		var observedBackoff time.Duration
		policy := StreamPolicy{
			MaxAttempts: 3,
			BaseDelay:   base,
			JitterPct:   jitterPct,
			Logger: func(attempt int, err error, backoff time.Duration) {
				observedBackoff = backoff
			},
		}
		// Use a cancellable context to short-circuit the actual sleep.
		ctx, cancel := context.WithCancel(context.Background())
		// Patch sleep by cancelling after a very short time.
		// We use a real RetryStream call but we don't want to wait 500ms.
		// Instead, run in a goroutine and cancel after the Logger fires.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Let the Logger fire first (it fires before sleep), then cancel.
			time.Sleep(5 * time.Millisecond)
			cancel()
		}()
		_, _ = RetryStream(ctx, policy, fn)
		wg.Wait()

		if observedBackoff == 0 {
			// No retry was triggered; skip this trial.
			continue
		}
		if observedBackoff < lo || observedBackoff > hi {
			t.Fatalf("trial %d: backoff %v out of range [%v, %v]", trial, observedBackoff, lo, hi)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Mid-stream retry tests
// ─────────────────────────────────────────────────────────────────────────────

// Mid-stream error before any delta → silent retry; final result from second stream.
func TestRetryStream_MidStreamTransientNoContent(t *testing.T) {
	var calls int32
	// First stream: no events, errors with ErrTransient on Final().
	firstStream := newFakeStream(nil, &llm.ErrTransient{Status: 502, Message: "bad gateway"})
	// Second stream: has content, succeeds.
	secondStream := newFakeStream([]llm.StreamEvent{{Kind: llm.StreamText, Text: "world"}}, nil)

	fn := func() (llm.Stream, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return firstStream, nil
		}
		return secondStream, nil
	}
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("RetryStream() error: %v", err)
	}
	evs, _, ferr := drainStream(s)
	if ferr != nil {
		t.Fatalf("Final() error: %v", ferr)
	}
	if len(evs) == 0 || evs[0].Text != "world" {
		t.Fatalf("expected text 'world' from retry stream, got %v", evs)
	}
	// fn should have been called twice: once for the initial stream, once for retry.
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 fn calls (initial + 1 mid-stream retry), got %d", calls)
	}
}

// Mid-stream error after text deltas → no retry, propagates error.
func TestRetryStream_MidStreamTransientWithText(t *testing.T) {
	var calls int32
	// Stream emits text then errors.
	errStream := newFakeStream(
		[]llm.StreamEvent{{Kind: llm.StreamText, Text: "partial text"}},
		&llm.ErrTransient{Status: 502, Message: "disconnected"},
	)
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return errStream, nil
	}
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("RetryStream() error: %v", err)
	}
	_, _, ferr := drainStream(s)
	if ferr == nil {
		t.Fatal("expected error from Final() after partial text mid-stream failure")
	}
	var et *llm.ErrTransient
	if !errors.As(ferr, &et) {
		t.Fatalf("expected *ErrTransient, got %T: %v", ferr, ferr)
	}
	// Only one call to fn: no retry because text was already emitted.
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 fn call (no retry after partial text), got %d", calls)
	}
}

// Mid-stream error after tool_use → no retry.
func TestRetryStream_MidStreamTransientWithToolUse(t *testing.T) {
	var calls int32
	toolStream := newFakeStream(
		[]llm.StreamEvent{
			{Kind: llm.StreamTool, Tool: &llm.ToolUse{ID: "t1", Name: "bash"}},
		},
		&llm.ErrTransient{Status: 502, Message: "disconnected"},
	)
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return toolStream, nil
	}
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("RetryStream() error: %v", err)
	}
	_, _, ferr := drainStream(s)
	if ferr == nil {
		t.Fatal("expected error from Final() after tool_use mid-stream failure")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 fn call (no retry after tool_use), got %d", calls)
	}
}

// 3 consecutive mid-stream transient failures with no content → ErrTransient propagates.
func TestRetryStream_MidStreamThreeTransients(t *testing.T) {
	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return newFakeStream(nil, &llm.ErrTransient{Status: 503, Message: "down"}), nil
	}
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("RetryStream() error: %v", err)
	}
	_, _, ferr := drainStream(s)
	if ferr == nil {
		t.Fatal("expected error after 3 mid-stream transient failures")
	}
	var et *llm.ErrTransient
	if !errors.As(ferr, &et) {
		t.Fatalf("expected *ErrTransient, got %T: %v", ferr, ferr)
	}
}

// Cancel propagates to the underlying stream.
func TestRetryStream_CancelPropagates(t *testing.T) {
	var cancelled int32
	inner := &cancelTrackingStream{
		onCancel: func() { atomic.StoreInt32(&cancelled, 1) },
	}
	fn := func() (llm.Stream, error) { return inner, nil }
	policy := instantPolicy(3)
	s, err := RetryStream(context.Background(), policy, fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = s.Cancel()
	if atomic.LoadInt32(&cancelled) != 1 {
		t.Fatal("Cancel() did not propagate to inner stream")
	}
}

// cancelTrackingStream calls onCancel when Cancel() is invoked.
type cancelTrackingStream struct {
	onCancel func()
	once     sync.Once
	ch       chan llm.StreamEvent
}

func (c *cancelTrackingStream) Events() <-chan llm.StreamEvent {
	c.once.Do(func() {
		c.ch = make(chan llm.StreamEvent)
		close(c.ch)
	})
	return c.ch
}
func (c *cancelTrackingStream) Cancel() error {
	if c.onCancel != nil {
		c.onCancel()
	}
	return nil
}
func (c *cancelTrackingStream) Final() (llm.Response, error) {
	c.once.Do(func() {
		c.ch = make(chan llm.StreamEvent)
		close(c.ch)
	})
	for range c.ch {
	}
	return llm.Response{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// jitteredDelay unit tests
// ─────────────────────────────────────────────────────────────────────────────

func TestJitteredDelay_ExponentialBase(t *testing.T) {
	// With no jitter, delays should be BaseDelay * 2^(attempt-1).
	p := StreamPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, JitterPct: 0}
	r := newTestRng(0)
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 500 * time.Millisecond},
		{2, 1000 * time.Millisecond},
		{3, 2000 * time.Millisecond},
		{4, 4000 * time.Millisecond},
	}
	for _, tc := range cases {
		got := jitteredDelay(p, tc.attempt, r)
		if got != tc.want {
			t.Fatalf("attempt=%d got=%v want=%v", tc.attempt, got, tc.want)
		}
	}
}

func TestJitteredDelay_JitterRange(t *testing.T) {
	const base = 500 * time.Millisecond
	const pct = 0.10
	lo := time.Duration(math.Round(float64(base) * (1 - pct)))
	hi := time.Duration(math.Round(float64(base) * (1 + pct)))

	p := StreamPolicy{MaxAttempts: 3, BaseDelay: base, JitterPct: pct}
	for trial := 0; trial < 100; trial++ {
		r := newRandomRng()
		d := jitteredDelay(p, 1, r)
		if d < lo || d > hi {
			t.Fatalf("trial %d: delay %v out of range [%v, %v]", trial, d, lo, hi)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers for deterministic / random RNGs in tests
// ─────────────────────────────────────────────────────────────────────────────

func newTestRng(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}

func newRandomRng() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}
