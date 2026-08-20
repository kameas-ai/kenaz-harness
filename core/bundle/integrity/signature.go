package integrity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// SignatureResolver reads the raw detached-signature bytes a manifest
// SignatureRef.Locator refers to. core/trust never learns what a
// Locator means (verifier.go's own doc comment: "intentionally
// narrow"); resolving locators is this package's job because it is the
// layer that already knows what a bundle's on-disk (or channel-fetched)
// shape is — see research/request-shape.md, shape (a).
type SignatureResolver func(locator string) ([]byte, error)

// FileResolver returns a SignatureResolver that reads locator as a
// filesystem path. An absolute locator is read as-is; a relative
// locator is resolved against bundleRoot (the convention documented in
// research/signed-payload.md: "<bundleRoot>/kenaz.yaml.sig"). The
// caller supplies bundleRoot because it is the one that knows where the
// manifest came from (a local directory today; a channel-fetched
// staging directory once UNIT-5/6 land) — core/trust must never learn
// about filesystems (verifier.go:10-13).
func FileResolver(bundleRoot string) SignatureResolver {
	return func(locator string) ([]byte, error) {
		if locator == "" {
			return nil, fmt.Errorf("integrity: empty signature locator")
		}
		path := locator
		if !filepath.IsAbs(path) {
			path = filepath.Join(bundleRoot, locator)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("integrity: read signature %s: %w", path, err)
		}
		return b, nil
	}
}

// VerifyManifestSignatures hands every signature ref on m to the
// verifier and returns the first failure. The bundle layer never
// implements signature math — see plan §6.3 / D4.
//
// `verifier` may be a trust.NoopVerifier when the trust mission is not
// yet wired; in that case `policy == SigningRequired` returns
// ErrSignatureRequired.
//
// `resolve` supplies the raw signature bytes for each sig.Locator (see
// [SignatureResolver] / [FileResolver]). It may be nil — verification
// then proceeds with empty signature bytes, which the trust engine's
// envelope-shape check refuses (matches pre-UNIT-2 behaviour for
// callers that haven't adopted a resolver).
//
// The returned bool reports whether at least one signature was present
// AND verified OK — i.e. whether this call is a positive, real
// verification result, as opposed to "no signature to check" (false) or
// "signature present but rejected" (false, non-nil error). Callers
// persist this bit rather than deriving a trust tier from mere
// signature-ref presence (spec FR-006 / UNIT-4 G-2).
func VerifyManifestSignatures(ctx context.Context, m *manifest.Manifest, verifier trust.Verifier, anchors []trust.Anchor, policy SigningPolicy, resolve SignatureResolver) (bool, error) {
	hasSig := len(m.Signatures) > 0
	if policy == SigningForbidden && hasSig {
		return false, fmt.Errorf("%w: signing-forbidden policy and bundle is signed", bundle.ErrSignatureInvalid)
	}
	if policy == SigningRequired && !hasSig {
		return false, fmt.Errorf("%w: bundle %s has no signatures", bundle.ErrSignatureRequired, m.Name)
	}
	if !hasSig {
		return false, nil
	}
	if verifier == nil {
		if policy == SigningRequired {
			return false, fmt.Errorf("%w: no verifier configured", bundle.ErrSignatureRequired)
		}
		return false, nil
	}
	payload := m.SigningPayload()
	for _, sig := range m.Signatures {
		var sigBytes []byte
		if resolve != nil {
			b, err := resolve(sig.Locator)
			if err != nil {
				return false, fmt.Errorf("%w: resolve signature %s: %v", bundle.ErrSignatureInvalid, sig.Locator, err)
			}
			sigBytes = b
		}
		req := trust.VerifyRequest{
			Payload: payload,
			Signature: trust.SignatureRef{
				Kind:      sig.Kind,
				Locator:   sig.Locator,
				Algorithm: sig.Algorithm,
				KeyID:     sig.KeyID,
			},
			SignatureBytes: sigBytes,
			Anchors:        anchors,
		}
		res, err := verifier.Verify(ctx, req)
		if err != nil {
			return false, fmt.Errorf("%w: %v", bundle.ErrSignatureInvalid, err)
		}
		if !res.OK {
			if policy == SigningRequired {
				return false, fmt.Errorf("%w: %s (%s)", bundle.ErrSignatureRequired, res.Reason, sig.Locator)
			}
			// SigningOptional: a present-but-invalid signature still
			// fails — the operator opted-in by attaching the signature.
			return false, fmt.Errorf("%w: %s (%s)", bundle.ErrSignatureInvalid, res.Reason, sig.Locator)
		}
	}
	return true, nil
}
