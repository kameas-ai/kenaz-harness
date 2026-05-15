package sentry

import (
	"sync"
	"time"
)

const breadcrumbCap = 50

// Breadcrumb is a structured log entry stored in the ring buffer.
type Breadcrumb struct {
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	// Data carries additional structured key-value pairs. All values must
	// be primitives (string/int/bool/float); nested objects are not preserved.
	Data map[string]any `json:"data,omitempty"`
}

// RingBuffer is a thread-safe FIFO ring buffer for breadcrumbs. Capacity is
// fixed at breadcrumbCap (50). Older entries are evicted when the buffer is
// full.
type RingBuffer struct {
	mu   sync.Mutex
	buf  []Breadcrumb
	head int // write position (next slot to overwrite)
	size int // current number of entries (0..breadcrumbCap)
}

// Add appends a breadcrumb. When the buffer is full the oldest entry is
// overwritten.
func (r *RingBuffer) Add(b Breadcrumb) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) < breadcrumbCap {
		r.buf = append(r.buf, b)
		r.size = len(r.buf)
		r.head = r.size % breadcrumbCap
		return
	}
	r.buf[r.head] = b
	r.head = (r.head + 1) % breadcrumbCap
}

// Snapshot returns a copy of all current breadcrumbs in FIFO order
// (oldest first).
func (r *RingBuffer) Snapshot() []Breadcrumb {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.buf)
	if n == 0 {
		return nil
	}
	out := make([]Breadcrumb, n)
	if n < breadcrumbCap {
		// Not yet wrapped — elements are stored from 0 to n-1.
		copy(out, r.buf)
		return out
	}
	// Buffer is full and may have wrapped. head points to the oldest slot.
	for i := 0; i < n; i++ {
		out[i] = r.buf[(r.head+i)%breadcrumbCap]
	}
	return out
}

// globalBreadcrumbs is the process-wide ring buffer.
var globalBreadcrumbs RingBuffer

// AddBreadcrumb appends a breadcrumb to the global ring buffer.
func AddBreadcrumb(b Breadcrumb) {
	globalBreadcrumbs.Add(b)
}

// SnapshotBreadcrumbs returns all current breadcrumbs in FIFO order.
func SnapshotBreadcrumbs() []Breadcrumb {
	return globalBreadcrumbs.Snapshot()
}
