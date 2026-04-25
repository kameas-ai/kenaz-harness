package policy

import (
	"context"
	"errors"
	"sync"
	"time"
)

// This file holds the cross-mission interface seams the policy engine
// depends on. Each one is the smallest contract this mission needs;
// the consuming missions (bundle-format-resolver-01KQ1A3J,
// a2a-signed-cards-trust-01KQ18P9, event-log-01KQ1A3M) are responsible
// for landing the production implementations and wiring them in.
//
// Per DIRECTIVE_010, the seams here are explicit so the cross-mission
// dependency is visible in code review rather than hiding behind
// duck-typed adapters.

// ---------------------------------------------------------------------------
// event-log-01KQ1A3M — append-only event emitter
// ---------------------------------------------------------------------------

// EventEmitter is the seam every PolicyEvent travels through. The
// production event-log mission provides an Emitter that hash-chains,
// redacts, and persists; this package never writes audit data
// directly to disk (plan §8 R7).
type EventEmitter interface {
	// Emit appends the event and returns when the entry is durable
	// per the event-log mission's semantics. Implementations MUST be
	// safe for concurrent use.
	Emit(ctx context.Context, evt PolicyEvent) error
}

// PolicyEvent enumerates the audit kinds emitted by the policy
// engine. The kinds match data-model.md §PolicyEvent and FR-009.
type PolicyEvent struct {
	Kind        PolicyEventKind `json:"kind"`
	OccurredAt  time.Time       `json:"occurred_at"`
	PolicyID    string          `json:"policy_id,omitempty"`
	ClauseID    string          `json:"clause_id,omitempty"`
	DecisionID  string          `json:"decision_id,omitempty"`
	Decision    *Decision       `json:"decision,omitempty"`
	Finding     *Finding        `json:"finding,omitempty"`
	OldEffective string         `json:"old_effective,omitempty"`
	NewEffective string         `json:"new_effective,omitempty"`
	Message     string          `json:"message,omitempty"`
	Inputs      map[string]any  `json:"inputs,omitempty"`
}

// PolicyEventKind is the closed enum of audit-log entries the engine
// produces. The names match the wire shape consumers (and auditors)
// expect.
type PolicyEventKind string

const (
	EventPolicyLoaded                  PolicyEventKind = "policy_loaded"
	EventPolicyLayerTransitioned       PolicyEventKind = "policy_layer_transitioned"
	EventPolicyEvaluationAllowed       PolicyEventKind = "policy_evaluation_allowed"
	EventPolicyEvaluationDenied        PolicyEventKind = "policy_evaluation_denied"
	EventPolicyValidatorFinding        PolicyEventKind = "policy_validator_finding"
	EventPolicyUnavailableFailClosed   PolicyEventKind = "policy_unavailable_fail_closed"
	EventPolicyUnavailableFailOpen     PolicyEventKind = "policy_unavailable_fail_open"
	EventPolicyOverrideUsed            PolicyEventKind = "policy_override_used"
)

// MemoryEmitter is the in-memory EventEmitter the engine falls back
// to when the production event-log has not yet been wired. It is the
// scaffolding plan §8 R8 anticipates: the harness can boot with a
// no-op-but-observable emitter, and tests can assert the exact
// sequence of events.
type MemoryEmitter struct {
	mu     sync.Mutex
	events []PolicyEvent
}

// NewMemoryEmitter returns a fresh MemoryEmitter.
func NewMemoryEmitter() *MemoryEmitter { return &MemoryEmitter{} }

// Emit records the event in memory. Always returns nil.
func (m *MemoryEmitter) Emit(_ context.Context, evt PolicyEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
	return nil
}

// Events returns a snapshot of every event recorded so far.
func (m *MemoryEmitter) Events() []PolicyEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PolicyEvent, len(m.events))
	copy(out, m.events)
	return out
}

// Reset clears the recorded events. Test helper.
func (m *MemoryEmitter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}

// ---------------------------------------------------------------------------
// a2a-signed-cards-trust-01KQ18P9 — signature verification
// ---------------------------------------------------------------------------

// Signer identifies the principal whose key signed an artifact.
type Signer struct {
	KeyID       string `json:"key_id"`
	TrustAnchor string `json:"trust_anchor"`
	DisplayName string `json:"display_name,omitempty"`
}

// SignatureVerifier is the seam the policy bundle handler uses to
// verify policy artifacts' signature envelopes (spec FR-003, plan §4
// step 2). The production implementation lives in the
// a2a-signed-cards-trust mission.
type SignatureVerifier interface {
	// Verify returns the verifying signers when the envelope is
	// signed under one of the operator's trusted anchors. Returns
	// ErrUntrustedAnchor when the signer is unknown,
	// ErrMissingSignature when the envelope is empty, and
	// ErrWrongSigner when the signer is known but not trusted for
	// policy artifacts.
	Verify(envelope []byte, content []byte) ([]Signer, error)
}

// ErrMissingSignature is returned when no signature envelope is
// present.
var ErrMissingSignature = errors.New("policy: missing signature envelope")

// ErrWrongSigner is returned when the signer is recognized but not
// authorized to sign the artifact in question.
var ErrWrongSigner = errors.New("policy: artifact signed by wrong signer")

// ErrUntrustedAnchor is returned when the trust anchor is not yet
// trusted by the operator (spec edge case).
var ErrUntrustedAnchor = errors.New("policy: signer trust anchor not trusted")

// AlwaysAllowVerifier is the development stub: it accepts any
// non-empty envelope and reports a "dev" signer. Production wiring
// MUST replace it with the real a2a-signed-cards-trust verifier
// before any real policy is enforced (spec FR-003).
type AlwaysAllowVerifier struct{ DisplayName string }

// Verify implements SignatureVerifier.
func (v *AlwaysAllowVerifier) Verify(envelope, _ []byte) ([]Signer, error) {
	if len(envelope) == 0 {
		return nil, ErrMissingSignature
	}
	name := v.DisplayName
	if name == "" {
		name = "dev-stub"
	}
	return []Signer{{KeyID: "dev-stub", TrustAnchor: "dev-stub", DisplayName: name}}, nil
}

// AlwaysRejectVerifier is the test stub for FR-003 negative cases.
type AlwaysRejectVerifier struct{ Err error }

// Verify implements SignatureVerifier.
func (v *AlwaysRejectVerifier) Verify(_ []byte, _ []byte) ([]Signer, error) {
	if v.Err != nil {
		return nil, v.Err
	}
	return nil, ErrWrongSigner
}

// ---------------------------------------------------------------------------
// bundle-format-resolver-01KQ1A3J — artifact-kind registration seam
// ---------------------------------------------------------------------------

// ArtifactKindHandler is the contract the bundle resolver expects
// from a kind handler. The policy engine registers a handler for
// `kind: policy` in core/policy/bundle.
type ArtifactKindHandler interface {
	// Kind returns the artifact kind this handler serves, e.g.
	// "policy".
	Kind() string

	// Handle is invoked by the bundle resolver when an artifact of
	// the matching kind is fetched. The handler parses, validates,
	// verifies signature, and forwards to the engine.
	Handle(ctx context.Context, raw RawArtifact) error
}

// RawArtifact is the minimal shape the bundle resolver hands a
// kind-handler. The real bundle-format-resolver mission ships richer
// metadata; this struct is the subset the policy mission needs.
type RawArtifact struct {
	BundleID         string
	ArtifactID       string
	Kind             string
	Body             []byte
	SignatureEnvelope []byte
	FetchedAt        time.Time
}

// ArtifactKindRegistry is the seam toward the bundle resolver. The
// real resolver provides a Register call; the policy engine calls it
// once at boot (plan §6 bootstrap order). The seam stays here so a
// missing or out-of-order resolver surface fails loudly rather than
// silently disabling enforcement.
type ArtifactKindRegistry interface {
	RegisterHandler(h ArtifactKindHandler) error
}

// MemoryKindRegistry is a test / dev stub for ArtifactKindRegistry.
type MemoryKindRegistry struct {
	mu       sync.Mutex
	handlers map[string]ArtifactKindHandler
}

// NewMemoryKindRegistry constructs a MemoryKindRegistry.
func NewMemoryKindRegistry() *MemoryKindRegistry {
	return &MemoryKindRegistry{handlers: map[string]ArtifactKindHandler{}}
}

// RegisterHandler implements ArtifactKindRegistry.
func (r *MemoryKindRegistry) RegisterHandler(h ArtifactKindHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handlers[h.Kind()]; ok {
		return errors.New("policy: bundle handler already registered for kind " + h.Kind())
	}
	r.handlers[h.Kind()] = h
	return nil
}

// Dispatch invokes the registered handler for the given raw artifact.
// Returns ErrKindNotRegistered when no handler claims the kind.
func (r *MemoryKindRegistry) Dispatch(ctx context.Context, raw RawArtifact) error {
	r.mu.Lock()
	h, ok := r.handlers[raw.Kind]
	r.mu.Unlock()
	if !ok {
		return ErrKindNotRegistered
	}
	return h.Handle(ctx, raw)
}

// ErrKindNotRegistered surfaces when the bundle resolver tries to
// dispatch a kind whose handler has not yet registered (the
// "bootstrap order" check in WP05).
var ErrKindNotRegistered = errors.New("policy: artifact kind not registered")
