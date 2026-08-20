// Package trust public engine surface.
//
// See plan §3 (Public API) and §2.1 (package layout) at
// kitty-specs/a2a-signed-cards-trust-01KQ18P9/plan.md. The interface here is
// the *only* surface external packages may consume per DIRECTIVE_001 and
// charter C-001.
package trust

import (
	"context"
	"time"
)

// TrustEngine is the only entry point external packages use (plan §3).
//
// Implementation invariants:
//   - Verify always returns exactly one [VerificationResult]; never
//     returns (nil, nil) and never returns a zero-value Decision (NFR-005,
//     C-005).
//   - Every Verify call emits exactly one append-only audit event into
//     core/event/.
//   - Sign fails closed when the backend reports HealthUnavailable or
//     HealthDegraded; never silently substitutes an alternative backend
//     (NFR-007).
type TrustEngine interface {
	// Verify a payload + signature envelope against the engine's
	// configured anchors and policies (FR-002, FR-012).
	Verify(ctx context.Context, payload []byte, env Envelope, opts VerifyOptions) (VerificationResult, error)

	// Sign a payload using the named local identity (FR-001, FR-008).
	Sign(ctx context.Context, payload []byte, ident IdentityRef, opts SignOptions) (Envelope, error)

	// InstallAnchor registers a new trust anchor (FR-003). Each call
	// emits an audit event.
	InstallAnchor(ctx context.Context, a Anchor) error

	// RemoveAnchor marks an anchor as removed (FR-003). Removal is
	// distinguishable from "never installed" — verify against a removed
	// anchor returns RejAnchorRemoved, not RejAnchorMissing.
	RemoveAnchor(ctx context.Context, anchorID string) error

	// ListAnchors returns the live anchor set, excluding tombstoned
	// (Removed) entries. Used by `harness trust status`.
	ListAnchors(ctx context.Context) ([]Anchor, error)

	// BeginRotation stamps PreviousKey + OverlapEnds onto the anchor
	// (FR-005, FR-013). During the overlap, verification accepts either
	// key and flags grace-period acceptances in audit.
	BeginRotation(ctx context.Context, anchorID string, newKey PublicKey, overlap time.Duration) error

	// CompleteRotation purges PreviousKey (FR-005). Called explicitly by
	// the operator or implicitly by the rotation manager when
	// OverlapEnds elapses.
	CompleteRotation(ctx context.Context, anchorID string) error

	// IngestRevocation accepts a signed revocation record into the
	// cache (FR-006, FR-007). Subsequently, any verify call against the
	// referenced key or identity returns RejKeyRevoked.
	IngestRevocation(ctx context.Context, rec RevocationRecord) error

	// Preflight is invoked by core/config/ at startup (FR-014).
	// Returns structured findings the operator can act on; severity
	// SeverityError is expected to fail harness startup.
	Preflight(ctx context.Context) ([]PreflightFinding, error)
}

// Config is passed to [NewEngine] to construct the production engine.
// Concrete fields are filled in over WP02–WP07 — at WP01 only the type
// shell exists so callers can compile.
type Config struct {
	// AlgorithmPolicy enumerates which Algorithms verification will
	// accept (FR-004). Default per Open Question 2: Ed25519-only.
	AlgorithmPolicy AlgorithmPolicy
	// ClockSkew bounds the issued_at / expires_at tolerance (FR-016).
	ClockSkew ClockSkewPolicy
	// ChainDepth bounds the maximum allowed envelope chain length.
	ChainDepth ChainDepthPolicy
	// Anchors is the persistence seam for the engine's AnchorStore
	// (bundle-download-and-verify-01PMZ909 UNIT-3, spec §1.7 F-1). Nil
	// (the zero value) defaults to an in-memory store scoped to this
	// engine instance — the v1.0 behaviour every existing caller and
	// test already gets. Production callers with a real data
	// directory pass [NewSQLiteAnchorStore] so an installed anchor
	// survives a relaunch; the test-harness path (no DataDir) is left
	// on the in-memory default. There is deliberately no package-level
	// setter — the store is a construction-time dependency, not
	// process-global state.
	Anchors AnchorStore
}

// NewEngine constructs a verify-only [TrustEngine] with an in-memory
// emitter and no signing dispatcher. Useful when the harness only
// consumes signed surfaces and never mints them locally. Sign attempts
// return [ErrBackendNotRegistered].
//
// For a sign-capable engine, use [NewEngineWithEmitter] with a
// configured backend dispatcher and the production event emitter.
func NewEngine(cfg Config) (TrustEngine, error) {
	return NewEngineWithEmitter(cfg, nil, NewMemoryEmitter())
}
