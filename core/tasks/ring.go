package tasks

import (
	"io"
	"strings"
	"sync"
)

const (
	// DefaultRingCapBytes is the per-stream ring buffer cap (8 MiB).
	DefaultRingCapBytes = 8 * 1024 * 1024

	// TailCapBytes is the maximum tail size returned in hook payloads (8 KiB).
	TailCapBytes = 8 * 1024
)

// ringBuffer is a fixed-capacity byte buffer that drops the oldest bytes
// when the cap is exceeded. It implements io.Writer so it can be used as
// a tee destination for exec.Cmd stdout/stderr.
//
// ringBuffer is NOT safe for concurrent writers; callers must serialize
// writes. Concurrent reads are safe if no write is in progress.
type ringBuffer struct {
	mu   sync.RWMutex
	buf  []byte
	cap  int
	full bool // once set, we've wrapped at least once
}

func newRingBuffer(capBytes int) *ringBuffer {
	if capBytes <= 0 {
		capBytes = DefaultRingCapBytes
	}
	return &ringBuffer{
		buf: make([]byte, 0, capBytes),
		cap: capBytes,
	}
}

// Write appends p to the buffer, dropping the OLDEST bytes when the cap
// is exceeded (first-byte strategy inverted: we keep the most recent N
// bytes of total output).
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	// If p alone exceeds cap, keep only the tail.
	if len(p) >= r.cap {
		r.buf = append(r.buf[:0], p[len(p)-r.cap:]...)
		r.full = true
		return len(p), nil
	}
	remaining := r.cap - len(r.buf)
	if len(p) <= remaining {
		r.buf = append(r.buf, p...)
	} else {
		// Drop oldest bytes to make room.
		need := len(p) - remaining
		r.buf = append(r.buf[:0], r.buf[need:]...)
		r.buf = append(r.buf, p...)
		r.full = true
	}
	return len(p), nil
}

// Bytes returns a defensive copy of the current buffer contents.
func (r *ringBuffer) Bytes() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// Tail returns the last n bytes as a string.
func (r *ringBuffer) Tail(n int) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.buf) == 0 {
		return ""
	}
	data := r.buf
	if len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}

// lineWriter wraps an underlying ringBuffer and io.Writer (the task log
// file) while also broadcasting complete lines to a subscriber list.
// It accumulates bytes into a scratch buffer, flushes complete lines to
// subscribers, and writes all bytes to the ring and the optional file.
//
// lineWriter is safe for concurrent writes only via the per-task taskStream
// which serializes all writes.
type lineWriter struct {
	mu          sync.Mutex
	stream      string      // "stdout" or "stderr"
	ring        *ringBuffer
	file        io.Writer   // nil-safe
	scratch     []byte
	nextOffset  int64
	subscribers *subscriberSet
}

func newLineWriter(stream string, ring *ringBuffer, file io.Writer, subs *subscriberSet) *lineWriter {
	return &lineWriter{
		stream:      stream,
		ring:        ring,
		file:        file,
		subscribers: subs,
	}
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	// Write to ring buffer (best-effort, no error returned to producer).
	_, _ = lw.ring.Write(p)
	// Write to log file (best-effort).
	if lw.file != nil {
		_, _ = lw.file.Write(p)
	}
	// Accumulate and broadcast complete lines.
	lw.scratch = append(lw.scratch, p...)
	for {
		idx := strings.IndexByte(string(lw.scratch), '\n')
		if idx < 0 {
			break
		}
		text := string(lw.scratch[:idx])
		lw.scratch = lw.scratch[idx+1:]
		ln := Line{
			Stream: lw.stream,
			Text:   text,
			Offset: lw.nextOffset,
		}
		lw.nextOffset++
		lw.subscribers.broadcast(ln)
	}
	return len(p), nil
}
