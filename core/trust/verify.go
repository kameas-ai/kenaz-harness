package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/trust/internal/algo"
)

// verifyDeps bundles the read-side collaborators the verify pipeline
// needs. The engine constructs one of these and passes it down so unit
// tests can substitute fakes for [AnchorStore] and [RevocationCache].
type verifyDeps struct {
	algo        *algo.Registry
	anchors     AnchorStore
	revocations RevocationCache
	identities  IdentityCache
	policy      AlgorithmPolicy
	clockSkew   ClockSkewPolicy
	chainDepth  ChainDepthPolicy
	emitter     Emitter
}

// IdentityCache implements FR-015 — binding agent_id ↔ public-key
// fingerprint. The first time an agent_id is seen, the binding is
// recorded; a later presentation of the same agent_id with a *different*
// fingerprint is rejected as RejIdentityCollision.
//
// Legitimate rotation uses BeginRotation, which updates the binding
// through the engine rather than triggering this collision.
type IdentityCache interface {
	// Bind associates agent_id with fingerprint. Returns
	// [ErrIdentityCollision] when a different fingerprint is already
	// bound. A repeat of the same binding is idempotent.
	Bind(ctx context.Context, agentID, fingerprint string) error
	// Lookup returns the fingerprint currently bound to agent_id, or
	// the empty string when no binding exists.
	Lookup(ctx context.Context, agentID string) string
}

// memIdentityCache is the v1.0 in-memory identity cache backing FR-015.
type memIdentityCache struct {
	bindings map[string]string
}

func newMemIdentityCache() *memIdentityCache {
	return &memIdentityCache{bindings: make(map[string]string)}
}

func (m *memIdentityCache) Bind(_ context.Context, agentID, fingerprint string) error {
	if agentID == "" || fingerprint == "" {
		return nil
	}
	if existing, ok := m.bindings[agentID]; ok {
		if existing != fingerprint {
			return ErrIdentityCollision
		}
		return nil
	}
	m.bindings[agentID] = fingerprint
	return nil
}

func (m *memIdentityCache) Lookup(_ context.Context, agentID string) string {
	return m.bindings[agentID]
}

// runVerify implements the canonical 10-step fail-fast pipeline from
// plan §4.2.
//
// Order matters — cheap checks first, signature math last:
//
//  1. Envelope shape
//  2. Algorithm policy gate
//  3. Chain-depth gate
//  4. Anchor lookup (anchor_missing | anchor_removed)
//  5. Revocation cache (key_revoked)
//  6. Clock-skew window (clock_skew_exceeded)
//  7. Signature math (signature_invalid)
//  8. Rotation overlap (sets CacheState=grace if previous-key path)
//  9. Identity-collision check (identity_collision)
// 10. Audit emit (verification-accepted | verification-rejected)
//
// Step 11 returns the [VerificationResult]. Invariant: exactly one
// VerificationResult ↔ exactly one audit emission (NFR-005, C-005).
func runVerify(ctx context.Context, d verifyDeps, payload []byte, env Envelope, opts VerifyOptions) (VerificationResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	policy := d.policy
	if opts.PolicyOverride != nil {
		policy = *opts.PolicyOverride
	}
	hash := sha256Hex(payload)

	reject := func(code RejectionCode, detail string) (VerificationResult, error) {
		res := VerificationResult{
			Decision:      DecisionRejected,
			Algorithm:     env.Algorithm,
			RejectionCode: code,
			Detail:        detail,
			EvaluatedAt:   now,
			PayloadHash:   hash,
		}
		emitAuditOrLog(ctx, d.emitter, Event{
			Kind:        EventVerificationRejected,
			OccurredAt:  now,
			Algorithm:   env.Algorithm,
			Decision:    DecisionRejected,
			Rejection:   code,
			PayloadHash: hash,
			Detail:      detail,
		})
		return res, NewRejection(code, detail)
	}

	// Step 1: envelope shape (plan §4.2 step 1).
	if err := ValidateEnvelopeShape(env); err != nil {
		return reject(RejSignatureInvalid, "envelope shape invalid")
	}

	// Step 2: algorithm policy gate (FR-004).
	if !policy.Permits(env.Algorithm) {
		return reject(RejAlgorithmNotPermit, "algorithm not in operator allow-list")
	}

	// Step 3: chain-depth gate (defense-in-depth).
	if !d.chainDepth.Permits(len(env.Chain)) {
		return reject(RejChainDepthExceeded, "chain depth exceeds policy")
	}

	// Step 4: anchor lookup (anchor_missing | anchor_removed).
	anchor, err := d.anchors.FindByKeyID(ctx, env.KeyID)
	if err != nil {
		if errors.Is(err, ErrAnchorNotFound) {
			return reject(RejAnchorMissing, "no anchor matches envelope key_id")
		}
		return reject(RejAnchorMissing, err.Error())
	}
	if anchor.Removed {
		return reject(RejAnchorRemoved, "anchor was removed by operator")
	}

	// Step 5: revocation cache (FR-006). Revocation by key id OR identity.
	if revoked, _ := d.revocations.IsRevoked(ctx, env.KeyID, env.AgentID, now); revoked {
		return reject(RejKeyRevoked, "subject is revoked")
	}

	// Step 6: clock-skew window (FR-016).
	if !d.clockSkew.Within(env.IssuedAt, now) {
		return reject(RejClockSkewExceeded, "issued_at outside tolerance")
	}
	if !env.ExpiresAt.IsZero() && now.After(env.ExpiresAt.Add(d.clockSkew.Tolerance)) {
		return reject(RejClockSkewExceeded, "expires_at exceeded")
	}

	// Step 7 + 8: signature math — try current key first, then previous
	// key during rotation overlap. Plan §4.2 step 8: previous-key
	// acceptance flips CacheState to grace and is recorded.
	var verifyKey PublicKey
	cacheState := CacheFresh
	if anchor.PublicKey.Fingerprint == env.KeyID {
		verifyKey = anchor.PublicKey
	} else if anchor.PreviousKey != nil && anchor.PreviousKey.Fingerprint == env.KeyID {
		if !withinGrace(anchor, now) {
			return reject(RejKeyExpired, "previous key outside rotation overlap")
		}
		verifyKey = *anchor.PreviousKey
		cacheState = CacheGrace
	} else {
		// The fingerprint is not the anchor's current or previous key.
		// Defensive — should be caught by FindByKeyID, but if a stale
		// secondary index returns the wrong row, we surface anchor_missing.
		return reject(RejAnchorMissing, "envelope key_id not bound to resolved anchor")
	}

	if err := d.algo.Verify(env.Algorithm, verifyKey.Bytes, payload, env.Signature); err != nil {
		var re *RejectionError
		if errors.As(err, &re) {
			return reject(re.Code, re.Detail)
		}
		if errors.Is(err, ErrAlgorithmNotImplemented) || errors.Is(err, ErrAlgorithmNotSupported) {
			return reject(RejAlgorithmNotPermit, err.Error())
		}
		return reject(RejSignatureInvalid, err.Error())
	}

	// Step 9: identity-collision check (FR-015) — bind the agent_id ↔
	// fingerprint or refuse a conflicting binding.
	if env.AgentID != "" {
		if err := d.identities.Bind(ctx, env.AgentID, env.KeyID); err != nil {
			if errors.Is(err, ErrIdentityCollision) {
				return reject(RejIdentityCollision, "agent_id bound to a different fingerprint")
			}
			return reject(RejSignatureInvalid, err.Error())
		}
	}

	// Step 10: audit emit (NFR-005 — exactly one event per result).
	res := VerificationResult{
		Decision:    DecisionAccepted,
		AnchorID:    anchor.AnchorID,
		Algorithm:   env.Algorithm,
		CacheState:  cacheState,
		EvaluatedAt: now,
		PayloadHash: hash,
	}
	emitAuditOrLog(ctx, d.emitter, Event{
		Kind:        EventVerificationAccepted,
		OccurredAt:  now,
		AnchorID:    anchor.AnchorID,
		Algorithm:   env.Algorithm,
		CacheState:  cacheState,
		Decision:    DecisionAccepted,
		PayloadHash: hash,
	})
	return res, nil
}

// emitAuditOrLog drops emission errors silently — audit emission must
// not change the verification outcome (NFR-005 requires the entry to
// exist; tests assert that). A future iteration can route emit errors
// to a dead-letter queue, but at v1.0 the in-process emitter is
// infallible and the production event-log emitter handles its own
// retries.
func emitAuditOrLog(ctx context.Context, e Emitter, ev Event) {
	if e == nil {
		return
	}
	_ = e.Emit(ctx, ev)
}

// sha256Hex returns the lower-case hex SHA-256 of payload — used as the
// PayloadHash field on [VerificationResult] and the audit Event.
func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
