package hooks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAsyncPool_PanicDoesNotCrashWorker asserts that a panicking hook
// fn does not crash the pool worker goroutine. Subsequent work items are
// still processed and the process survives (FR-002).
func TestAsyncPool_PanicDoesNotCrashWorker(t *testing.T) {
	t.Parallel()

	p := newAsyncPool(2)
	defer p.shutdown()

	var panicFired atomic.Bool
	var afterPanic atomic.Bool

	var wg sync.WaitGroup
	wg.Add(2)

	// First work item: panics.
	p.submit(asyncWork{fn: func() {
		defer wg.Done()
		panicFired.Store(true)
		panic("injected panic in hook worker")
	}})

	// Second work item: runs normally after the panic.
	p.submit(asyncWork{fn: func() {
		defer wg.Done()
		afterPanic.Store(true)
	}})

	// Wait with a timeout so a stuck worker surfaces as a test failure.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pool workers — worker may have crashed")
	}

	if !panicFired.Load() {
		t.Error("panic hook fn was never called")
	}
	if !afterPanic.Load() {
		t.Error("post-panic hook fn was never called — worker goroutine did not survive the panic")
	}
}

// TestAsyncPool_SubmitAndDrain asserts the happy path: submitted work
// runs and the pool shuts down cleanly.
func TestAsyncPool_SubmitAndDrain(t *testing.T) {
	t.Parallel()

	// size=4 → queue capacity=16; send 4 items (well within capacity).
	p := newAsyncPool(4)

	var count atomic.Int64
	const n = 4
	for i := 0; i < n; i++ {
		ok := p.submit(asyncWork{fn: func() { count.Add(1) }})
		if !ok {
			t.Fatal("submit returned false unexpectedly")
		}
	}

	p.shutdown()
	if got := count.Load(); got != n {
		t.Errorf("count = %d, want %d", got, n)
	}
}
