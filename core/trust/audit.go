package trust

import (
	"context"
	"sync"
	"time"
)

// EventKind names every trust audit event the engine emits (plan §5.2).
//
// The set is append-only; filters and dashboards depend on the strings.
type EventKind string

const (
	EventVerificationAccepted EventKind = "trust/verification-accepted"
	EventVerificationRejected EventKind = "trust/verification-rejected"
	EventAnchorInstalled      EventKind = "trust/anchor-installed"
	EventAnchorRemoved        EventKind = "trust/anchor-removed"
	EventKeyRotated           EventKind = "trust/key-rotated"
	EventRevocationIngested   EventKind = "trust/revocation-ingested"
	EventBackendUnavailable   EventKind = "trust/backend-unavailable"
	EventPreflightFinding     EventKind = "trust/preflight-finding"
)

// Event is a single append-only entry the engine hands to an [Emitter].
//
// Per plan §5.2 the payload carries enough detail to replay the decision
// offline (anchor_id, algorithm, cache_state, rejection_code,
// backend_kind, payload_hash, timestamp). It carries *no* private-key
// bytes and *no* raw payload bytes — just the SHA-256 hash for forensic
// correlation.
type Event struct {
	Kind         EventKind
	OccurredAt   time.Time
	AnchorID     string
	Algorithm    Algorithm
	CacheState   CacheState
	Decision     Decision
	Rejection    RejectionCode
	BackendKind  BackendKind
	PayloadHash  string
	Detail       string
	Fields       map[string]string
}

// Emitter is the cross-mission stub for the event-log Emitter
// (`core/event/`). The real implementation lives in
// `event-log-01KQ1A3M`. WP01 ships an in-memory dispatcher so the trust
// engine can be exercised in isolation while the event-log mission
// completes (per task constraints — "stub event-log Emitter").
type Emitter interface {
	Emit(ctx context.Context, e Event) error
}

// MemoryEmitter is the v1.0 in-memory event sink. It is goroutine-safe
// and exposes the captured events for assertions in tests. Production
// builds will swap in the real `core/event/` emitter via [NewEngineWithEmitter].
type MemoryEmitter struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryEmitter constructs an empty in-memory emitter.
func NewMemoryEmitter() *MemoryEmitter {
	return &MemoryEmitter{}
}

// Emit implements [Emitter].
func (m *MemoryEmitter) Emit(_ context.Context, e Event) error {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

// Events returns a copy of the captured event slice.
func (m *MemoryEmitter) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Reset clears captured events. Used between test cases.
func (m *MemoryEmitter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}
