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

// shutdown closes the work channel and blocks until all in-flight
// workers have exited.
func (p *asyncPool) shutdown() {
	close(p.work)
	p.wg.Wait()
}
