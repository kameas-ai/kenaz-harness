// Package event is a stub of the event-log-01KQ1A3M surface that the
// ACP orchestration mission emits into.
//
// The full append-only on-disk log lives in that mission. This stub
// provides a credible Emitter / Log surface backed by an in-memory
// slice so other packages can compile, link, and test today. The shape
// is intended to be drop-in compatible with the real impl when it
// lands.
package event

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Type is the event-kind enumeration. Namespaces are colon-delimited;
// the ACP mission registers under "acp/...".
type Type string

// Actor identifies the subsystem or entity that emitted the event.
type Actor string

// Event is one append-only entry.
type Event struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	ParentSeq *int64          `json:"parent_seq,omitempty"`
	Type      Type            `json:"type"`
	TS        int64           `json:"ts"`
	Actor     Actor           `json:"actor"`
	Payload   json.RawMessage `json:"payload"`
}

// AppendOpts configure a single Append call.
type AppendOpts struct {
	Type      Type
	Actor     Actor
	Payload   any
	ParentSeq *int64
}

// Cursor identifies a position to read from.
type Cursor struct {
	SessionID string
	After     int64
}

// Log is the append-only event log surface.
type Log interface {
	Append(ctx context.Context, sessionID string, opts AppendOpts) (Event, error)
	Read(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error)
	LatestSeq(ctx context.Context, sessionID string) (int64, error)
	Close() error
}

// MemoryLog is an in-memory Log useful for tests and the v1 stub.
type MemoryLog struct {
	mu    sync.Mutex
	seq   atomic.Int64
	store map[string][]Event
}

// NewMemoryLog constructs an empty MemoryLog.
func NewMemoryLog() *MemoryLog {
	return &MemoryLog{store: map[string][]Event{}}
}

// Append records a new entry. The entry's Seq is monotonic across the
// log (not per-session), matching how the real on-disk log will assign
// seqs from a single writer.
func (m *MemoryLog) Append(_ context.Context, sessionID string, opts AppendOpts) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var payload json.RawMessage
	if opts.Payload != nil {
		b, err := json.Marshal(opts.Payload)
		if err != nil {
			return Event{}, err
		}
		payload = b
	}
	seq := m.seq.Add(1)
	ev := Event{
		SessionID: sessionID,
		Seq:       seq,
		ParentSeq: opts.ParentSeq,
		Type:      opts.Type,
		TS:        time.Now().UnixNano(),
		Actor:     opts.Actor,
		Payload:   payload,
	}
	m.store[sessionID] = append(m.store[sessionID], ev)
	return ev, nil
}

// Read returns events for sessionID with Seq in [fromSeq, toSeq]. If
// toSeq <= 0 the upper bound is open.
func (m *MemoryLog) Read(_ context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.store[sessionID]
	out := make([]Event, 0, len(src))
	for _, ev := range src {
		if ev.Seq < fromSeq {
			continue
		}
		if toSeq > 0 && ev.Seq > toSeq {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// LatestSeq returns the highest Seq for sessionID.
func (m *MemoryLog) LatestSeq(_ context.Context, sessionID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.store[sessionID]
	if len(src) == 0 {
		return 0, nil
	}
	return src[len(src)-1].Seq, nil
}

// Close is a no-op for the memory log.
func (m *MemoryLog) Close() error { return nil }

// AllForSession returns a copy of every event for sessionID, useful in
// tests for asserting full audit trails.
func (m *MemoryLog) AllForSession(sessionID string) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.store[sessionID]
	out := make([]Event, len(src))
	copy(out, src)
	return out
}
