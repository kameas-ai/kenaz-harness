package chat

import (
	"context"
	"sync"
)

// runTracker counts the goroutines a ChatRunner spawns that touch storage
// after StartStream has returned — driveRun itself (whose exit path
// records the turn outcome, flushes the journal and deletes the stream
// checkpoint), its periodic checkpoint flusher, and the post-run
// auto-title / merge-suggestion / advice workers — so the owner can wait
// for them before closing the database (Drain).
//
// Not a sync.WaitGroup: StartStream can race Drain at shutdown, and a
// WaitGroup forbids an Add from zero concurrent with Wait. The zero value
// is ready to use.
type runTracker struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n returns to 0; nil while n == 0
}

func (t *runTracker) add() {
	t.mu.Lock()
	if t.n == 0 {
		t.idle = make(chan struct{})
	}
	t.n++
	t.mu.Unlock()
}

func (t *runTracker) done() {
	t.mu.Lock()
	t.n--
	if t.n == 0 {
		close(t.idle)
		t.idle = nil
	}
	t.mu.Unlock()
}

// wait blocks until no tracked goroutine is running or ctx ends.
func (t *runTracker) wait(ctx context.Context) error {
	for {
		t.mu.Lock()
		ch := t.idle
		t.mu.Unlock()
		if ch == nil {
			return nil
		}
		select {
		case <-ch:
			// Loop: a run started after this idle period began has its
			// own channel.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// goTracked runs fn on a tracked goroutine.
func (r *ChatRunner) goTracked(fn func()) {
	r.runs.add()
	go func() {
		defer r.runs.done()
		fn()
	}()
}

// Drain waits until every in-flight run — including its exit path and
// the background work it spawned — has finished, or ctx ends (returning
// ctx.Err()). It does not cancel anything; see Shutdown.
//
// Owners must Drain before closing the storage the runs write to: a run
// goroutine outlives StartStream's return, and its deferred cleanup
// (DeleteStreamCheckpoint, RecordTurnOutcome) otherwise races the DB
// Close (CI run 37705330106, TestDriveRun_AC003_ErrorClosePromotesCheckpoint).
func (r *ChatRunner) Drain(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return r.runs.wait(ctx)
}

// Shutdown cancels every live run (cause "app-shutdown", which the
// terminal path treats like a Stop: the partial is persisted and the
// stream closes with reason stop-called) and then Drains. Nil-safe;
// safe to call more than once. API.Shutdown calls it before core's
// storage is closed.
func (r *ChatRunner) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	subs := make([]*chatSub, 0, len(r.subs))
	for _, s := range r.subs {
		subs = append(subs, s)
	}
	r.mu.Unlock()
	for _, s := range subs {
		s.cancelCause.CompareAndSwap(nil, "app-shutdown")
		s.cancel()
	}
	return r.Drain(ctx)
}
