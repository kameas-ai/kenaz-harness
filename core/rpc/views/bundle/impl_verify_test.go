package bundle

// UNIT-4 (bundle-download-and-verify-01PMZ909): Install calls
// VerifyManifestSignatures against the real core/trust engine and
// derives the persisted trust tier from a real, positive result — not
// from signature_ref's mere presence. These tests exercise the whole
// stack (a real *trust.EngineVerifier over a real *trust.engine, real
// ed25519 signing, real CAS-shaped lockfile round trip) rather than a
// fake verifier, per AC-006's own falsifiability note: "fails if the
// fixture passes a nil verifier."

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	corebundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/lockfile"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// signedFixture builds a bundle directory containing kenaz.yaml (with a
// signatures: block pointing at kenaz.yaml.sig), the matching ed25519
// key pair, AND the real bytes of the one declared artifact (UNIT-5:
// Install now actually fetches and hash-verifies every artifact, so a
// content_hash with no matching bytes on disk refuses the install
// before signature verification is even relevant to the caller).
// Returns the directory, the parsed manifest, and the key pair so the
// caller can install a matching (or non-matching) anchor.
func signedFixture(t *testing.T, name string) (dir string, pub ed25519.PublicKey, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "policy"), 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	artifactBytes := []byte(name + "-policy-bytes")
	digest := sha256Hex(artifactBytes)
	if err := os.WriteFile(filepath.Join(dir, "policy", "policy.toml"), artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: ` + name + `
version: 1.0.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
signatures:
  - kind: ed25519_detached
    locator: kenaz.yaml.sig
    algorithm: ed25519
    key_id: ` + trust.ComputeFingerprint(pub) + `
`
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m, err := manifest.Parse([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("Parse manifest: %v", err)
	}
	sig := ed25519.Sign(priv, m.SigningPayload())
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), sig, 0o644); err != nil {
		t.Fatalf("write signature: %v", err)
	}
	return dir, pub, priv
}

func testAnchorForKey(anchorID string, pub ed25519.PublicKey) trust.Anchor {
	return trust.Anchor{
		AnchorID:  anchorID,
		Kind:      trust.AnchorRawPublicKey,
		Algorithm: trust.AlgEd25519,
		PublicKey: trust.PublicKey{
			Algorithm:   trust.AlgEd25519,
			Bytes:       append([]byte(nil), pub...),
			Fingerprint: trust.ComputeFingerprint(pub),
		},
	}
}

// TestInstall_AC001Analog_SignedAndAnchored_VerifiesAndTierIsSigned
// confirms the whole stack: a real engine + real anchor + real
// signature produces Verified=true and a "signed" tier.
func TestInstall_AC001Analog_SignedAndAnchored_VerifiesAndTierIsSigned(t *testing.T) {
	dir, pub, _ := signedFixture(t, "alpha")
	engine, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	anchor := testAnchorForKey("anchor-1", pub)
	if err := engine.InstallAnchor(context.Background(), anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	verifier, err := trust.NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}

	rw := &memReadWriter{}
	api := NewAPI(
		WithReader(rw), WithWriter(rw),
		WithVerifier(verifier),
		WithAnchorsFunc(func(ctx context.Context) ([]trust.Anchor, error) {
			return engine.ListAnchors(ctx)
		}),
		WithCAS(testCAS(t)),
	)

	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: dir})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.Tier != "signed" {
		t.Errorf("Tier=%q, want signed", got.Tier)
	}
}

// TestInstall_AC006_BadSignature_SigningOptional_RefusesWithNoLockfileRow
// is AC-006: real CAS-shaped lockfile round trip, real engine, real
// anchor set, SigningOptional policy, a BAD signature (payload tampered
// after signing) — Install must error wrapping bundle.ErrSignatureInvalid,
// and the lockfile must show NO row for the bundle (rollback on
// refusal).
func TestInstall_AC006_BadSignature_SigningOptional_RefusesWithNoLockfileRow(t *testing.T) {
	dir, pub, _ := signedFixture(t, "tampered")
	// Corrupt the manifest AFTER signing by editing an artifact hash —
	// this changes SigningPayload() without re-signing, exactly the
	// "present but invalid" case §71-74 of signature.go documents.
	manifestPath := filepath.Join(dir, "kenaz.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	tampered := []byte(string(raw) + "\nmetadata:\n  tampered: \"true\"\n")
	if err := os.WriteFile(manifestPath, tampered, 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	engine, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	anchor := testAnchorForKey("anchor-1", pub)
	if err := engine.InstallAnchor(context.Background(), anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	verifier, err := trust.NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}

	rw := &memReadWriter{}
	casDir := t.TempDir()
	realCAS, cerr := cache.New(casDir)
	if cerr != nil {
		t.Fatalf("cache.New: %v", cerr)
	}
	api := NewAPI(
		WithReader(rw), WithWriter(rw),
		WithVerifier(verifier),
		WithAnchorsFunc(func(ctx context.Context) ([]trust.Anchor, error) {
			return engine.ListAnchors(ctx)
		}),
		WithSigningPolicyFunc(func() integrity.SigningPolicy { return integrity.SigningOptional }),
		WithCAS(CASFromCache(realCAS)),
	)

	_, err = api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: dir})
	if err == nil {
		t.Fatal("expected Install to refuse a tampered manifest")
	}
	if !errors.Is(err, corebundle.ErrSignatureInvalid) {
		t.Fatalf("err=%v, want wrapping bundle.ErrSignatureInvalid", err)
	}

	// No lockfile row: List must return zero bundles.
	list, lerr := api.List(context.Background())
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("expected no lockfile row after a refused install, got %+v", list)
	}
	// AC-006: no CAS residue either — the refusal happens before the
	// artifact-fetch loop even starts (signatures are verified first).
	if realCAS.Has(sha256Hex([]byte("tampered-policy-bytes"))) {
		t.Fatalf("refused install must not leave a CAS entry")
	}
}

// TestInstall_AC006_Falsifiable_NilVerifierPassesVacuously documents
// AC-006's own falsifiability note by executing it: a nil verifier
// under SigningOptional makes VerifyManifestSignatures return nil
// early (signature.go's own documented short-circuit) — Install
// SUCCEEDS on the very same tampered manifest that the real-verifier
// test above refuses. This is not a bug in this test file; it is proof
// that AC-006 above is exercising the real verifier and not silently
// passing for the wrong reason.
func TestInstall_AC006_Falsifiable_NilVerifierPassesVacuously(t *testing.T) {
	dir, _, _ := signedFixture(t, "vacuous")
	manifestPath := filepath.Join(dir, "kenaz.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	tampered := []byte(string(raw) + "\nmetadata:\n  tampered: \"true\"\n")
	if err := os.WriteFile(manifestPath, tampered, 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	rw := &memReadWriter{}
	// No WithVerifier — the pre-UNIT-4 / no-trust-wired configuration.
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(testCAS(t)))
	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: dir})
	if err != nil {
		t.Fatalf("expected a nil verifier to skip verification and succeed, got error: %v", err)
	}
	if got.Tier == "signed" {
		t.Fatalf("a nil-verifier install must never claim \"signed\" — got tier %q", got.Tier)
	}
}

// TestInstall_AC007_UnsignedBundle_DefaultPolicy_InstallsUnsigned is
// AC-007: an unsigned bundle installs under the default policy
// (SigningOptional) and its tier is NOT "signed".
//
// Mutation: flip the fallback default in Install from
// integrity.SigningOptional to integrity.SigningRequired — this test
// must go red (Install would refuse the unsigned bundle outright).
func TestInstall_AC007_UnsignedBundle_DefaultPolicy_Installs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "policy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	artifactBytes := []byte("unsigned-policy-bytes")
	digest := sha256Hex(artifactBytes)
	if err := os.WriteFile(filepath.Join(tmp, "policy", "policy.toml"), artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: unsigned-bundle
version: 1.0.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
`
	if err := os.WriteFile(filepath.Join(tmp, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	// No WithVerifier / WithSigningPolicyFunc — the DEFAULT path.
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(testCAS(t)))
	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: tmp})
	if err != nil {
		t.Fatalf("Install of an unsigned bundle under the default policy should succeed: %v", err)
	}
	if got.Tier == "signed" {
		t.Fatalf("unsigned bundle rendered as tier=%q, want not-signed", got.Tier)
	}
}

// TestInstall_AC009Analog_UnknownAnchor_AnchorMissing_RefusesUnderRequired
// exercises the SigningRequired policy path end to end: a correctly
// signed manifest but NO matching anchor installed anywhere refuses
// with ErrSignatureRequired (not a silent pass).
func TestInstall_UnknownAnchor_SigningRequired_Refuses(t *testing.T) {
	dir, _, _ := signedFixture(t, "no-anchor")
	engine, err := trust.NewEngine(trust.Config{}) // no anchor installed
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	verifier, err := trust.NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}
	rw := &memReadWriter{}
	api := NewAPI(
		WithReader(rw), WithWriter(rw),
		WithVerifier(verifier),
		WithAnchorsFunc(func(ctx context.Context) ([]trust.Anchor, error) { return nil, nil }),
		WithSigningPolicyFunc(func() integrity.SigningPolicy { return integrity.SigningRequired }),
		WithCAS(testCAS(t)),
	)
	_, err = api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: dir})
	if !errors.Is(err, corebundle.ErrSignatureRequired) {
		t.Fatalf("err=%v, want ErrSignatureRequired", err)
	}
	list, lerr := api.List(context.Background())
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("expected no lockfile row after refusal, got %+v", list)
	}
}

// TestLockedToBundle_G2_TierRequiresVerified is UNIT-4's G-2 gate: a
// table test over lockedToBundle asserting NO input combination yields
// tier "signed" without Verified==true. Package-internal (this file is
// `package bundle`, not `bundle_test`) so it can call the unexported
// lockedToBundle directly.
func TestLockedToBundle_G2_TierRequiresVerified(t *testing.T) {
	cases := []struct {
		name       string
		lb         lockfile.LockedBundle
		wantSigned bool
	}{
		{"no signature, not verified", lockfile.LockedBundle{Name: "a"}, false},
		{"signature_ref present, NOT verified (legacy row shape)", lockfile.LockedBundle{Name: "b", SignatureRef: "kenaz.yaml.sig"}, false},
		{"verified true, no signature_ref (defensive: still not signed without a ref)", lockfile.LockedBundle{Name: "c", Verified: true}, true},
		{"signature_ref present AND verified true", lockfile.LockedBundle{Name: "d", SignatureRef: "kenaz.yaml.sig", Verified: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lockedToBundle(tc.lb, nil, false)
			isSigned := got.Tier == "signed"
			if isSigned != tc.wantSigned {
				t.Errorf("tier=%q (signed=%v), want signed=%v", got.Tier, isSigned, tc.wantSigned)
			}
			if isSigned && !tc.lb.Verified {
				t.Errorf("G-2 VIOLATION: tier=\"signed\" but Verified=false")
			}
		})
	}
}
