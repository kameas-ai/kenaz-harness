// Package events defines the resolution-event payload shape and the
// EventLogger interface this layer emits to.
//
// Cross-mission boundary: the cross-cutting `event-log` mission owns
// the actual append-only ledger. We avoid importing core/event
// directly so that the cycle break in plan §6 is mechanical: this
// package defines a small interface (`EventLogger`); the consumer
// wires either a real event-log Logger, a buffer (for the boot-time
// nil case), or nil (during very early boot).
//
// The cycle: event-log needs an HMAC redaction salt resolved by this
// secrets layer; this layer wants to emit resolution events to
// event-log. Resolution: event-log boots in no-redaction mode just
// long enough to fetch its own salt, then promotes itself; this
// secrets layer accepts a nil EventLogger and buffers events until a
// real one is wired.
//
// Spec mapping: FR-012 (resolution events), C-002, C-003, C-005,
// NFR-003 (resolved value never present in events).
package events

import (
	"context"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
)

// EventKind enumerates the ten resolution-event kinds named in plan
// §5. Strings are stable across releases; consumers MAY persist them.
type EventKind string

const (
	KindResolutionRequested        EventKind = "secret.resolution.requested"
	KindResolutionCacheHit         EventKind = "secret.resolution.cache_hit"
	KindResolutionCacheMiss        EventKind = "secret.resolution.cache_miss"
	KindResolutionBackendDispatch  EventKind = "secret.resolution.backend_dispatched"
	KindResolutionOK               EventKind = "secret.resolution.ok"
	KindResolutionFailed           EventKind = "secret.resolution.failed"
	KindPreFlightOK                EventKind = "secret.preflight.ok"
	KindPreFlightFailed            EventKind = "secret.preflight.failed"
	KindCacheInvalidated           EventKind = "secret.cache.invalidated"
	KindRotationDetected           EventKind = "secret.rotation.detected"
)

// AllKinds returns the closed set of event kinds. Consumers iterate
// for completeness checks (e.g. audit-suite assertions).
func AllKinds() []EventKind {
	return []EventKind{
		KindResolutionRequested,
		KindResolutionCacheHit,
		KindResolutionCacheMiss,
		KindResolutionBackendDispatch,
		KindResolutionOK,
		KindResolutionFailed,
		KindPreFlightOK,
		KindPreFlightFailed,
		KindCacheInvalidated,
		KindRotationDetected,
	}
}

// ResolutionEvent is the FR-012 payload. The locator is NOT present;
// only the redaction-safe ReferenceID and the RefKind enum appear.
//
// IMPORTANT: this type MUST NEVER include the resolved value. The
// `[]byte`-only Secret type plus the lint analyzer enforces that no
// `string`-typed credential field exists at the call sites that
// populate this struct.
type ResolutionEvent struct {
	// ConsumerID identifies the caller (e.g. "llm:anthropic"). Safe
	// for logging.
	ConsumerID string `json:"consumer_id,omitempty"`
	// ReferenceID is ref.CredentialReference.ID — the redaction-safe
	// hex digest. Never the locator.
	ReferenceID string `json:"reference_id"`
	// ReferenceKind is the ref.RefKind enum string (env/keychain/...).
	ReferenceKind string `json:"reference_kind"`
	// BackendKind is the resolving backend's name.
	BackendKind registry.BackendKind `json:"backend_kind,omitempty"`
	// Outcome is one of the canonical FR-014 outcome strings (ok,
	// not_found, permission_denied, backend_unavailable,
	// format_invalid, rotated_mid_use). Empty for non-failure events
	// like cache_hit.
	Outcome string `json:"outcome,omitempty"`
	// LatencyMS is the wall-clock latency of the operation, in
	// milliseconds.
	LatencyMS int64 `json:"latency_ms"`
	// CacheHit is true when the value came from cache.
	CacheHit bool `json:"cache_hit"`
	// EmittedAt is the wall-clock time of emission.
	EmittedAt time.Time `json:"emitted_at"`
}

// NewResolutionEvent fills the redaction-safe fields from a
// reference.
func NewResolutionEvent(r ref.CredentialReference, consumerID string) ResolutionEvent {
	return ResolutionEvent{
		ConsumerID:    consumerID,
		ReferenceID:   r.ID(),
		ReferenceKind: r.Kind.String(),
		EmittedAt:     time.Now(),
	}
}

// EventLogger is the cross-mission interface that secrets emits to.
// The event-log mission ships a real implementation; tests inject a
// buffer; very-early-boot accepts nil.
//
// This interface intentionally mirrors the shape of the real
// `core/event.Log.Append` contract while staying decoupled.
type EventLogger interface {
	// Append records a resolution event. The implementation owns
	// persistence ordering and may add envelope metadata. The
	// secrets layer guarantees the payload contains no resolved
	// value bytes.
	Append(ctx context.Context, kind EventKind, payload ResolutionEvent) error
}

// BufferingLogger is the no-op-ish logger used during the boot cycle
// before a real Logger is wired. It records events in memory; once a
// real Logger is available, callers can drain via Flush.
type BufferingLogger struct {
	mu     sync.Mutex
	events []bufferedEvent
}

type bufferedEvent struct {
	Kind    EventKind
	Payload ResolutionEvent
}

// NewBufferingLogger returns an empty buffering logger.
func NewBufferingLogger() *BufferingLogger { return &BufferingLogger{} }

// Append implements EventLogger.
func (b *BufferingLogger) Append(_ context.Context, kind EventKind, payload ResolutionEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, bufferedEvent{Kind: kind, Payload: payload})
	return nil
}

// Drain returns the buffered events in arrival order and resets the
// buffer. Used at boot-cycle resolution.
func (b *BufferingLogger) Drain() []ResolutionEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]ResolutionEvent, len(b.events))
	for i, e := range b.events {
		out[i] = e.Payload
	}
	b.events = nil
	return out
}

// Len returns the buffered count (test helper).
func (b *BufferingLogger) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}
