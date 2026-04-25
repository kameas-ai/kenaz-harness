// Package events is the LLM connector's audit-emit surface (WP04).
//
// Every connector-generated event flows through Emitter exactly once.
// The emitter constructs payloads from typed structs (never from raw
// HTTP wire bodies) so plaintext credentials cannot enter the log even
// if a provider SDK round-trips a request body on retry (plan R2).
//
// The connector does not implement event-log redaction itself — that
// is the upstream event-log pipeline's job (event-log FR-005). The
// connector's contract is structural: it never puts credential bytes
// in the payload to begin with, and it contributes a curated catalog
// of provider credential prefixes (PlaintextCredentialPrefixes in
// core/llm) for the upstream redactor's catalog.
package events

import (
	"context"
	"sync"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Kind enumerates the connector-emitted event-log kinds (plan §5.3).
type Kind string

const (
	KindRequestSubmitted   Kind = "llm/request_submitted"
	KindPreflightResolved  Kind = "llm/preflight_resolved"
	KindPreflightFailed    Kind = "llm/preflight_failed"
	KindCapabilityRejected Kind = "llm/capability_rejected"
	KindRetryAttempted     Kind = "llm/retry_attempted"
	KindStreamChunk        Kind = "llm/stream_chunk"
	KindResponseFinal      Kind = "llm/response_final"
	KindCancelled          Kind = "llm/cancelled"
	KindError              Kind = "llm/error"
	KindPolicyDenied       Kind = "llm/policy_denied"
)

// AllKinds returns every kind the connector emits (used by tests and
// by namespace-registration scripts).
func AllKinds() []Kind {
	return []Kind{
		KindRequestSubmitted, KindPreflightResolved, KindPreflightFailed,
		KindCapabilityRejected, KindRetryAttempted, KindStreamChunk,
		KindResponseFinal, KindCancelled, KindError, KindPolicyDenied,
	}
}

// Entry is the structured event record persisted to the event log.
//
// Note: this is intentionally a flat struct, NOT a pointer to the
// upstream event-log type. The connector emits Entry values, and the
// concrete event-log binding (plugged in via Emitter) translates an
// Entry into whatever shape the upstream package requires.
type Entry struct {
	Kind      Kind           `json:"kind"`
	SessionID string         `json:"session_id,omitempty"`
	ProfileID string         `json:"profile_id,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Model     string         `json:"model,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// Sink is the lower-level write surface the Emitter uses to persist an
// Entry. Plugging a real event-log binding is a one-line change at
// the Registry construction site (per plan §6.2). Tests use a memory
// Sink (MemorySink, below).
type Sink interface {
	Append(ctx context.Context, e Entry) error
}

// Emitter is the connector's single chokepoint for audit emission.
//
// All call sites — Registry.Stream, CapabilityGate, RetryMiddleware,
// PreflightCoordinator — invoke methods on Emitter. The methods
// construct typed payloads; they never accept raw HTTP wire bodies.
type Emitter struct {
	sink Sink
	now  func() time.Time
}

// New returns an Emitter that writes to sink.
func New(sink Sink) *Emitter {
	if sink == nil {
		sink = &MemorySink{}
	}
	return &Emitter{sink: sink, now: time.Now}
}

// SetClock overrides the timestamp source (for deterministic tests).
func (e *Emitter) SetClock(now func() time.Time) {
	if now != nil {
		e.now = now
	}
}

func (e *Emitter) emit(ctx context.Context, k Kind, base Entry, payload map[string]any) error {
	base.Kind = k
	base.Timestamp = e.now()
	base.Payload = payload
	return e.sink.Append(ctx, base)
}

// RequestSubmitted records a request that has cleared the
// CapabilityGate / PolicyGuard / CredentialResolver gates.
//
// SnapshotID is the bundle ResolvedGraph snapshot id (FR-020); blank
// outside of a graph context (test mode).
func (e *Emitter) RequestSubmitted(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, snapshotID string) error {
	payload := map[string]any{
		"profile_id":           prof.ID,
		"provider":             prof.Kind,
		"model":                prof.Model,
		"snapshot_id":          snapshotID,
		"requested_capabilities": capList(req.RequestedCapabilities()),
		"messages_count":       len(req.Messages),
		"tools_count":          len(req.Tools),
		"attachments_count":    len(req.Attachments),
		"json_mode":            req.JSONMode != nil && req.JSONMode.Enabled,
		"caching":              req.Caching != nil && req.Caching.Enabled,
		"reasoning":            req.Reasoning != nil && req.Reasoning.Enabled,
	}
	return e.emit(ctx, KindRequestSubmitted, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, payload)
}

// PreflightResolved records a successful preflight resolution. The
// resolved bytes are NOT in the payload — only the reference shape.
func (e *Emitter) PreflightResolved(ctx context.Context, prof llm.ProviderProfile) error {
	return e.emit(ctx, KindPreflightResolved, Entry{
		ProfileID: prof.ID, Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"cred_kind": prof.Cred.Kind,
		// Locator is by definition the indirection key, not the value.
		"cred_locator": prof.Cred.Locator,
	})
}

// PreflightFailed records a preflight failure. The error message is
// passed through as-is; redaction in the upstream event-log pipeline
// catches any accidental credential bytes.
func (e *Emitter) PreflightFailed(ctx context.Context, prof llm.ProviderProfile, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return e.emit(ctx, KindPreflightFailed, Entry{
		ProfileID: prof.ID, Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"cred_kind":    prof.Cred.Kind,
		"cred_locator": prof.Cred.Locator,
		"error":        msg,
	})
}

// CapabilityRejected records a gate refusal before any wire call.
func (e *Emitter) CapabilityRejected(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, missing []llm.Capability) error {
	return e.emit(ctx, KindCapabilityRejected, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"missing_capabilities": capList(missing),
	})
}

// RetryAttempted records one retry boundary (US4 Acceptance 1).
func (e *Emitter) RetryAttempted(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, attempt int, plannedBackoffMS, actualMS int, errMsg string) error {
	return e.emit(ctx, KindRetryAttempted, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"attempt":        attempt,
		"planned_ms":     plannedBackoffMS,
		"actual_ms":      actualMS,
		"error":          errMsg,
	})
}

// StreamChunk records one streaming delta. The payload includes only
// the typed shape (kind, text length, finish reason if any) — never
// the provider's raw wire frame.
func (e *Emitter) StreamChunk(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, ev llm.StreamEvent) error {
	payload := map[string]any{
		"kind": string(ev.Kind),
	}
	if ev.Text != "" {
		// Length is enough to confirm flow; the actual text is recorded
		// in response_final.
		payload["text_bytes"] = len(ev.Text)
	}
	if ev.Finish != "" {
		payload["finish"] = ev.Finish
	}
	if ev.Usage != nil {
		payload["usage"] = ev.Usage
	}
	return e.emit(ctx, KindStreamChunk, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, payload)
}

// ResponseFinal records a terminal Response with usage + cost (NFR-012).
func (e *Emitter) ResponseFinal(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, resp llm.Response, latencyMS int64) error {
	return e.emit(ctx, KindResponseFinal, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"finish_reason": resp.FinishReason,
		"usage":         resp.Usage,
		"cost":          resp.Cost,
		"attempts":      resp.Attempts,
		"latency_ms":    latencyMS,
		"snapshot_id":   resp.SnapshotID,
	})
}

// Cancelled records a caller-initiated cancellation.
func (e *Emitter) Cancelled(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, reason string) error {
	return e.emit(ctx, KindCancelled, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"reason": reason,
	})
}

// Error records a non-transient or budget-exhausted error.
func (e *Emitter) Error(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return e.emit(ctx, KindError, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"error": msg,
	})
}

// PolicyDenied records a pre-call policy refusal.
func (e *Emitter) PolicyDenied(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, reason string) error {
	return e.emit(ctx, KindPolicyDenied, Entry{
		SessionID: req.SessionID, ProfileID: prof.ID,
		Provider: prof.Kind, Model: prof.Model,
	}, map[string]any{
		"reason": reason,
	})
}

func capList(caps []llm.Capability) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

// MemorySink is an in-memory Sink suitable for tests. It is goroutine-
// safe so the audit pipeline can be exercised under -race.
type MemorySink struct {
	mu      sync.Mutex
	entries []Entry
}

// Append stores e in the sink.
func (s *MemorySink) Append(_ context.Context, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

// Entries returns a snapshot copy of the recorded entries.
func (s *MemorySink) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Kinds returns the ordered Kind sequence for diagnostic assertions.
func (s *MemorySink) Kinds() []Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Kind, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.Kind
	}
	return out
}

// Reset clears recorded entries.
func (s *MemorySink) Reset() {
	s.mu.Lock()
	s.entries = nil
	s.mu.Unlock()
}
