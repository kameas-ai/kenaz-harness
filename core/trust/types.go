package trust

import "time"

// Algorithm identifies a signing/verification algorithm. Stable string
// constants conform to JOSE-style names so they can travel in envelopes.
//
// Implements FR-004 (algorithm policy) — the set is fixed at v1.0 with
// only Ed25519 implemented; ECDSA-P256 and RSA-PSS are interface-conformant
// slots filled in v1.x per plan §1.4.
type Algorithm string

const (
	// AlgEd25519 — RFC 8032 Ed25519. Default and only implemented at v1.0.
	AlgEd25519 Algorithm = "ed25519"
	// AlgECDSAP256 — interface slot for ECDSA over NIST P-256 / SHA-256.
	AlgECDSAP256 Algorithm = "ecdsa-p256"
	// AlgRSAPSSSHA256 — interface slot for RSA-PSS / SHA-256.
	AlgRSAPSSSHA256 Algorithm = "rsa-pss-sha256"
)

// String implements fmt.Stringer.
func (a Algorithm) String() string { return string(a) }

// BackendKind names a registered [SigningBackend].
//
// FR-008 (backend abstraction) — the dispatcher maps IdentityRef.Backend to
// the registered backend with this kind.
type BackendKind string

const (
	BackendSoftware   BackendKind = "software"
	BackendOSKeychain BackendKind = "oskeychain"
	BackendAWSKMS     BackendKind = "awskms"
	BackendYubiKey    BackendKind = "yubikey"
	BackendPKCS11     BackendKind = "pkcs11"
)

// String implements fmt.Stringer.
func (b BackendKind) String() string { return string(b) }

// HealthStatus is the backend-availability state observed by [SigningBackend.Health].
//
// NFR-007 — only Health=HealthOK is acceptable for a sign operation.
// HealthUnavailable causes the dispatcher to fail closed.
type HealthStatus int

const (
	// HealthUnavailable — backend cannot perform sign operations now.
	HealthUnavailable HealthStatus = iota
	// HealthDegraded — backend is reachable but reporting reduced
	// functionality; the dispatcher treats this as a refusal at v1.0.
	HealthDegraded
	// HealthOK — backend is fully operational.
	HealthOK
)

// String renders the health value for audit/debug output.
func (h HealthStatus) String() string {
	switch h {
	case HealthOK:
		return "ok"
	case HealthDegraded:
		return "degraded"
	case HealthUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

// PublicKey is a backend-agnostic representation of a verifying key.
// The Bytes are the algorithm-natural encoding (e.g. raw 32-byte Ed25519
// public key). Fingerprint is a SHA-256 over Bytes computed by
// internal/fingerprint and travels in envelopes as key_id.
//
// Public keys are explicitly public — they may freely appear in audit and
// configuration, satisfying charter C-002 (no plaintext *private* keys).
type PublicKey struct {
	Algorithm   Algorithm
	Bytes       []byte
	Fingerprint string
}

// IdentityRef tells the sign dispatcher which backend to call and what key
// to use. Path is the backend-defined locator (keychain entry name, KMS
// key ARN, PIV slot, etc). Per C-002 it is *not* a key blob.
type IdentityRef struct {
	Backend   BackendKind
	Path      string
	Algorithm Algorithm
	// AnchorID, if non-empty, ties the signing identity to a known anchor
	// in the trust store. Useful when the same key is registered as both
	// a local signer and a peer anchor (loopback test mode).
	AnchorID string
}

// BackendRef is the subset of IdentityRef passed to [SigningBackend.Sign]
// and [SigningBackend.PublicKey]. The dispatcher strips the BackendKind
// (already resolved by then).
type BackendRef struct {
	Path     string
	Metadata map[string]string
}

// AnchorKind names how an [Anchor] participates in trust resolution
// (FR-003). Precedence at lookup time: PinnedPeer > OrgIdentifier >
// RawPublicKey.
type AnchorKind int

const (
	// AnchorRawPublicKey — operator pinned an explicit public key with
	// metadata. Lowest-precedence binding.
	AnchorRawPublicKey AnchorKind = iota
	// AnchorOrgIdentifier — operator declared trust by org id; the public
	// key is resolved through a configured discovery mechanism.
	AnchorOrgIdentifier
	// AnchorPinnedPeer — operator pinned a specific peer id with its
	// public key. Highest-precedence.
	AnchorPinnedPeer
)

// String renders the kind for audit emission.
func (k AnchorKind) String() string {
	switch k {
	case AnchorRawPublicKey:
		return "raw_public_key"
	case AnchorOrgIdentifier:
		return "org_identifier"
	case AnchorPinnedPeer:
		return "pinned_peer"
	default:
		return "unknown"
	}
}

// Anchor is the unit of operator-configured trust (FR-003).
//
// Rotation state (PreviousKey + OverlapEnds) is set during a rotation
// window; the verify pipeline accepts either the previous or the new key
// during the window and flags grace-period acceptances per FR-013.
type Anchor struct {
	AnchorID    string
	Kind        AnchorKind
	PublicKey   PublicKey
	OrgID       string
	PeerID      string
	Algorithm   Algorithm
	InstalledAt time.Time
	InstalledBy string
	Metadata    map[string]string
	// Rotation state — non-nil only when mid-rotation.
	PreviousKey *PublicKey
	OverlapEnds *time.Time
	// Removed marks an anchor whose rows survive for audit-history
	// purposes but no longer participate in verification. Lookup of a
	// Removed anchor produces RejAnchorRemoved (distinct from
	// RejAnchorMissing — see plan §4.2 step 4 + spec edge cases).
	Removed bool
}

// Decision is the binary outcome of a verification call (FR-002).
type Decision int

const (
	// DecisionUnknown is the zero value; never returned by a successful
	// Verify call.
	DecisionUnknown Decision = iota
	DecisionAccepted
	DecisionRejected
)

// String renders the decision for audit emission.
func (d Decision) String() string {
	switch d {
	case DecisionAccepted:
		return "accepted"
	case DecisionRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// CacheState reports how the verification path obtained its anchor binding
// (FR-013 grace-period signal). It is captured in audit so an operator can
// see "N peers still on the previous key."
type CacheState int

const (
	// CacheFresh — verified against the anchor's current key.
	CacheFresh CacheState = iota
	// CacheGrace — verified against the previous key during a rotation
	// overlap window; the audit entry will be flagged.
	CacheGrace
	// CacheStale — anchor is in a state that requires a fresh fetch
	// (reserved for future revocation-freshness checks).
	CacheStale
)

// String renders the cache state for audit emission.
func (c CacheState) String() string {
	switch c {
	case CacheFresh:
		return "fresh"
	case CacheGrace:
		return "grace"
	case CacheStale:
		return "stale"
	default:
		return "unknown"
	}
}

// VerificationResult is the typed outcome of [TrustEngine.Verify] (FR-017).
//
// Invariant — exactly one of the following holds and is enforced by a
// contract test in WP03:
//   - Decision == DecisionAccepted ∧ AnchorID != "" ∧ RejectionCode == ""
//   - Decision == DecisionRejected ∧ RejectionCode != ""
type VerificationResult struct {
	Decision      Decision
	AnchorID      string
	Algorithm     Algorithm
	CacheState    CacheState
	RejectionCode RejectionCode
	Detail        string
	EvaluatedAt   time.Time
	// PayloadHash is a SHA-256 over the canonical payload bytes; it is
	// what flows into the audit entry per plan §5.2 (no raw payload in
	// the event log).
	PayloadHash string
}

// Envelope is the A2A v1 Signed Agent Cards wire envelope adapted for the
// engine. Real shape conformance (and potential a2a-go integration) lands
// in WP09 inside core/trust/envelope/. Field set tracks plan §5.3.
type Envelope struct {
	Payload             []byte
	Signature           []byte
	Algorithm           Algorithm
	KeyID               string
	IssuedAt            time.Time
	ExpiresAt           time.Time
	Chain               []string
	KeyDistributionHint string
	// AgentID is extracted from the canonicalized payload so the verify
	// pipeline can run identity-collision detection (FR-015) without
	// re-parsing the payload.
	AgentID string
}

// VerifyOptions tunes a single Verify call. Defaults are taken from the
// engine's configured policies (FR-004 algorithm allow-list, FR-016
// clock-skew tolerance).
type VerifyOptions struct {
	// Now overrides the verifier's clock for determinism in tests; if
	// zero, time.Now() is used.
	Now time.Time
	// PolicyOverride, when non-nil, replaces the engine's algorithm
	// policy for this single call. Used by integration tests; never
	// expected on a production path.
	PolicyOverride *AlgorithmPolicy
}

// SignOptions tunes a single Sign call.
type SignOptions struct {
	IssuedAt   time.Time
	Lifetime   time.Duration
	AgentID    string
	ChainHints []string
}

// RevocationSubject identifies what a [RevocationRecord] applies to
// (FR-006). Both subjects route through the same revocation cache.
type RevocationSubject int

const (
	// RevocationByKeyID — revokes by canonical public-key fingerprint.
	RevocationByKeyID RevocationSubject = iota
	// RevocationByIdentity — revokes the entire identity (agent_id),
	// covering any future re-keyings of the same identity until cleared.
	RevocationByIdentity
)

// String renders the revocation subject for audit emission.
func (r RevocationSubject) String() string {
	switch r {
	case RevocationByKeyID:
		return "key_id"
	case RevocationByIdentity:
		return "identity"
	default:
		return "unknown"
	}
}

// RevocationRecord is a signed assertion that a key id or identity is no
// longer trusted as of EffectiveAt (FR-006).
type RevocationRecord struct {
	RevocationID string
	SubjectKind  RevocationSubject
	SubjectID    string
	EffectiveAt  time.Time
	IngestedAt   time.Time
	Reason       string
	IssuedBy     string
	Signature    []byte
}

// PreflightSeverity classifies a [PreflightFinding] (FR-014).
type PreflightSeverity int

const (
	// SeverityWarning — surface the finding but do not block startup.
	SeverityWarning PreflightSeverity = iota
	// SeverityError — block harness startup with a non-zero exit (per
	// plan §6.5).
	SeverityError
)

// String renders the severity for audit emission.
func (s PreflightSeverity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// PreflightFinding is one entry in the [TrustEngine.Preflight] result
// (FR-014). Code is one of a small fixed set
// (backend_unavailable, anchor_algorithm_unsupported, anchor_key_malformed,
// revocation_endpoint_unreachable, …) so operators can alert on it.
type PreflightFinding struct {
	Code     string
	Severity PreflightSeverity
	AnchorID string
	Backend  BackendKind
	Detail   string
}
