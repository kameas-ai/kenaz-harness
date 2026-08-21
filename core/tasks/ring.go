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
// file) while also recording complete lines through record — which is
// Registry.AppendLine bound to this task's id (subagent-control-and-
// background-tasks-01PMZB11 UNIT-3). Before this unit, Write only
// broadcast to LIVE subscribers and never called AppendLine, so
// Registry.Tail — the mechanism BOTH kenaz__monitor's drain mode and
// its watch-mode final catch-up call read from (core/tools/monitor/
// tool.go Tail calls at :227/:267/:337) — returned an empty slice for
// every task regardless of whether output was captured: AppendLine had
// zero non-test callers repo-wide. record is what closes that gap;
// AppendLine assigns the offset and broadcasts, so this type no longer
// needs its own offset counter or a direct subscriber reference.
//
// lineWriter is safe for concurrent writes only via the per-task taskStream
// which serializes all writes.
type lineWriter struct {
	mu      sync.Mutex
	stream  string // "stdout" or "stderr"
	ring    *ringBuffer
	file    io.Writer // nil-safe
	scratch []byte
	record  func(Line) // nil-safe; records + broadcasts (Registry.AppendLine)
}

func newLineWriter(stream string, ring *ringBuffer, file io.Writer, record func(Line)) *lineWriter {
	return &lineWriter{
		stream: stream,
		ring:   ring,
		file:   file,
		record: record,
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
	// Accumulate and record complete lines.
	lw.scratch = append(lw.scratch, p...)
	for {
		idx := strings.IndexByte(string(lw.scratch), '\n')
		if idx < 0 {
			break
		}
		text := string(lw.scratch[:idx])
		lw.scratch = lw.scratch[idx+1:]
		if lw.record != nil {
			lw.record(Line{Stream: lw.stream, Text: text})
		}
	}
	return len(p), nil
}
