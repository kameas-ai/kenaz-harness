package trust

import (
	"errors"
	"fmt"
)

// RejectionCode is the stable taxonomy of verification rejection reasons
// per FR-017 plus chain_depth_exceeded (defense-in-depth, spec edge case).
//
// The set is append-only — codes are never renamed or removed across
// versions so operator alerting and dashboards remain stable.
type RejectionCode string

const (
	// RejSignatureInvalid — envelope failed shape parse, key/signature
	// pairing failed, or signature math returned an error.
	RejSignatureInvalid RejectionCode = "signature_invalid"
	// RejAlgorithmNotPermit — envelope.Algorithm is not in the configured
	// allow-list (FR-004).
	RejAlgorithmNotPermit RejectionCode = "algorithm_not_permitted"
	// RejAnchorMissing — no anchor matches the envelope's key id /
	// org id / peer id.
	RejAnchorMissing RejectionCode = "anchor_missing"
	// RejAnchorRemoved — an anchor that *was* trusted has since been
	// removed; distinct from RejAnchorMissing so audit can distinguish
	// the two (spec edge case).
	RejAnchorRemoved RejectionCode = "anchor_removed"
	// RejKeyRevoked — a revocation record applies to the signing key id
	// or identity (FR-006).
	RejKeyRevoked RejectionCode = "key_revoked"
	// RejKeyExpired — rotation overlap window has lapsed and the
	// envelope was signed with the now-retired previous key (FR-005).
	RejKeyExpired RejectionCode = "key_expired"
	// RejIdentityCollision — a different public key was previously bound
	// to the same agent_id (FR-015). The *later* presentation is
	// rejected; legitimate rotation routes through BeginRotation.
	RejIdentityCollision RejectionCode = "identity_collision"
	// RejClockSkewExceeded — issued_at / expires_at fell outside the
	// configured tolerance (FR-016).
	RejClockSkewExceeded RejectionCode = "clock_skew_exceeded"
	// RejChainDepthExceeded — envelope chain length exceeded operator
	// policy (defense-in-depth, spec edge case).
	RejChainDepthExceeded RejectionCode = "chain_depth_exceeded"
)

// String implements fmt.Stringer.
func (r RejectionCode) String() string { return string(r) }

// AllRejectionCodes returns every defined RejectionCode in declaration
// order. Used by tests to assert taxonomy coverage (DIRECTIVE_036).
func AllRejectionCodes() []RejectionCode {
	return []RejectionCode{
		RejSignatureInvalid,
		RejAlgorithmNotPermit,
		RejAnchorMissing,
		RejAnchorRemoved,
		RejKeyRevoked,
		RejKeyExpired,
		RejIdentityCollision,
		RejClockSkewExceeded,
		RejChainDepthExceeded,
	}
}

// RejectionError is the typed error returned alongside a
// [VerificationResult] when Decision == DecisionRejected. Callers can
// errors.As() to inspect the code without re-parsing strings.
type RejectionError struct {
	Code   RejectionCode
	Detail string
}

// Error implements the error interface.
func (e *RejectionError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("trust: rejected (%s)", e.Code)
	}
	return fmt.Sprintf("trust: rejected (%s): %s", e.Code, e.Detail)
}

// NewRejection constructs a typed rejection error.
func NewRejection(code RejectionCode, detail string) *RejectionError {
	return &RejectionError{Code: code, Detail: detail}
}

// Sentinel errors used across the engine and backend boundary. They are
// stable identities so callers can errors.Is() against them.
var (
	// ErrNotImplemented is returned by stubs WP01 ships before the
	// concrete implementation lands.
	ErrNotImplemented = errors.New("trust: not implemented")

	// ErrBackendUnavailable signals fail-closed per NFR-007.
	ErrBackendUnavailable = errors.New("trust: signing backend unavailable")

	// ErrBackendNotRegistered means IdentityRef.Backend has no
	// registered factory in the process; surfaces during sign dispatch.
	ErrBackendNotRegistered = errors.New("trust: backend not registered")

	// ErrAlgorithmNotImplemented is returned by interface-conformant
	// algorithm slots that do not yet have a real implementation
	// (FR-004 phasing — ECDSA-P256 / RSA-PSS at v1.0).
	ErrAlgorithmNotImplemented = errors.New("trust: algorithm not implemented")

	// ErrAlgorithmNotSupported is returned when a backend cannot
	// produce signatures for the requested algorithm.
	ErrAlgorithmNotSupported = errors.New("trust: algorithm not supported by backend")

	// ErrInvalidEnvelope means the envelope failed shape validation —
	// the verify pipeline maps this to RejSignatureInvalid.
	ErrInvalidEnvelope = errors.New("trust: invalid envelope")

	// ErrAnchorNotFound is returned by the anchor store when no row
	// matches.
	ErrAnchorNotFound = errors.New("trust: anchor not found")

	// ErrIdentityCollision is returned by anchor / identity stores when
	// a different fingerprint is already bound to the agent_id (FR-015).
	ErrIdentityCollision = errors.New("trust: identity collision")
)
