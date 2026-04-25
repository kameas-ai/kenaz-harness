// Package events implements the BootstrapEventSink used during storage
// startup and the swap-and-drain handshake that resolves the bootstrap
// cycle between core/storage and core/event (plan §4.6).
//
// This package is internal to core/storage so that the EventSink contract
// itself stays exported on the public surface while the buffer
// implementation remains a private detail.
package events

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sink mirrors the public storage.EventSink interface so this package can
// be imported from anywhere under core/storage without a cycle.
//
// TODO(WP05): consider re-exporting via storage so callers don't import
// this internal package directly. For now the storage Open implementation
// constructs a *BootstrapEventSink and stores it as storage.EventSink.
type Sink interface {
	Emit(ctx context.Context, kind string, payload map[string]any) error
}

// Buffered is the concrete BootstrapEventSink. Bounded ring buffer; when
// it overflows the oldest entry is dropped and a counter is incremented.
// On Drain, if any drops occurred, a synthetic events_dropped entry is
// appended with the dropped count, then the counter resets.
type Buffered struct {
	mu       sync.Mutex
	cap      int
	ring     []bufferedEvent
	head     int   // write index
	size     int   // current count
	dropped  int64 // dropped since last drain
	now      func() time.Time
	released bool
}

type bufferedEvent struct {
	When    time.Time
	Kind    string
	Payload map[string]any
}

// NewBuffered constructs a BootstrapEventSink with the given capacity.
// Capacity must be > 0; values <= 0 default to 1024.
func NewBuffered(capacity int) *Buffered {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Buffered{
		cap:  capacity,
		ring: make([]bufferedEvent, capacity),
		now:  time.Now,
	}
}

// Emit appends to the ring buffer. Never blocks.
func (b *Buffered) Emit(ctx context.Context, kind string, payload map[string]any) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return errors.New("events: bootstrap sink already drained and released")
	}
	// Defensive copy of payload so callers may safely mutate after Emit.
	p := make(map[string]any, len(payload))
	for k, v := range payload {
		p[k] = v
	}
	if b.size == b.cap {
		// Drop the oldest entry.
		b.dropped++
		// Overwrite at head; advance head; size stays at cap.
		b.ring[b.head] = bufferedEvent{When: b.now(), Kind: kind, Payload: p}
		b.head = (b.head + 1) % b.cap
		return nil
	}
	idx := (b.head + b.size) % b.cap
	b.ring[idx] = bufferedEvent{When: b.now(), Kind: kind, Payload: p}
	b.size++
	return nil
}

// Len reports the current buffered count (not including dropped).
func (b *Buffered) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// Dropped reports the number of events dropped since the last Drain.
func (b *Buffered) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// DrainTo invokes target.Emit for each buffered event in insertion order.
// If any events were dropped since the last drain, a synthetic
// events_dropped entry with key "count" is appended at the end. After a
// successful drain the buffer is released and subsequent Emits return an
// error — callers should treat the bootstrap sink as a one-shot resource.
//
// If any target.Emit call returns an error, the drain stops and that
// error is returned. The remaining buffered events are preserved so the
// caller may retry.
func (b *Buffered) DrainTo(ctx context.Context, target Sink) error {
	b.mu.Lock()
	if b.released {
		b.mu.Unlock()
		return errors.New("events: bootstrap sink already released")
	}
	// Snapshot the ring under the lock and clear it: anything that
	// arrives during the drain is a new arrival in the live buffer.
	// On failure we prepend the unflushed remainder ahead of those new
	// arrivals.
	size := b.size
	head := b.head
	cap := b.cap
	dropped := b.dropped
	snapshot := make([]bufferedEvent, size)
	for i := 0; i < size; i++ {
		snapshot[i] = b.ring[(head+i)%cap]
	}
	b.size = 0
	b.head = 0
	b.dropped = 0
	b.mu.Unlock()

	for i, ev := range snapshot {
		if err := target.Emit(ctx, ev.Kind, ev.Payload); err != nil {
			// Re-buffer the unflushed remainder so retries work.
			b.mu.Lock()
			remaining := snapshot[i:]
			// Capture any concurrent arrivals that landed in the live
			// buffer while we were draining.
			currentSize := b.size
			currentHead := b.head
			currentCap := b.cap
			newArrivals := make([]bufferedEvent, currentSize)
			for j := 0; j < currentSize; j++ {
				newArrivals[j] = b.ring[(currentHead+j)%currentCap]
			}
			// Repopulate: remaining (in original order) ahead of the
			// concurrent new arrivals.
			b.size = 0
			b.head = 0
			// Restore the dropped counter we cleared at snapshot time;
			// the synthetic events_dropped emit never ran.
			b.dropped += dropped
			for _, ev := range remaining {
				b.appendLocked(ev)
			}
			for _, ev := range newArrivals {
				b.appendLocked(ev)
			}
			b.mu.Unlock()
			return err
		}
	}

	if dropped > 0 {
		// Emit synthetic events_dropped after the real events. Keys
		// match the eventkinds payload schema.
		if err := target.Emit(ctx, "events_dropped", map[string]any{
			"count": dropped,
		}); err != nil {
			// If the synthetic emit fails, do not attempt to recover —
			// just surface. The synthetic is best-effort, and the real
			// events have already flushed.
			return err
		}
	}

	b.mu.Lock()
	// Anything that arrived during the drain stays in the live buffer
	// (size/head reflect those). Treat the ring as released only when
	// truly empty; otherwise leave the sink usable for the new arrivals
	// to be drained on a subsequent call.
	if b.size == 0 {
		b.released = true
		b.dropped = 0
	} else {
		// Subtract the dropped we already flushed so we don't
		// double-count on the next drain.
		b.dropped = b.dropped - dropped
		if b.dropped < 0 {
			b.dropped = 0
		}
	}
	b.mu.Unlock()
	return nil
}

// appendLocked adds an event to the ring without taking the mutex.
// Caller must hold b.mu.
func (b *Buffered) appendLocked(ev bufferedEvent) {
	if b.size == b.cap {
		b.dropped++
		b.ring[b.head] = ev
		b.head = (b.head + 1) % b.cap
		return
	}
	idx := (b.head + b.size) % b.cap
	b.ring[idx] = ev
	b.size++
}

// Released reports whether the sink has been drained and released.
func (b *Buffered) Released() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.released
}

// SetClock overrides the clock used to stamp buffered events. Test-only
// helper; production callers should leave this at the default (time.Now).
func (b *Buffered) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now != nil {
		b.now = now
	}
}
