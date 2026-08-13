package wiring

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// AuditEmitter adapts a structured-logging sink onto the
// compaction.AuditEmitter interface. The compaction engine and the
// soft-archive sweep emit three event kinds (KindSessionCompacted,
// KindCompactionFailed, KindCompactedOriginalsDeleted); the adapter
// renders each as a slog entry tagged with the audit.Kind so the
// existing harness log + downstream OTel exporters see the events.
//
// The harness's authoritative event log is core/event (which writes
// to data.db with a redaction pipeline). Wiring compaction audits onto
// that log is a follow-up — for WP08 we use slog as the universal
// surface so the chassis chat path always sees these events without
// pulling the entire event-log mission into core/agentgraph/compaction.
//
// The emitter also keeps a ring buffer of the last N events so the
// rpc layer can query "tell me the latest compaction events" without a
// round-trip to the persistent event log. The ring is bounded so a
// long-running session's maximal-mode emissions don't pin unbounded
// memory.
type AuditEmitter struct {
	mu     sync.Mutex
	ring   []RecordedEvent
	cap    int
	cursor int
}

// RecordedEvent is one entry in the in-memory ring. Marshaled to JSON
// for slog so downstream tooling can reconstruct the payload without
// reflective inspection.
type RecordedEvent struct {
	Kind    audit.Kind      `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// NewAuditEmitter constructs an emitter with a default ring capacity
// of 256 events.
func NewAuditEmitter() *AuditEmitter {
	return &AuditEmitter{cap: 256}
}

// WithRingCapacity overrides the in-memory ring size. Useful for tests
// that want to pin every emitted event without window math.
func (a *AuditEmitter) WithRingCapacity(n int) *AuditEmitter {
	if n <= 0 {
		n = 1
	}
	a.mu.Lock()
	a.cap = n
	a.ring = nil // reset; old buffer would be wrong-sized
	a.cursor = 0
	a.mu.Unlock()
	return a
}

// Emit implements compaction.AuditEmitter. Marshals the payload to
// JSON, writes the event to the ring buffer, and emits a slog entry
// tagged with the kind so the harness log captures the event. Errors
// (marshal failure on a malformed payload) are swallowed by convention
// — audit must never abort the engine.
func (a *AuditEmitter) Emit(ctx context.Context, kind audit.Kind, payload any) {
	if a == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		// Last-resort fallback: log with the marshal error so the chat
		// path still gets a breadcrumb, then drop the event.
		logging.L().Warn("compaction.audit.marshal_failed",
			"kind", string(kind), "err", err.Error())
		return
	}
	a.record(RecordedEvent{Kind: kind, Payload: raw})
	logging.L().Info("compaction.audit",
		"kind", string(kind),
		"payload", json.RawMessage(raw),
	)
}

// record appends to the bounded ring under the mutex. Newer events
// overwrite older ones once the ring is full — most-recent-first
// semantics so the rpc layer's "tail" queries are cheap.
func (a *AuditEmitter) record(e RecordedEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cap <= 0 {
		a.cap = 256
	}
	if len(a.ring) < a.cap {
		a.ring = append(a.ring, e)
		return
	}
	a.ring[a.cursor] = e
	a.cursor = (a.cursor + 1) % a.cap
}

// Recent returns up to n recent events in chronological (oldest-first)
// order. n <= 0 returns every buffered event.
func (a *AuditEmitter) Recent(n int) []RecordedEvent {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.ring) == 0 {
		return nil
	}
	// Reconstruct chronological order: when the ring isn't full,
	// a.ring is already chronological. When it's full, the oldest
	// event sits at a.cursor.
	out := make([]RecordedEvent, 0, len(a.ring))
	if len(a.ring) < a.cap {
		out = append(out, a.ring...)
	} else {
		out = append(out, a.ring[a.cursor:]...)
		out = append(out, a.ring[:a.cursor]...)
	}
	if n > 0 && n < len(out) {
		out = out[len(out)-n:]
	}
	return out
}
