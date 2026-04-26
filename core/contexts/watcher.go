// Package contexts — file-system watcher.
//
// WP05 upgrades the WP01 in-process notify stub to an fsnotify-backed
// watcher with 200 ms debounce + duplicate-event drop. Two emission
// paths feed the same debounce pipeline:
//
//  1. Library mutators (Save / CreateFolder / Rename / Delete) call
//     notifyOp after a successful disk operation. Subscribers see a
//     tick within ~debounceWindow of the call.
//  2. fsnotify events from external mutations (operator drops a file
//     in via Finder, a deploy script rewrites a sidecar) feed the
//     same channel via a background recursive walk that re-adds new
//     subdirectories as they appear.
//
// Debounce contract:
//   - Per-path timer: each event resets a 200 ms window for that path.
//   - Duplicate-drop: same path + same op while a window is open is
//     a no-op; the existing pending event still fires.
//   - Delete-then-create within the window collapses to a single
//     Modified event — the most common "atomic save" pattern (write
//     temp + rename) emits Remove + Create back-to-back and the user
//     should see one refresh, not two.
//
// Subscribers receive struct{} ticks via the existing Watcher.Events
// channel; tests asserting op-level semantics observe via OpEvents.
package contexts

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Op classifies a debounced filesystem mutation. The set is small on
// purpose: subscribers only care whether the tree changed, not the
// fine-grained mode bits fsnotify exposes.
type Op uint8

const (
	// OpCreated indicates a new file or folder appeared.
	OpCreated Op = iota + 1
	// OpModified indicates an existing file's content was rewritten.
	OpModified
	// OpDeleted indicates a file or folder went away.
	OpDeleted
)

// String reports a stable lowercase tag for logs / tests.
func (o Op) String() string {
	switch o {
	case OpCreated:
		return "created"
	case OpModified:
		return "modified"
	case OpDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// debounceWindow is the per-path quiet period before a pending event
// fans out to subscribers. 200 ms is the spec'd budget — short enough
// the UI feels responsive (≤ 500 ms perceived), long enough to coalesce
// a write-temp + rename atomic save into a single tick.
const debounceWindow = 200 * time.Millisecond

// Watcher fans debounced "tree changed" notifications to subscribers.
// The channel is non-blocking — a slow consumer drops events; the
// contract is "at least one event per quiet period," not "every event
// delivered."
type Watcher struct {
	ch     chan struct{}      // struct{} ticks for backwards-compatible consumers
	opCh   chan WatcherEvent  // typed events for tests + future per-op routing
	closed atomic.Bool
}

// WatcherEvent is the typed payload available via OpEvents. Path is
// slash-separated and relative to the library root so callers can
// compare against tree node paths directly.
type WatcherEvent struct {
	Path string
	Op   Op
}

// Subscribe attaches a new Watcher. The returned Watcher's Events()
// channel receives one struct{} per debounced mutation. Close detaches
// the subscriber; calling Subscribe on a nil Library is a no-op.
func (l *Library) Subscribe() *Watcher {
	if l == nil {
		return nil
	}
	w := &Watcher{
		ch:   make(chan struct{}, 1),
		opCh: make(chan WatcherEvent, 16),
	}
	l.mu.Lock()
	l.watchers = append(l.watchers, w)
	l.mu.Unlock()
	return w
}

// Events returns the receive-only channel used to wait for
// notifications. Producers send a tick on every debounced mutation;
// the channel has capacity 1 so successive mutations between two reads
// coalesce into a single event.
func (w *Watcher) Events() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.ch
}

// OpEvents returns the typed event channel. Capacity 16 — tests assert
// op + path; production consumers read Events() and re-fetch the tree.
func (w *Watcher) OpEvents() <-chan WatcherEvent {
	if w == nil {
		return nil
	}
	return w.opCh
}

// Close detaches the subscriber. Idempotent.
func (w *Watcher) Close() {
	if w == nil || !w.closed.CompareAndSwap(false, true) {
		return
	}
	// Don't close the channels — a producer race could panic on send.
	// Drop our reference and let GC reclaim them once the Library
	// removes us from its slice.
}

// fsWatcher is a process-singleton fsnotify-backed watcher. nil when
// fsnotify cannot construct a backend (sandboxed CI without inotify);
// the in-process notifyOp path still works in that case.
type fsWatcher struct {
	root  string
	inner *fsnotify.Watcher

	mu      sync.Mutex
	pending map[string]*pendingEvent

	stop chan struct{}
}

type pendingEvent struct {
	op    Op
	timer *time.Timer
}

// StartWatching wires an fsnotify recursive watch over the library
// root and feeds events into the same debounce pipeline used by
// Library mutators. Idempotent: a second call is a no-op. Errors
// constructing the backend (e.g. unsupported platform) downgrade to
// "in-process notifications only" — Save / CreateFolder / Rename /
// Delete still notify subscribers.
//
// Stop with StopWatching when the chassis shuts down; outstanding
// debounce timers fire one last time so subscribers don't miss the
// final tick.
func (l *Library) StartWatching() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.fsw != nil {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	inner, err := fsnotify.NewWatcher()
	if err != nil {
		// Sandboxed environments (some CI runners) refuse the inotify
		// syscall. Surface the error so callers can log it; the
		// notifyOp path still runs and Save / CreateFolder / Rename /
		// Delete continue to drive subscribers.
		return err
	}

	fsw := &fsWatcher{
		root:    l.root,
		inner:   inner,
		pending: make(map[string]*pendingEvent),
		stop:    make(chan struct{}),
	}
	l.mu.Lock()
	l.fsw = fsw
	l.mu.Unlock()

	// Walk the existing tree and add every directory to the watcher.
	// fsnotify is non-recursive on Linux/macOS; we maintain the set
	// manually as new subdirectories appear.
	if err := fsw.addRecursive(l.root); err != nil {
		_ = inner.Close()
		l.mu.Lock()
		l.fsw = nil
		l.mu.Unlock()
		return err
	}

	go fsw.run(l)
	return nil
}

// StopWatching tears down the fsnotify backend, flushing any pending
// debounce timers so subscribers see the final tick. Idempotent.
func (l *Library) StopWatching() {
	if l == nil {
		return
	}
	l.mu.Lock()
	fsw := l.fsw
	l.fsw = nil
	l.mu.Unlock()
	if fsw == nil {
		return
	}
	close(fsw.stop)
	_ = fsw.inner.Close()

	// Flush any pending debounced events before returning so the test
	// harness (and a real shutdown sequence) sees deterministic state.
	fsw.mu.Lock()
	defer fsw.mu.Unlock()
	for path, pe := range fsw.pending {
		if pe.timer != nil {
			pe.timer.Stop()
		}
		l.deliver(WatcherEvent{Path: path, Op: pe.op})
		delete(fsw.pending, path)
	}
}

// addRecursive registers absPath and every directory beneath it with
// the fsnotify watcher. Called on StartWatching and on every Create
// event for a directory so newly-mkdir'd subtrees stay observable.
func (f *fsWatcher) addRecursive(absPath string) error {
	return filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore unreadable subtrees — partial coverage > none
		}
		if !d.IsDir() {
			return nil
		}
		// Skip the trash directory — its churn is the chassis's own
		// noise and never needs to surface in the tree.
		base := filepath.Base(p)
		if base == trashDirName {
			return fs.SkipDir
		}
		return f.inner.Add(p)
	})
}

// run is the fsnotify pump. Translates fsnotify ops into our smaller
// Op set and routes through the same debounce path notifyOp uses.
func (f *fsWatcher) run(l *Library) {
	for {
		select {
		case <-f.stop:
			return
		case ev, ok := <-f.inner.Events:
			if !ok {
				return
			}
			f.dispatch(l, ev)
		case _, ok := <-f.inner.Errors:
			if !ok {
				return
			}
			// Errors are best-effort logged via the chassis logger
			// elsewhere; this loop just keeps draining so a transient
			// failure (e.g. resource limit hit) doesn't wedge the
			// channel.
		}
	}
}

// dispatch maps an fsnotify event to an Op + path and feeds the
// debounce pipeline. New subdirectories are registered before
// returning so their children show up.
func (f *fsWatcher) dispatch(l *Library, ev fsnotify.Event) {
	rel := relForPath(l.root, ev.Name)
	if rel == "" {
		return
	}
	if ignoreEventName(rel) {
		return
	}

	var op Op
	switch {
	case ev.Op&fsnotify.Create != 0:
		op = OpCreated
		// New directory: extend the watch set so its children fire
		// events too. Best-effort — a permission error is silently
		// dropped (we surface the partial tree on Tree()).
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = f.addRecursive(ev.Name)
		}
	case ev.Op&fsnotify.Remove != 0, ev.Op&fsnotify.Rename != 0:
		op = OpDeleted
	case ev.Op&fsnotify.Write != 0:
		op = OpModified
	case ev.Op&fsnotify.Chmod != 0:
		// Chmod alone is noise; ignore.
		return
	default:
		return
	}
	l.notifyOp(rel, op)
}

// notifyOp is the single funnel both fsnotify and Library mutators
// call. Per-path debounce window: same path + same op while a timer
// is open is dropped; delete-then-create within the window collapses
// to Modified (atomic-save pattern); a different op replaces the
// pending entry.
func (l *Library) notifyOp(relPath string, op Op) {
	if l == nil {
		return
	}
	l.mu.Lock()
	fsw := l.fsw
	l.mu.Unlock()
	if fsw == nil {
		// No fsnotify backend wired; debounce in-place anyway so the
		// in-process emission path (Library mutators) still drops
		// duplicate ticks within the window.
		l.debounceFallback.debounce(l, relPath, op)
		return
	}
	fsw.debounce(l, relPath, op)
}

// debounce records a pending event for relPath. The first event for a
// path schedules a timer that fires after debounceWindow and delivers
// the event to subscribers. Further events for the same path within
// the window mutate the pending entry per the rules in the package
// doc.
func (f *fsWatcher) debounce(l *Library, relPath string, op Op) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if existing, ok := f.pending[relPath]; ok {
		// Duplicate-drop: same op, same path → keep the existing
		// timer (do not extend it). The contract is "one tick per
		// quiet period" — extending would let a fast writer starve
		// subscribers indefinitely.
		if existing.op == op {
			return
		}
		// Race rule: delete-then-create (or write-then-delete) within
		// the window collapses to Modified. The user sees one tick;
		// the Tree() re-fetch reflects the post-state.
		if (existing.op == OpDeleted && op == OpCreated) ||
			(existing.op == OpCreated && op == OpDeleted) ||
			(existing.op == OpModified && op == OpDeleted) ||
			(existing.op == OpDeleted && op == OpModified) {
			existing.op = OpModified
			return
		}
		// Other transitions (Created → Modified, Modified → Created)
		// keep the most recent op so the consumer sees the dominant
		// outcome.
		existing.op = op
		return
	}

	pe := &pendingEvent{op: op}
	pe.timer = time.AfterFunc(debounceWindow, func() {
		f.fire(l, relPath)
	})
	f.pending[relPath] = pe
}

// fire delivers the pending event for relPath and removes the entry.
// Called from the AfterFunc goroutine.
func (f *fsWatcher) fire(l *Library, relPath string) {
	f.mu.Lock()
	pe, ok := f.pending[relPath]
	if !ok {
		f.mu.Unlock()
		return
	}
	delete(f.pending, relPath)
	f.mu.Unlock()
	l.deliver(WatcherEvent{Path: relPath, Op: pe.op})
}

// deliver fans out a single typed event to every subscriber. The
// struct{} channel uses non-blocking send (cap 1, drop on full) so a
// slow consumer never wedges the producer; the typed channel has cap
// 16 and also drops on full — tests should drain quickly enough.
func (l *Library) deliver(ev WatcherEvent) {
	l.mu.Lock()
	live := l.watchers[:0]
	for _, w := range l.watchers {
		if w == nil || w.closed.Load() {
			continue
		}
		select {
		case w.ch <- struct{}{}:
		default:
		}
		select {
		case w.opCh <- ev:
		default:
		}
		live = append(live, w)
	}
	l.watchers = live
	l.mu.Unlock()
}

// notify is the back-compat helper called by Library mutators. The
// shape predates the typed Op channel; we synthesize a generic
// "modified" tick when no Op context is available. Kept private to
// the package so callers within library.go can keep their existing
// call sites unchanged.
//
// All Library mutators that DO know the op call notifyOp directly;
// notify is the legacy fall-through used during migration.
func (l *Library) notify() {
	if l == nil {
		return
	}
	l.notifyOp("", OpModified)
}

// relForPath converts an absolute fsnotify-reported path back to the
// slash-separated library-relative form so it matches Tree paths.
// Returns "" if absPath isn't under root (defensive — fsnotify only
// reports paths in dirs we registered).
func relForPath(root, absPath string) string {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return ""
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ignoreEventName silences events on paths the user never sees: the
// trash directory, the recents JSON, atomic-save tempfiles. The library
// surface treats these as internal plumbing and re-rendering on their
// churn would just thrash the UI.
func ignoreEventName(rel string) bool {
	if rel == "" {
		return true
	}
	first := rel
	if i := strings.IndexByte(rel, '/'); i != -1 {
		first = rel[:i]
	}
	if first == trashDirName || first == recentJSONRel {
		return true
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i != -1 {
		base = rel[i+1:]
	}
	// atomic-save tempfiles created by Library.Save (".save-*").
	if strings.HasPrefix(base, ".save-") {
		return true
	}
	return false
}

// debounceFallback handles the no-fsnotify path. The same per-path
// rules apply; we keep a separate map so an absent fsWatcher doesn't
// require a no-op shim that complicates the hot path.
type fallbackDebouncer struct {
	mu      sync.Mutex
	pending map[string]*pendingEvent
}

func (d *fallbackDebouncer) debounce(l *Library, relPath string, op Op) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil {
		d.pending = make(map[string]*pendingEvent)
	}
	if existing, ok := d.pending[relPath]; ok {
		if existing.op == op {
			return
		}
		if (existing.op == OpDeleted && op == OpCreated) ||
			(existing.op == OpCreated && op == OpDeleted) ||
			(existing.op == OpModified && op == OpDeleted) ||
			(existing.op == OpDeleted && op == OpModified) {
			existing.op = OpModified
			return
		}
		existing.op = op
		return
	}
	pe := &pendingEvent{op: op}
	pe.timer = time.AfterFunc(debounceWindow, func() {
		d.mu.Lock()
		entry, ok := d.pending[relPath]
		if !ok {
			d.mu.Unlock()
			return
		}
		delete(d.pending, relPath)
		d.mu.Unlock()
		l.deliver(WatcherEvent{Path: relPath, Op: entry.op})
	})
	d.pending[relPath] = pe
}
