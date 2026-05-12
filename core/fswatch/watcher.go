// Package fswatch wraps fsnotify with per-session watch-path management.
// A Watcher instance is created per session, watches a set of absolute
// paths, debounces filesystem events into batches, and fires the
// file_changed hook event via a provided hook runner.
//
// Lifecycle:
//
//	w, err := New(sessionID, runner, logger)
//	w.Watch("/path/to/dir")
//	// ... session runs ...
//	w.Close() // stop watcher goroutine; idempotent
//
// Debounce: events within the same 250 ms window are batched into a
// single FileChangedEvent. This prevents hook spam when an editor
// writes a file in multiple operations.
//
// Thread safety: Watch, Unwatch, and Close are safe for concurrent use.
package fswatch

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sigil-tech/kaneaz-harness/core/hooks"
)

const (
	// debounceDuration is how long the watcher waits after the last
	// filesystem event before firing the batch.
	debounceDuration = 250 * time.Millisecond

	// maxBatchSize limits the number of operations in one batch so a
	// large tree churn does not produce an unbounded payload.
	maxBatchSize = 200
)

// Watcher watches a set of paths for a single session and fires
// file_changed hook events.
type Watcher struct {
	sessionID string
	runner    *hooks.Runner
	logger    *slog.Logger

	mu      sync.Mutex
	watched map[string]struct{} // currently watched absolute paths

	fsw    *fsnotify.Watcher
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Watcher for the given session. It starts the background
// dispatch goroutine immediately.
func New(sessionID string, runner *hooks.Runner, logger *slog.Logger) (*Watcher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{
		sessionID: sessionID,
		runner:    runner,
		logger:    logger,
		watched:   map[string]struct{}{},
		fsw:       fsw,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go w.dispatch()
	return w, nil
}

// Watch adds a path to the watched set. Duplicate adds are no-ops.
// The path is resolved to an absolute path before watching.
func (w *Watcher) Watch(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[abs]; ok {
		return nil // already watched
	}
	if err := w.fsw.Add(abs); err != nil {
		return err
	}
	w.watched[abs] = struct{}{}
	return nil
}

// Unwatch removes a path from the watched set.
func (w *Watcher) Unwatch(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[abs]; !ok {
		return nil // not watched
	}
	if err := w.fsw.Remove(abs); err != nil {
		return err
	}
	delete(w.watched, abs)
	return nil
}

// WatchedPaths returns the set of currently watched paths.
func (w *Watcher) WatchedPaths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	paths := make([]string, 0, len(w.watched))
	for p := range w.watched {
		paths = append(paths, p)
	}
	return paths
}

// SetWatchPaths replaces the watched set with the provided paths.
// Paths that are new are added; paths that were watched but are absent
// from the new set are removed.
func (w *Watcher) SetWatchPaths(paths []string) {
	desired := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			w.logger.Warn("fswatch.set_watch_paths.abs_failed",
				"path", p, "err", err.Error())
			continue
		}
		desired[abs] = struct{}{}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove paths no longer wanted.
	for p := range w.watched {
		if _, ok := desired[p]; !ok {
			if err := w.fsw.Remove(p); err != nil {
				w.logger.Warn("fswatch.unwatch_failed", "path", p, "err", err.Error())
			}
			delete(w.watched, p)
		}
	}
	// Add new paths.
	for p := range desired {
		if _, ok := w.watched[p]; !ok {
			if err := w.fsw.Add(p); err != nil {
				w.logger.Warn("fswatch.watch_failed", "path", p, "err", err.Error())
				continue
			}
			w.watched[p] = struct{}{}
		}
	}
}

// Close stops the watcher. Subsequent calls are no-ops. Close blocks
// until the dispatch goroutine exits.
func (w *Watcher) Close() {
	w.cancel()
	<-w.done
	_ = w.fsw.Close()
}

// dispatch runs in a goroutine, collects fsnotify events, debounces
// them, and fires file_changed hooks.
func (w *Watcher) dispatch() {
	defer close(w.done)

	var (
		pending  []hooks.FileChangedOp
		pendingP []string
		timer    *time.Timer
		timerC   <-chan time.Time
		seenP    map[string]struct{}
	)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := pending
		batchPaths := pendingP
		pending = nil
		pendingP = nil
		seenP = nil
		if timer != nil {
			timer.Stop()
			timerC = nil
		}
		ctx := w.ctx
		if w.runner != nil {
			ev := hooks.FileChangedEvent{
				SessionID: w.sessionID,
				Paths:     batchPaths,
				Events:    batch,
			}
			_, _ = w.runner.Fire(ctx, hooks.EventFileChanged, ev)
		}
	}

	for {
		select {
		case <-w.ctx.Done():
			flush()
			return

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Warn("fswatch.error", "session_id", w.sessionID, "err", err.Error())

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			op := opName(event.Op)
			if op == "" {
				continue
			}
			if seenP == nil {
				seenP = map[string]struct{}{}
			}
			pending = append(pending, hooks.FileChangedOp{Path: event.Name, Op: op})
			if _, dup := seenP[event.Name]; !dup {
				seenP[event.Name] = struct{}{}
				pendingP = append(pendingP, event.Name)
			}
			// Cap batch size to avoid unbounded growth.
			if len(pending) >= maxBatchSize {
				flush()
				continue
			}
			// Reset the debounce timer.
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounceDuration)
			timerC = timer.C

		case <-timerC:
			flush()
		}
	}
}

// opName maps an fsnotify.Op bitmask to a string. Returns "" for
// operations we don't map (e.g. Op(0)).
func opName(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Create != 0:
		return "create"
	case op&fsnotify.Write != 0:
		return "write"
	case op&fsnotify.Remove != 0:
		return "remove"
	case op&fsnotify.Rename != 0:
		return "rename"
	case op&fsnotify.Chmod != 0:
		return "chmod"
	default:
		return ""
	}
}
