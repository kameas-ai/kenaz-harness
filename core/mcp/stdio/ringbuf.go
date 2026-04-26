package stdio

import "sync"

// RingBufferSize is the fixed capacity of every stderr ring buffer
// the pool allocates per server. 64 KiB is enough to capture the
// last few hundred lines of stderr without unbounded memory growth
// when a server goes berserk.
const RingBufferSize = 64 * 1024

// RingBuffer is a fixed-size byte ring used to capture the tail of
// each server's stderr stream. Writes are O(n) over the input
// (just two memcpy calls regardless of input size) and never fail.
// Snapshot returns the most recent maxBytes bytes in chronological
// order.
//
// The implementation uses a single lock around mutating ops. Stderr
// is low-volume; the lock contention story is "irrelevant".
type RingBuffer struct {
	mu   sync.Mutex
	buf  []byte
	pos  int
	full bool
}

// NewRingBuffer returns a buffer of the requested capacity. Sizes
// less than 1 default to RingBufferSize so callers can construct
// with a zero size for "use the default".
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = RingBufferSize
	}
	return &RingBuffer{buf: make([]byte, size)}
}

// Write appends p to the buffer, overwriting the oldest bytes when
// the capacity is reached. Always returns (len(p), nil) so it
// satisfies io.Writer without surprises.
func (r *RingBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	capacity := len(r.buf)
	// If the input is larger than the whole buffer, only the trailing
	// `capacity` bytes can possibly survive — fast-path them.
	if len(p) >= capacity {
		copy(r.buf, p[len(p)-capacity:])
		r.pos = 0
		r.full = true
		return len(p), nil
	}
	// Two-step write: from pos to end, then wrap to start if needed.
	first := copy(r.buf[r.pos:], p)
	if first < len(p) {
		copy(r.buf, p[first:])
		r.full = true
	} else if r.pos+first == capacity {
		r.full = true
	}
	r.pos = (r.pos + len(p)) % capacity
	return len(p), nil
}

// Snapshot returns the most recent maxBytes bytes of the buffer in
// chronological order. When maxBytes <= 0 or maxBytes exceeds the
// stored content, the entire stored content is returned.
func (r *RingBuffer) Snapshot(maxBytes int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	capacity := len(r.buf)
	var out []byte
	if r.full {
		out = make([]byte, capacity)
		// Oldest byte is at pos; copy from pos→end then 0→pos.
		copy(out, r.buf[r.pos:])
		copy(out[capacity-r.pos:], r.buf[:r.pos])
	} else {
		out = make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
	}
	return string(out)
}

// Len returns the number of bytes currently stored. Test-only
// accessor; production code uses Snapshot.
func (r *RingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return len(r.buf)
	}
	return r.pos
}
