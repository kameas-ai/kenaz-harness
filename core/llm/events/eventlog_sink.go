package events

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/event"
)

// EventLogEmitter is the public seam from the event-log mission this
// package consumes. Production code passes a concrete event-log
// Emitter; tests pass a fake. Defined as a local alias so the import
// graph stays explicit and the Sink adapter does not have to import
// internals of core/event/log/.
type EventLogEmitter = event.Emitter

// EventLogSink is a Sink that translates connector audit Entries into
// event-log AppendInputs and forwards them to a wrapped event.Emitter.
//
// DIRECTIVE_001: this adapter lives in the consuming mission's package
// (core/llm/events) and depends on the public event.Emitter contract,
// never on core/event/log internals.
//
// Translation rules:
//   - Entry.Kind ("llm/request_submitted", ...) is normalized to the
//     event-log dotted grammar ("llm.request_submitted") before being
//     forwarded as event.Kind. The connector ships kinds in slash form
//     to mirror its EmitterID convention; the upstream event-log
//     grammar (FR-002 / WP02) requires dotted segments, so we translate
//     at the seam. The kind validation that the upstream emitter
//     performs on Append is the single source of truth; the connector
//     publishes a curated catalog (AllKinds()) the operator registers.
//   - Entry.SessionID, when non-empty, is parsed as an event.ULID and
//     attached to AppendInput.SessionID. Empty session ids leave the
//     AppendInput session pointer nil so headless events (preflight,
//     capability gate) land outside any session chain.
//   - Entry.Payload is forwarded as-is. The event-log redaction
//     pipeline runs unconditionally inside Emitter.Append (C-004); the
//     connector contributes structural safety (no raw HTTP bodies in
//     payloads) but never short-circuits redaction.
//   - The emitter id is hard-coded to "llm/connector"; per-provider
//     subdivision happens in the payload (Provider, Model, ProfileID)
//     rather than the emitter id, matching the EmitterID allowlist.
type EventLogSink struct {
	emitter   EventLogEmitter
	emitterID event.EmitterID
}

// EventLogSinkOption configures an EventLogSink.
type EventLogSinkOption func(*EventLogSink)

// WithEmitterID overrides the default "llm/connector" emitter id.
// Useful in test harnesses that allowlist a different namespace.
func WithEmitterID(id event.EmitterID) EventLogSinkOption {
	return func(s *EventLogSink) { s.emitterID = id }
}

// NewEventLogSink wraps emitter as a Sink. Returns an error when
// emitter is nil so callers cannot accidentally publish a Sink that
// drops every Append silently — that is the responsibility of the
// boot-time stub, not of the production wiring.
func NewEventLogSink(emitter EventLogEmitter, opts ...EventLogSinkOption) (*EventLogSink, error) {
	if emitter == nil {
		return nil, errors.New("llm/events: NewEventLogSink: nil emitter")
	}
	s := &EventLogSink{
		emitter:   emitter,
		emitterID: event.EmitterID("llm/connector"),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Append implements Sink by forwarding to the wrapped event.Emitter.
//
// Errors from the upstream Emitter are returned wrapped so callers can
// errors.Is against event-log sentinel errors without taking a hard
// dependency on the upstream package.
func (s *EventLogSink) Append(ctx context.Context, e Entry) error {
	if s == nil || s.emitter == nil {
		return errors.New("llm/events: EventLogSink: not initialized")
	}
	in := event.AppendInput{
		EmitterID: s.emitterID,
		Kind:      translateKind(e.Kind),
		Payload:   buildPayload(e),
		EmittedAt: e.Timestamp,
	}
	if e.SessionID != "" {
		sid := event.ULID(e.SessionID)
		in.SessionID = &sid
	}
	if _, err := s.emitter.Append(ctx, in); err != nil {
		return fmt.Errorf("llm/events: append %s: %w", e.Kind, err)
	}
	return nil
}

// buildPayload merges the Entry's structured fields with its
// freeform Payload map into a single payload object. The connector's
// envelope fields (provider, model, profile_id, event_id) ride
// alongside the per-kind payload keys so the upstream redactor sees
// one document and operators can filter on either dimension.
func buildPayload(e Entry) map[string]any {
	out := make(map[string]any, len(e.Payload)+4)
	if e.Provider != "" {
		out["provider"] = e.Provider
	}
	if e.Model != "" {
		out["model"] = e.Model
	}
	if e.ProfileID != "" {
		out["profile_id"] = e.ProfileID
	}
	if e.EventID != "" {
		out["event_id"] = e.EventID
	}
	for k, v := range e.Payload {
		out[k] = v
	}
	return out
}

// translateKind maps the connector's slash-separated kind ids
// ("llm/request_submitted") to the event-log dotted grammar
// ("llm.request_submitted") that core/event/kind enforces.
//
// Translation is mechanical (replace '/' with '.') and idempotent on
// already-dotted ids. We translate at the seam rather than mutating
// the connector's public Kind catalog because operator dashboards and
// fixture replays already index on the slash form.
func translateKind(k Kind) event.Kind {
	return event.Kind(strings.ReplaceAll(string(k), "/", "."))
}

// Compile-time assertion: *EventLogSink satisfies Sink.
var _ Sink = (*EventLogSink)(nil)
