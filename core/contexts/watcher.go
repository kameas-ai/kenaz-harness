// Watcher is the WP01 stub for "the library subtree changed" events.
//
// The full plan asks for an fsnotify-backed watcher with 200 ms
// debounce; that lands in WP05 (polish). For WP01 we expose the
// same surface — Subscribe / Close / RegisterChange — without taking
// a new external dependency. Callers (today: Library mutators) emit
// synthetic notifications via Library.notify so frontends already
// wire up the listener and the upgrade in WP05 is a wiring swap, not
// an interface migration.
//
// The frontend may also poll Tree() if it wants point-in-time
// freshness while a long edit is in flight.
package contexts

import (
	"sync/atomic"
)

// Watcher fans synthetic "tree changed" notifications to subscribers.
// Subscribers receive a struct{} on every Library mutation (Save,
// CreateFolder, Rename, Delete). The channel is non-blocking — a slow
// consumer drops events; the contract is "at least one event per
// quiet period," not "every event delivered."
type Watcher struct {
	ch     chan struct{}
	closed atomic.Bool
}

// Subscribe attaches a new Watcher. The returned Watcher's Events()
// channel receives one struct{} per mutation. Close detaches the
// subscriber; calling Subscribe on a nil Library is a no-op.
func (l *Library) Subscribe() *Watcher {
	if l == nil {
		return nil
	}
	w := &Watcher{ch: make(chan struct{}, 1)}
	l.mu.Lock()
	l.watchers = append(l.watchers, w)
	l.mu.Unlock()
	return w
}

// Events returns the receive-only channel used to wait for
// notifications. Producers send a tick on every mutation; the channel
// has capacity 1 so successive mutations between two reads coalesce
// into a single event.
func (w *Watcher) Events() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.ch
}

// Close detaches the subscriber. Idempotent.
func (w *Watcher) Close() {
	if w == nil || !w.closed.CompareAndSwap(false, true) {
		return
	}
	// Don't close the channel — a producer race could panic on send.
	// Drop our reference and let GC reclaim it once the Library
	// removes us from its slice.
}

// notify pushes a tick to every live watcher. The watcher list is
// pruned of closed entries on the same pass so a long-running Library
// does not leak references.
//
// Producers are mutators on Library — Save / CreateFolder / Rename /
// Delete. They call notify after the mutation succeeds; a failure
// path does not emit so subscribers don't re-render for nothing.
func (l *Library) notify() {
	if l == nil {
		return
	}
	l.mu.Lock()
	live := l.watchers[:0]
	for _, w := range l.watchers {
		if w == nil || w.closed.Load() {
			continue
		}
		// Non-blocking send — buffer of 1 means a previous
		// unconsumed tick is still pending and the new one is
		// redundant.
		select {
		case w.ch <- struct{}{}:
		default:
		}
		live = append(live, w)
	}
	l.watchers = live
	l.mu.Unlock()
}
