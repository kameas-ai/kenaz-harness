// capture_rate.go — process-scoped memory capture-rate tracker.
//
// Design: a single global CaptureRateTracker holds a ring buffer of
// timestamps for the last N successful chunk writes (up to 120 slots,
// well beyond any 60-second window) so ChunksPerMinute can be computed
// exactly without a background goroutine.
//
// Thread-safety: all mutations go through a sync.Mutex. The ring buffer
// is small enough that the lock is never contended for more than a few
// microseconds.
//
// Embedder health is tracked via two atomic values:
//   - lastEmbedLatencyNs: the nanosecond duration of the most recent
//     embedder call (set by RecordEmbedCall).
//   - a small ring buffer of recent error timestamps (last 5 min window).
//
// No migrations: all state is in-process memory; it resets on restart.
package memory

import (
	"sync"
	"time"
)

const (
	// captureRingSize is the maximum number of write-timestamps retained.
	// 120 gives fine-grained 60-second windowing even at 2 writes/second.
	captureRingSize = 120

	// embedSlowThreshold is the latency beyond which the embedder is
	// reported as "slow" (spec: > 5s for the last attempt).
	embedSlowThreshold = 5 * time.Second

	// embedErrorWindow is the look-back window for the "error" health state.
	embedErrorWindow = 5 * time.Minute

	// embedErrorThreshold is the error count that triggers "error" state.
	embedErrorThreshold = 3
)

// CaptureRateTracker tracks memory capture velocity and embedder health.
// The zero value is ready to use.
type CaptureRateTracker struct {
	mu sync.Mutex

	// writeTimes holds the UTC timestamps of the last captureRingSize
	// successful chunk writes (oldest-first when full).
	writeTimes [captureRingSize]time.Time
	writeHead  int  // next write slot (ring index)
	writeCount int  // total writes ever (capped at ring size for ring-full detection)

	// lastEmbedDuration is the wall-clock duration of the most recent
	// embedder call. Zero means no call has been made yet.
	lastEmbedDuration time.Duration

	// embedErrors holds the timestamps of recent embedder errors so
	// ChunksPerMinute can classify "error" state without importing slog.
	embedErrors [captureRingSize]time.Time
	errHead     int
	errCount    int
}

// Global singleton used by the store's Add path and the RPC surface.
// Callers that need an isolated tracker (tests) construct one directly.
var globalCaptureTracker CaptureRateTracker

// GlobalCaptureTracker returns the process-scoped capture rate tracker.
// The pointer is stable for the process lifetime.
func GlobalCaptureTracker() *CaptureRateTracker { return &globalCaptureTracker }

// RecordWrite records one successful chunk write at the given time.
// Call this immediately after Store.Add returns nil.
func (t *CaptureRateTracker) RecordWrite(at time.Time) {
	t.mu.Lock()
	t.writeTimes[t.writeHead] = at
	t.writeHead = (t.writeHead + 1) % captureRingSize
	if t.writeCount < captureRingSize {
		t.writeCount++
	}
	t.mu.Unlock()
}

// RecordEmbedCall records the latency of one embedder call. Call this
// after every Embed() invocation regardless of success.
func (t *CaptureRateTracker) RecordEmbedCall(d time.Duration) {
	t.mu.Lock()
	t.lastEmbedDuration = d
	t.mu.Unlock()
}

// RecordEmbedError records one embedder error at the given time.
func (t *CaptureRateTracker) RecordEmbedError(at time.Time) {
	t.mu.Lock()
	t.embedErrors[t.errHead] = at
	t.errHead = (t.errHead + 1) % captureRingSize
	if t.errCount < captureRingSize {
		t.errCount++
	}
	t.mu.Unlock()
}

// CaptureRateSnapshot is the computed snapshot returned to the RPC surface.
type CaptureRateSnapshot struct {
	ChunksPerMinute  float64    // writes in the last 60s
	EmbedderHealth   string     // "ok" | "slow" | "error"
	LastErrorAt      *time.Time // nil when no recent error
	RecentErrorCount int        // errors in the last embedErrorWindow
}

// Snapshot computes the current capture-rate snapshot. now is injected
// so callers can pass time.Now().UTC() in production and a fixed time
// in tests.
func (t *CaptureRateTracker) Snapshot(now time.Time) CaptureRateSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	window := now.Add(-60 * time.Second)
	var count int
	for i := 0; i < t.writeCount; i++ {
		idx := (t.writeHead - 1 - i + captureRingSize) % captureRingSize
		ts := t.writeTimes[idx]
		if ts.IsZero() {
			break
		}
		if ts.After(window) {
			count++
		}
	}

	// Embedder health.
	errWindow := now.Add(-embedErrorWindow)
	var recentErrors int
	var lastErrAt *time.Time
	for i := 0; i < t.errCount; i++ {
		idx := (t.errHead - 1 - i + captureRingSize) % captureRingSize
		ts := t.embedErrors[idx]
		if ts.IsZero() {
			break
		}
		if ts.After(errWindow) {
			recentErrors++
			if lastErrAt == nil {
				cp := ts
				lastErrAt = &cp
			}
		}
	}

	health := "ok"
	if recentErrors >= embedErrorThreshold {
		health = "error"
	} else if t.lastEmbedDuration > embedSlowThreshold {
		health = "slow"
	}

	return CaptureRateSnapshot{
		ChunksPerMinute:  float64(count),
		EmbedderHealth:   health,
		LastErrorAt:      lastErrAt,
		RecentErrorCount: recentErrors,
	}
}
