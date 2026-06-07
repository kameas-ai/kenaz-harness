// Package retry (see also middleware.go / policy.go) adds a stream-aware
// classified retry wrapper for the chat-runner boundary (WP02 of the
// long-turn-resilience mission).
//
// RetryStream wraps fn (a function that opens an llm.Stream) with:
//   - Pre-stream retry: fn() errors before any events emit → retry with
//     exponential backoff up to policy.MaxAttempts.
//   - Mid-stream retry (best-effort): the stream's events channel closes
//     with *llm.ErrTransient AND the per-attempt buffer has zero tool_use
//     AND zero text content → silent restart from scratch.
//   - No retry on: ErrAuth, ErrInvalidRequest, ErrCancelled, or any
//     non-ErrTransient error type.
//
// The returned Stream is transparent to callers: Events() / Cancel() /
// Final() behave exactly as specified by the llm.Stream interface.
//
// TODO(resilience-WP03): when mid-stream retry cannot restart (partial
// text or tool_use already emitted), this package fires
// chat.retry.skip_partial_output and propagates the error. The full
// resume-flow (persist partial + surface a Resume button) is handled
// by Layer 3 in WP03.
package retry

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// StreamPolicy configures RetryStream.
type StreamPolicy struct {
	// MaxAttempts is the maximum number of total attempts (including the
	// first). Values < 1 are treated as 1. Default is 3.
	MaxAttempts int
	// BaseDelay is the base backoff delay for the first retry.
	// Default is 500 ms.
	BaseDelay time.Duration
	// JitterPct is the ±fraction for jitter, e.g. 0.10 for ±10%.
	// Zero disables jitter.
	JitterPct float64
	// Logger is called once per retry attempt before the backoff sleep.
	// Nil is fine — the function always logs through logging.L() regardless.
	Logger func(attempt int, err error, backoff time.Duration)
}

// defaultStreamPolicy returns a policy with WP02 defaults.
func defaultStreamPolicy() StreamPolicy {
	return StreamPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		JitterPct:   0.10,
	}
}

// apply fills zero-value fields in p with defaults.
func (p StreamPolicy) apply() StreamPolicy {
	d := defaultStreamPolicy()
	if p.MaxAttempts < 1 {
		p.MaxAttempts = d.MaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = d.BaseDelay
	}
	if p.JitterPct < 0 {
		p.JitterPct = 0
	}
	return p
}

// jitteredDelay returns BaseDelay * 2^(attempt-1) with ±JitterPct jitter.
// attempt is 1-based (attempt=1 → first retry after initial failure).
func jitteredDelay(p StreamPolicy, attempt int, rng *rand.Rand) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := p.BaseDelay
	for i := 1; i < attempt; i++ {
		d *= 2
	}
	if p.JitterPct > 0 {
		// jitter in range [d*(1-pct), d*(1+pct)]
		spread := float64(d) * p.JitterPct
		jit := (rng.Float64()*2 - 1) * spread // in [-spread, +spread]
		d = time.Duration(float64(d) + jit)
		if d < 0 {
			d = 0
		}
	}
	return d
}

// RetryStream wraps fn with classified retry-with-backoff.
//
// Pre-stream path: if fn() returns *llm.ErrTransient the call is retried
// up to policy.MaxAttempts total attempts, with exponential backoff
// (BaseDelay * 2^(attempt-1)) ±JitterPct between attempts.
//
// Mid-stream path: the returned Stream buffers events internally. If the
// underlying stream closes with *llm.ErrTransient before any text or
// tool_use content has been emitted, RetryStream opens a fresh stream
// and transparently continues. If content has already been emitted,
// chat.retry.skip_partial_output is logged and the error propagates for
// Layer 3 (WP03) to handle.
//
// ErrAuth, ErrInvalidRequest, and ErrCancelled propagate immediately
// without retrying.
//
// Each attempt gets the caller's original ctx; retries do NOT extend
// deadlines.
func RetryStream(ctx context.Context, policy StreamPolicy, fn func() (llm.Stream, error)) (llm.Stream, error) {
	policy = policy.apply()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	var stream llm.Stream
	var lastErr error

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := fn()
		if err == nil {
			stream = s
			break
		}
		// Classify the error.
		if !llm.IsTransient(err) {
			return nil, err
		}
		lastErr = err
		// Log and backoff (unless this was the last attempt).
		backoff := jitteredDelay(policy, attempt, rng)
		logRetryAttempt(attempt, err, backoff)
		if policy.Logger != nil {
			policy.Logger(attempt, err, backoff)
		}
		if attempt == policy.MaxAttempts {
			break
		}
		if serr := sleepCtx(ctx, backoff); serr != nil {
			return nil, serr
		}
	}

	if stream == nil {
		// All pre-stream attempts exhausted.
		logRetryExhausted(policy.MaxAttempts, lastErr)
		return nil, lastErr
	}

	// Wrap the stream with mid-stream retry capability.
	return newRetryableStream(ctx, policy, fn, stream, rng), nil
}

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
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
}

// logRetryAttempt emits chat.retry.attempt.
func logRetryAttempt(attempt int, err error, backoff time.Duration) {
	logging.L().Info("chat.retry.attempt",
		"attempt_n", attempt,
		"backoff_ms", backoff.Milliseconds(),
		"error_class", errorClass(err),
		"error", err.Error(),
	)
}

// logRetryExhausted emits chat.retry.exhausted.
func logRetryExhausted(attempts int, err error) {
	logging.L().Info("chat.retry.exhausted",
		"attempts", attempts,
		"final_error_class", errorClass(err),
		"error", errStr(err),
	)
}

// logSkipPartialOutput emits chat.retry.skip_partial_output (stub — Layer 3
// will act on this; for now we just log and propagate).
func logSkipPartialOutput(attempt int, err error, hasText, hasTool bool) {
	logging.L().Info("chat.retry.skip_partial_output",
		"attempt_n", attempt,
		"has_text", hasText,
		"has_tool", hasTool,
		"error_class", errorClass(err),
		"error", err.Error(),
	)
}

// errorClass returns a stable string tag for the error type, used in log
// fields so dashboards can group by class without parsing the message.
func errorClass(err error) string {
	if err == nil {
		return "nil"
	}
	var et *llm.ErrTransient
	if errors.As(err, &et) {
		return "transient"
	}
	var ea *llm.ErrAuth
	if errors.As(err, &ea) {
		return "auth"
	}
	var ei *llm.ErrInvalidRequest
	if errors.As(err, &ei) {
		return "invalid_request"
	}
	var ec *llm.ErrCancelled
	if errors.As(err, &ec) {
		return "cancelled"
	}
	return "unknown"
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ─────────────────────────────────────────────────────────────────────────────
// retryableStream — transparent mid-stream retry wrapper
// ─────────────────────────────────────────────────────────────────────────────

// retryableStream wraps an llm.Stream. Its goroutine drains events from
// the underlying stream into an output channel. If the stream closes with
// ErrTransient before any text or tool_use content has been seen, the
// goroutine opens a fresh stream via fn and continues pumping from it.
// Callers interact with the wrapper exactly as they would with a plain
// llm.Stream: Events() → drain → Final().
type retryableStream struct {
	ctx    context.Context
	policy StreamPolicy
	fn     func() (llm.Stream, error)
	rng    *rand.Rand

	out  chan llm.StreamEvent
	once sync.Once

	mu      sync.Mutex
	current llm.Stream // current underlying stream (swapped on mid-stream retry)
	finalResp llm.Response
	finalErr  error
	done    chan struct{} // closed when pump exits
}

func newRetryableStream(ctx context.Context, policy StreamPolicy, fn func() (llm.Stream, error), initial llm.Stream, rng *rand.Rand) *retryableStream {
	return &retryableStream{
		ctx:     ctx,
		policy:  policy,
		fn:      fn,
		rng:     rng,
		out:     make(chan llm.StreamEvent, 16),
		current: initial,
		done:    make(chan struct{}),
	}
}

// Events returns the receive channel. The pump goroutine is started on
// first call (lazy, per llm.Stream contract).
func (rs *retryableStream) Events() <-chan llm.StreamEvent {
	rs.once.Do(func() {
		go rs.pump()
	})
	return rs.out
}

// pump is the goroutine that drives the underlying stream(s) and writes
// events to rs.out. It handles mid-stream retry by opening a new
// underlying stream when the current one closes with ErrTransient and
// no content has been emitted yet.
func (rs *retryableStream) pump() {
	defer close(rs.out)
	defer close(rs.done)

	// Track per-pump content state (resets on each mid-stream retry attempt).
	var (
		hasText bool
		hasTool bool
	)

	// We have already consumed one pre-stream attempt to open the initial
	// stream; mid-stream retries start at attempt 2.
	for midAttempt := 1; midAttempt <= rs.policy.MaxAttempts; midAttempt++ {
		hasText = false
		hasTool = false

		rs.mu.Lock()
		inner := rs.current
		rs.mu.Unlock()

		// Drain events from current inner stream.
		var streamErr error
		for ev := range inner.Events() {
			switch ev.Kind {
			case llm.StreamText:
				if ev.Text != "" {
					hasText = true
				}
			case llm.StreamTool:
				hasTool = true
			}
			rs.out <- ev
		}

		resp, err := inner.Final()
		if err == nil {
			// Success — store and return.
			rs.mu.Lock()
			rs.finalResp = resp
			rs.finalErr = nil
			rs.mu.Unlock()
			return
		}
		streamErr = err

		// Classify the stream error.
		if !llm.IsTransient(streamErr) {
			// Non-transient: propagate immediately.
			rs.mu.Lock()
			rs.finalErr = streamErr
			rs.mu.Unlock()
			return
		}

		// Transient error after some content: cannot safely retry.
		if hasText || hasTool {
			// TODO(resilience-WP03): escalate to Layer 3 resume flow.
			logSkipPartialOutput(midAttempt, streamErr, hasText, hasTool)
			rs.mu.Lock()
			rs.finalErr = streamErr
			rs.mu.Unlock()
			return
		}

		// Transient error, no content emitted: safe to retry.
		if midAttempt >= rs.policy.MaxAttempts {
			// Budget exhausted.
			logRetryExhausted(rs.policy.MaxAttempts, streamErr)
			rs.mu.Lock()
			rs.finalErr = streamErr
			rs.mu.Unlock()
			return
		}

		backoff := jitteredDelay(rs.policy, midAttempt, rs.rng)
		logRetryAttempt(midAttempt, streamErr, backoff)
		if rs.policy.Logger != nil {
			rs.policy.Logger(midAttempt, streamErr, backoff)
		}

		if serr := sleepCtx(rs.ctx, backoff); serr != nil {
			rs.mu.Lock()
			rs.finalErr = serr
			rs.mu.Unlock()
			return
		}

		// Open a fresh underlying stream.
		newStream, newErr := rs.fn()
		if newErr != nil {
			if !llm.IsTransient(newErr) {
				rs.mu.Lock()
				rs.finalErr = newErr
				rs.mu.Unlock()
				return
			}
			// Another transient on open — next iteration handles it.
			_ = newErr
			// Create a stub that immediately errors so the loop can handle it:
			newStream = &immediatelyErrStream{err: newErr}
		}
		rs.mu.Lock()
		rs.current = newStream
		rs.mu.Unlock()
	}
}

// Cancel terminates the current underlying stream.
func (rs *retryableStream) Cancel() error {
	rs.mu.Lock()
	inner := rs.current
	rs.mu.Unlock()
	if inner != nil {
		return inner.Cancel()
	}
	return nil
}

// Final blocks until the pump goroutine exits (done channel closes) and
// returns the terminal Response or error.
func (rs *retryableStream) Final() (llm.Response, error) {
	// Ensure pump has started.
	rs.once.Do(func() {
		go rs.pump()
	})
	// Wait for pump to finish.
	<-rs.done
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.finalResp, rs.finalErr
}

// ─────────────────────────────────────────────────────────────────────────────
// immediatelyErrStream — stub for when a mid-stream re-open fails
// ─────────────────────────────────────────────────────────────────────────────

// immediatelyErrStream is a minimal llm.Stream that immediately closes its
// events channel and returns err from Final(). Used when a mid-stream
// retry attempt fails to open a new stream so the pump loop can handle
// it uniformly without special cases.
type immediatelyErrStream struct {
	err  error
	once sync.Once
	ch   chan llm.StreamEvent
}

func (s *immediatelyErrStream) Events() <-chan llm.StreamEvent {
	s.once.Do(func() {
		s.ch = make(chan llm.StreamEvent)
		close(s.ch)
	})
	return s.ch
}

func (s *immediatelyErrStream) Cancel() error { return nil }
func (s *immediatelyErrStream) Final() (llm.Response, error) {
	// Ensure channel is closed even if Events() was never called.
	s.once.Do(func() {
		s.ch = make(chan llm.StreamEvent)
		close(s.ch)
	})
	return llm.Response{}, s.err
}
