// Package verify is the FR-020 stable seam for AgentCard verification.
//
// v1 ships UnsignedAcceptVerifier as the default: every fetched card is
// accepted. The a2a-signed-cards-trust-01KQ18P9 mission swaps in a real
// verifier without touching envelope or transport packages.
//
// CRITICAL: Per plan §8 R8, the http_public transport refuses to dial
// or listen when the configured verifier is the default no-op
// (UnsignedAcceptVerifier). That refusal is enforced at the transport
// constructor — see core/acp/transports/http_public/.
package verify

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/acp"
)

// UnsignedAcceptVerifier accepts every fetched card as-is. Suitable for
// loopback / LAN deployments under one operator's control. NEVER
// suitable for cross-organization trust; the http_public transport
// refuses to construct against this verifier (plan R8).
type UnsignedAcceptVerifier struct{}

// Verify always returns Accepted=true.
func (UnsignedAcceptVerifier) Verify(_ context.Context, _ acp.AgentCard) (acp.VerifyResult, error) {
	return acp.VerifyResult{Accepted: true}, nil
}

// IsUnsignedAccept reports whether v is the no-op default. Transport
// packages use this to refuse public exposure with v1's default
// verifier (DIRECTIVE_001 / plan §8 R8 mitigation).
//
// In v1 we conservatively return refusal-to-verify for any
// http_public attempt regardless of the configured verifier, until the
// signed-cards mission ships a real one. See
// core/acp/transports/http_public.
func IsUnsignedAccept(v acp.Verifier) bool {
	_, ok := v.(UnsignedAcceptVerifier)
	return ok
}

// RefusalVerifier always returns Accepted=false with a stable reason.
// Used as the safety default for transports that must never carry an
// unverified card across the wire (e.g., http_public until the
// signed-cards mission lands).
type RefusalVerifier struct {
	Reason string
}

// Verify always returns Accepted=false.
func (r RefusalVerifier) Verify(_ context.Context, _ acp.AgentCard) (acp.VerifyResult, error) {
	reason := r.Reason
	if reason == "" {
		reason = "verification not yet available; refusing to verify"
	}
	return acp.VerifyResult{Accepted: false, Reason: reason}, acp.ErrVerifierRejected
}
