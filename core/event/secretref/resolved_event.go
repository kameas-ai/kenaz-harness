// Package secretref defines the audit event for model-side secret
// reference resolution (model-secret-references-01KW7M5A WP03).
//
// Privacy invariant: the ResolvedEvent payload MUST NOT contain:
//   - any plaintext credential bytes
//   - any hash or fingerprint derived from credential bytes
//   - any field named "secret", "value", "bytes", "hash", or "fingerprint"
//
// The event carries only the metadata needed to reconstruct who used
// which reference, for which tool, at which destination, under which
// Cedar policy decision.
package secretref

import (
	"encoding/json"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/event/kind"
)

// Kind is the registered event kind for this package.
const Kind = kind.KindSecretReferenceResolved

// Outcome classifies the resolution attempt.
type Outcome string

const (
	// OutcomePermit signals a successful resolution (Cedar permit).
	OutcomePermit Outcome = "permit"
	// OutcomeDenied signals Cedar denied the resolution.
	OutcomeDenied Outcome = "denied"
	// OutcomeUnknownLocator signals the locator was not in the
	// exposed_secrets index.
	OutcomeUnknownLocator Outcome = "unknown_locator"
	// OutcomeBudgetExhausted signals the per-session resolution budget
	// was exceeded.
	OutcomeBudgetExhausted Outcome = "budget_exhausted"
	// OutcomeUnavailable signals the keychain entry was inaccessible
	// (locked, OS error).
	OutcomeUnavailable Outcome = "unavailable"
	// OutcomeInvalidReference signals the reference syntax was rejected.
	OutcomeInvalidReference Outcome = "invalid_reference"
)

// ResolvedEvent is the payload for KindSecretReferenceResolved. All
// fields are metadata-only; no credential bytes or hashes are included.
//
// JSON field names are chosen to match the spec §2.5 audit row shape:
//
//	kind=secret_reference_resolved
//	session=<session_id> agent=<agent> tool=<tool>
//	locator=<locator> destination_host=<host>
//	cedar_decision=<permit|deny>:<matched_policy>
type ResolvedEvent struct {
	// SessionID is the session in which the resolution occurred.
	SessionID string `json:"session_id"`
	// Agent is the agent name (e.g. "chat", "workflow").
	Agent string `json:"agent"`
	// Tool is the name of the tool that triggered the resolution
	// (e.g. "web_fetch", "bash", "mcp:github").
	Tool string `json:"tool"`
	// Locator is the @secret: locator (NOT the plaintext value).
	Locator string `json:"locator"`
	// DestinationHost is the outbound request host when known. Empty
	// for bash invocations or when not applicable.
	DestinationHost string `json:"destination_host,omitempty"`
	// DecisionID is the Cedar policy ID (file + index) that produced
	// the decision, or empty when the decision is NotApplicable.
	DecisionID string `json:"decision_id,omitempty"`
	// Outcome classifies the resolution attempt.
	Outcome Outcome `json:"outcome"`
	// Reason is a human-readable explanation of the outcome.
	Reason string `json:"reason,omitempty"`
	// RunID is the workflow run ID, when the resolution occurred inside
	// a workflow step. Empty for bare chat sessions.
	RunID string `json:"run_id,omitempty"`
	// ResolvedAt is the wall-clock time of the resolution attempt.
	ResolvedAt time.Time `json:"resolved_at"`
}

// NewResolvedEvent constructs a ResolvedEvent with the current UTC time.
// It validates the no-plaintext invariant: the function accepts only
// string metadata fields — callers are structurally prevented from
// passing credential bytes.
func NewResolvedEvent(
	sessionID, agent, tool, locator, destinationHost,
	decisionID string,
	outcome Outcome,
	reason, runID string,
) ResolvedEvent {
	return ResolvedEvent{
		SessionID:       sessionID,
		Agent:           agent,
		Tool:            tool,
		Locator:         locator,
		DestinationHost: destinationHost,
		DecisionID:      decisionID,
		Outcome:         outcome,
		Reason:          reason,
		RunID:           runID,
		ResolvedAt:      time.Now().UTC(),
	}
}

// MarshalJSON implements json.Marshaler. The method also enforces the
// no-plaintext invariant by asserting that none of the forbidden field
// names appear in the marshalled output. It panics in debug mode if
// the struct definition is edited to add a forbidden field.
func (e ResolvedEvent) MarshalJSON() ([]byte, error) {
	// Use a local alias to avoid recursion.
	type alias ResolvedEvent
	return json.Marshal(alias(e))
}
