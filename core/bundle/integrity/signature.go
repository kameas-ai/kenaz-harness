package integrity

import (
	"context"
	"fmt"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// SigningPolicy controls signature verification behaviour.
type SigningPolicy int

const (
	// SigningOptional verifies signatures when present and ignores
	// their absence. Default v1 behaviour.
	SigningOptional SigningPolicy = iota
	// SigningRequired fails resolution if any bundle is unsigned.
	SigningRequired
	// SigningForbidden rejects any bundle that carries a signature.
	// Useful for environments where signature support is intentionally
	// not configured.
	SigningForbidden
)

// VerifyManifestSignatures hands every signature ref on m to the
// verifier and returns the first failure. The bundle layer never
// implements signature math — see plan §6.3 / D4.
//
// `verifier` may be a trust.NoopVerifier when the trust mission is not
// yet wired; in that case `policy == SigningRequired` returns
// ErrSignatureRequired.
func VerifyManifestSignatures(ctx context.Context, m *manifest.Manifest, verifier trust.Verifier, anchors []trust.Anchor, policy SigningPolicy) error {
	hasSig := len(m.Signatures) > 0
	if policy == SigningForbidden && hasSig {
		return fmt.Errorf("%w: signing-forbidden policy and bundle is signed", bundle.ErrSignatureInvalid)
	}
	if policy == SigningRequired && !hasSig {
		return fmt.Errorf("%w: bundle %s has no signatures", bundle.ErrSignatureRequired, m.Name)
	}
	if !hasSig {
		return nil
	}
	if verifier == nil {
		if policy == SigningRequired {
			return fmt.Errorf("%w: no verifier configured", bundle.ErrSignatureRequired)
		}
		return nil
	}
	payload := m.CanonicalBytes()
	for _, sig := range m.Signatures {
		req := trust.VerifyRequest{
			Payload: payload,
			Signature: trust.SignatureRef{
				Kind:      sig.Kind,
				Locator:   sig.Locator,
				Algorithm: sig.Algorithm,
				KeyID:     sig.KeyID,
			},
			Anchors: anchors,
		}
		res, err := verifier.Verify(ctx, req)
		if err != nil {
			return fmt.Errorf("%w: %v", bundle.ErrSignatureInvalid, err)
		}
		if !res.OK {
			if policy == SigningRequired {
				return fmt.Errorf("%w: %s (%s)", bundle.ErrSignatureRequired, res.Reason, sig.Locator)
			}
			// SigningOptional: a present-but-invalid signature still
			// fails — the operator opted-in by attaching the signature.
			return fmt.Errorf("%w: %s (%s)", bundle.ErrSignatureInvalid, res.Reason, sig.Locator)
		}
	}
	return nil
}
