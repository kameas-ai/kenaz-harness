package acp

import "errors"

// Typed error taxonomy. Subpackages classify failures into one of these
// sentinel errors (or a wrapping chain that contains one of them); the
// public surface returns the typed errors verbatim so callers can use
// errors.Is to distinguish error categories.

var (
	// ErrPeerNotFound means the peer_id is not in the registry. FR-004.
	ErrPeerNotFound = errors.New("acp: peer not found")
	// ErrCredentialResolution means the indirect credential reference
	// could not be resolved (env unset, keychain entry missing, etc.).
	// FR-006, FR-018.
	ErrCredentialResolution = errors.New("acp: credential resolution failed")
	// ErrPolicyDenied means the policy engine refused this call.
	// FR-013, FR-014.
	ErrPolicyDenied = errors.New("acp: policy denied")
	// ErrSkillNotFound means the requested skill is unknown.
	// FR-005, FR-014.
	ErrSkillNotFound = errors.New("acp: skill not found")
	// ErrUnsupportedProtocolVersion means the peer advertised a version
	// the harness's SDK cannot speak. FR-001.
	ErrUnsupportedProtocolVersion = errors.New("acp: unsupported protocol version")
	// ErrTransportRefused means the transport layer refused this address
	// (e.g., a non-loopback host on http_loopback). FR-007, NFR-006.
	ErrTransportRefused = errors.New("acp: transport refused")
	// ErrCancelled means the task was cancelled before completion.
	// FR-010.
	ErrCancelled = errors.New("acp: cancelled")
	// ErrTaskFailed means the task terminated in a Failed state.
	// FR-009.
	ErrTaskFailed = errors.New("acp: task failed")
	// ErrSchemaViolation means a Skill's input/output payload did not
	// match the declared JSON Schema.
	ErrSchemaViolation = errors.New("acp: schema violation")
	// ErrIllegalStateTransition means a caller attempted to move a Task
	// state in a way the lifecycle forbids (backwards / out of a terminal
	// state). FR-009.
	ErrIllegalStateTransition = errors.New("acp: illegal state transition")
	// ErrVerifierRejected means the Verifier seam refused to accept a
	// fetched AgentCard. FR-020.
	ErrVerifierRejected = errors.New("acp: verifier rejected card")
)

// IsTransient classifies whether err denotes a transient condition that a
// caller may reasonably retry. The protocol layer never retries on the
// caller's behalf; this is a hint for the workflow layer.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrCancelled),
		errors.Is(err, ErrPolicyDenied),
		errors.Is(err, ErrPeerNotFound),
		errors.Is(err, ErrSkillNotFound),
		errors.Is(err, ErrUnsupportedProtocolVersion),
		errors.Is(err, ErrTransportRefused),
		errors.Is(err, ErrSchemaViolation),
		errors.Is(err, ErrIllegalStateTransition),
		errors.Is(err, ErrVerifierRejected),
		errors.Is(err, ErrCredentialResolution):
		return false
	default:
		// Unclassified failures are treated as transient by default to
		// avoid silent permanent failures of the workflow layer.
		return true
	}
}

// IsRefusal reports whether err is a categorical refusal (policy or
// transport) rather than a downstream failure.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrPolicyDenied) ||
		errors.Is(err, ErrTransportRefused) ||
		errors.Is(err, ErrUnsupportedProtocolVersion) ||
		errors.Is(err, ErrVerifierRejected)
}
