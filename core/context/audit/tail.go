// Package audit — tail.go
//
// TailReader provides the cursor-based read surface used by the fleet
// audit archiver to stream local audit events to the fleet backend.
// It is separate from the Emitter contract so the fleet package can
// depend only on this thin interface without pulling in the full
// event-log dependency chain.
//
// (fleet-audit-archival-01NDFSEX13 WP01)
package audit

import (
	"context"
	"time"
)

// TailEvent is a single audit event as seen by the TailReader. It
// carries the row-level hash-chain fields needed by the archiver to
// verify continuity before POSTing a batch to the fleet endpoint.
//
// Privacy invariant: TailEvent MUST NOT carry user-authored text or
// credential bytes — only IDs, timestamps, kinds, hashes, and the
// already-redacted payload bytes from the event-log pipeline.
type TailEvent struct {
	// ID is the event_id (ULID string). Used as the cursor value.
	ID string `json:"id"`
	// Kind is the audit Kind string (e.g. "session.auto_titled").
	Kind string `json:"kind"`
	// EmittedAt is the event timestamp.
	EmittedAt time.Time `json:"emitted_at"`
	// Payload is the already-redacted JSON payload bytes from the
	// event-log pipeline. Must not contain secrets or user content.
	Payload []byte `json:"payload"`
	// PayloadHash is the SHA-256 hash stored in the event-log row,
	// used by the chain verifier to detect tampering pre-send.
	PayloadHash [32]byte `json:"payload_hash"`
	// PrevHash is the previous row's PayloadHash from the event-log
	// chain. The archiver checks each row's PrevHash against its
	// predecessor's PayloadHash to detect breaks.
	PrevHash [32]byte `json:"prev_hash"`
	// SessionID is the session the event belongs to. Empty for
	// session-less events (process-level, fleet-level, etc.).
	SessionID string `json:"session_id,omitempty"`
}

// TailReader is the cursor-based read surface for streaming local audit
// events to the fleet archiver. It is implemented by the event-log
// Store and substituted with a fake in tests.
type TailReader interface {
	// Since returns up to limit audit events whose event_id is strictly
	// greater than afterID, ordered by event_id ascending. An empty
	// afterID starts from the beginning of the log.
	//
	// An empty return slice (with nil error) means the tail is
	// up-to-date: no new events exist after afterID.
	Since(ctx context.Context, afterID string, limit int) ([]TailEvent, error)

	// HighWater returns the event_id of the most-recently appended event.
	// Returns ("", nil) when the log is empty.
	HighWater(ctx context.Context) (string, error)
}

// StoreTailReader wraps an event-log store backend, exposing TailReader.
// The store backend must implement the Since/HighWater query patterns via
// its SelectByTimeRange / GetRow methods.
//
// Callers in core/fleet construct StoreTailReader at boot time by passing
// the wired event-log store; tests substitute a MemoryTailReader.
type StoreTailReader struct {
	// selectSince fetches up to limit rows with event_id > afterID.
	// Injected as a closure so this package does not import core/event/log
	// (DIRECTIVE_001: no cyclic imports).
	selectSince func(ctx context.Context, afterID string, limit int) ([]TailEvent, error)
	// highWater returns the latest event_id.
	highWater func(ctx context.Context) (string, error)
}

// NewStoreTailReader constructs a StoreTailReader from the provided
// function closures. Both functions must be non-nil.
func NewStoreTailReader(
	selectSince func(ctx context.Context, afterID string, limit int) ([]TailEvent, error),
	highWater func(ctx context.Context) (string, error),
) *StoreTailReader {
	return &StoreTailReader{
		selectSince: selectSince,
		highWater:   highWater,
	}
}

// Since implements TailReader.
func (r *StoreTailReader) Since(ctx context.Context, afterID string, limit int) ([]TailEvent, error) {
	return r.selectSince(ctx, afterID, limit)
}

// HighWater implements TailReader.
func (r *StoreTailReader) HighWater(ctx context.Context) (string, error) {
	return r.highWater(ctx)
}

// MemoryTailReader is an in-memory TailReader for tests. Callers append
// events via Append and use the reader interface to simulate the archive loop.
//
// Safe for concurrent use.
type MemoryTailReader struct {
	events []TailEvent
}

// Append adds an event to the in-memory tail. Not goroutine-safe during
// concurrent Append+Since calls; tests should populate before reading.
func (m *MemoryTailReader) Append(e TailEvent) { m.events = append(m.events, e) }

// Since implements TailReader.
func (m *MemoryTailReader) Since(_ context.Context, afterID string, limit int) ([]TailEvent, error) {
	var out []TailEvent
	for _, e := range m.events {
		if afterID != "" && e.ID <= afterID {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// HighWater implements TailReader.
func (m *MemoryTailReader) HighWater(_ context.Context) (string, error) {
	if len(m.events) == 0 {
		return "", nil
	}
	return m.events[len(m.events)-1].ID, nil
}
