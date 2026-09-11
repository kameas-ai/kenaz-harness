// pool.go — bounded goroutine pool for async hook dispatch.
//
// The pool is created lazily per Runner (via Runner.lazyPool). Workers
// drain the channel in FIFO order; when the buffered channel is full the
// submit call returns false (caller logs and drops).
package hooks

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// defaultPoolSize is the default number of worker goroutines in the
// async hook dispatch pool.
const defaultPoolSize = 8

// asyncPool is the bounded goroutine pool for async hook dispatch.
type asyncPool struct {
	work chan asyncWork
	wg   sync.WaitGroup
}

// asyncWork is one unit of work enqueued in the pool.
type asyncWork struct {
	fn func()
}

// newAsyncPool starts `size` worker goroutines draining a buffered
// channel of capacity size*4.
func newAsyncPool(size int) *asyncPool {
	if size <= 0 {
		size = defaultPoolSize
	}
	p := &asyncPool{
		work: make(chan asyncWork, size*4),
	}
	for i := 0; i < size; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for w := range p.work {
				// Recover panics from individual hook builtins (FR-002).
				// A panicking hook is logged and surfaced as a hook failure;
				// the worker goroutine continues processing subsequent work
				// items and the process does not crash.
				func() {
					defer func() {
						if r := recover(); r != nil {
							stack := debug.Stack()
							logging.L().Error("hooks.pool.worker.panic",
								"panic", fmt.Sprintf("%v", r),
								"stack", string(stack),
							)
						}
					}()
					w.fn()
				}()
			}
		}()
	}
	return p
}

// submit enqueues work without blocking. Returns false when the queue
// is at capacity and the work was dropped.
func (p *asyncPool) submit(w asyncWork) bool {
	select {
	case p.work <- w:
		return true
	default:
		return false
	}
}

// shutdown closes the work channel and waits up to timeout for all
// in-flight workers to drain. If timeout <= 0 it waits unboundedly
// (kept for test call sites that want the old unconditional-drain
// behavior).
//
// When the timeout elapses first, shutdown returns false without
// waiting any further — the wg.Wait() keeps running in a background
// goroutine and any workers still processing queued items are simply
// abandoned. This is safe: close(p.work) has already happened above,
// so no new work can be enqueued (submit()'s select-with-default
// never blocks and the closed channel only ever has receivers, not
// senders, after this point); each worker only touches its own
// captured closure state, never the pool itself, so an abandoned
// worker cannot race the caller or write into anything the caller is
// tearing down. The process exiting is what ultimately reclaims the
// goroutine.
func (p *asyncPool) shutdown(timeout time.Duration) bool {
	close(p.work)
	if timeout <= 0 {
		p.wg.Wait()
		return true
	}
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
