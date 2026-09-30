package mlsidecar

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// EngineArtifactName is the manifest artifact name this package always
// uses for the single engine zip (design §6.2: "fetch the engine via
// core/bundle A-1 channels").
const EngineArtifactName = "kameas-ml-engine"

// ErrDigestMismatch is returned by VerifyFileSHA256 (and, wrapped, by
// VerifyEngineArtifact) when a downloaded or on-disk artifact's real
// bytes do not hash to the digest a manifest or install.json record
// declares. This is the F2 "planted proof" failure mode — a tampered
// artifact never executes because this check runs before any spawn.
var ErrDigestMismatch = errors.New("mlsidecar: artifact sha256 mismatch")

// EngineManifest builds the one-artifact bundle manifest describing the
// engine zip at artifactPath (bundle-relative, e.g. "kameas-ml.zip")
// with the expected sha256 digest ("sha256:<hex>", core/bundle/
// integrity's format) and an optional detached signature ref.
//
// Reusing core/bundle/manifest.Manifest here — rather than inventing an
// mlsidecar-local descriptor — is what lets VerifyEngineArtifact hand
// off to core/bundle/integrity.VerifyManifestSignatures verbatim: this
// is the "no second verifier" reuse the WP12 brief requires (design
// §3.2: "There is exactly one verifier, and it is not Python"; §3.7 R2:
// "Verification stays exclusively in Go/core/trust").
func EngineManifest(version, artifactPath, sha256Digest string, sig *manifest.SignatureRef) *manifest.Manifest {
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Name:          "kameas-ml",
		Version:       version,
		Artifacts: []manifest.ArtifactDescriptor{
			{Name: EngineArtifactName, Kind: "ml_sidecar_engine", Path: artifactPath, ContentHash: sha256Digest},
		},
	}
	if sig != nil {
		m.Signatures = []manifest.SignatureRef{*sig}
	}
	return m
}

// Verifier bundles the trust inputs VerifyEngineArtifact needs.
type Verifier struct {
	TrustVerifier trust.Verifier
	Anchors       []trust.Anchor
	// Policy defaults to the zero value (integrity.SigningOptional) if
	// left unset; DefaultEngineVerifier sets the engine-download default
	// of SigningRequired (design §6.2: "verify signature via core/trust
	// (SigningPolicy: Required)").
	Policy integrity.SigningPolicy
}

// DefaultEngineVerifier returns a Verifier configured with the
// engine-download default policy (SigningRequired). Tests that need to
// exercise SigningOptional/Forbidden construct a Verifier{} literal
// directly.
func DefaultEngineVerifier(tv trust.Verifier, anchors []trust.Anchor) Verifier {
	return Verifier{TrustVerifier: tv, Anchors: anchors, Policy: integrity.SigningRequired}
}

// VerifyEngineArtifact verifies m's signatures (via core/trust, reusing
// core/bundle/integrity.VerifyManifestSignatures — the SAME function
// core/rpc/views/bundle's Install path calls) AND that artifactPath's
// real on-disk bytes hash to the digest m declares. Both checks must
// pass before a caller may execute the artifact, swap `current`, or
// clear quarantine (design F4/§3.7 R3: "the installer, after A-1
// signature + sha256 verification, removes com.apple.quarantine").
func (v Verifier) VerifyEngineArtifact(ctx context.Context, m *manifest.Manifest, artifactPath string, resolve integrity.SignatureResolver) (verified bool, err error) {
	if len(m.Artifacts) != 1 || m.Artifacts[0].Name != EngineArtifactName {
		return false, fmt.Errorf("mlsidecar: engine manifest must declare exactly one %q artifact", EngineArtifactName)
	}
	verified, err = integrity.VerifyManifestSignatures(ctx, m, v.TrustVerifier, v.Anchors, v.Policy, resolve)
	if err != nil {
		return false, fmt.Errorf("mlsidecar: verify engine signature: %w", err)
	}
	if err := VerifyFileSHA256(artifactPath, m.Artifacts[0].ContentHash); err != nil {
		return false, err
	}
	return verified, nil
}

// HashFileSHA256 streams path and returns "sha256:<hex>" using
// core/bundle/integrity.HashSHA256 — the identical primitive core/update
// and core/bundle's CAS use. install.go calls this once, right after
// unpacking, to record the digest of the ACTUAL EXECUTABLE this client
// will later re-verify at adoption time (adopt.go's EvaluateAdoption) —
// deliberately distinct from the engine ZIP's own ContentHash (verified
// by VerifyEngineArtifact BEFORE unpacking): the zip is discarded after
// unpacking, so it is not what a later adoption attempt could re-hash.
func HashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("mlsidecar: open %s for hashing: %w", path, err)
	}
	defer f.Close()
	got, _, err := integrity.HashSHA256(f)
	if err != nil {
		return "", fmt.Errorf("mlsidecar: hash %s: %w", path, err)
	}
	return got, nil
}

// VerifyFileSHA256 streams path and compares its digest against want
// ("sha256:<hex>").
func VerifyFileSHA256(path, want string) error {
	got, err := HashFileSHA256(path)
	if err != nil {
		return err
	}
	if !integrity.CompareDigests(got, want) {
		return fmt.Errorf("%w: %s got %s want %s", ErrDigestMismatch, path, got, want)
	}
	return nil
}
