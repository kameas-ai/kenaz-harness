package mlsidecar

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// fakeTrustVerifier is a deterministic trust.Verifier stand-in, the same
// shape core/bundle/integrity's own test suite uses (signature_test.go's
// fakeVerifier) — this package reuses core/trust's contract without
// depending on a real signing backend for its own tests.
type fakeTrustVerifier struct {
	ok  bool
	err error
	// wantBytes, if non-nil, asserts the SignatureBytes this verifier
	// was actually handed — proof that install.go's channel-based
	// signature resolver fetched the real sibling artifact rather than
	// silently verifying against an empty payload.
	wantBytes []byte
}

func (f fakeTrustVerifier) Verify(_ context.Context, req trust.VerifyRequest) (trust.VerifyResult, error) {
	if f.err != nil {
		return trust.VerifyResult{}, f.err
	}
	if f.wantBytes != nil && string(req.SignatureBytes) != string(f.wantBytes) {
		return trust.VerifyResult{}, errBadSignatureBytes
	}
	if f.ok {
		return trust.VerifyResult{OK: true}, nil
	}
	return trust.VerifyResult{OK: false, Reason: "signature_invalid"}, nil
}

var errBadSignatureBytes = errors.New("mlsidecar_test: unexpected signature bytes handed to verifier")

func writeArtifact(t *testing.T, dir, name string, content []byte) (path, sha string) {
	t.Helper()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer f.Close()
	sha, _, err = integrity.HashSHA256(f)
	if err != nil {
		t.Fatalf("hash artifact: %v", err)
	}
	return path, sha
}

func fileResolver(dir string) integrity.SignatureResolver {
	return integrity.FileResolver(dir)
}

// TestVerifyEngineArtifact_HappyPath proves the "reuse, no second
// verifier" claim end to end: a real ed25519 signature over
// SigningPayload, verified by an ed25519-checking trust.Verifier (the
// EXACT pattern core/bundle/integrity/signature_test.go's own
// TestVerifyManifestSignatures_RealEd25519RoundTrip uses), plus a
// correct sha256 digest.
func TestVerifyEngineArtifact_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path, sha := writeArtifact(t, dir, "engine.zip", []byte("the real engine bytes"))

	m := EngineManifest("1.0.0", "engine.zip", sha, &manifest.SignatureRef{
		Kind: "ed25519_detached", Locator: "engine.zip.sig", Algorithm: "ed25519", KeyID: "k1",
	})
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	sig := ed25519.Sign(priv, m.SigningPayload())
	if err := os.WriteFile(filepath.Join(dir, "engine.zip.sig"), sig, 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	v := Verifier{TrustVerifier: ed25519VerifierFor(pub), Policy: integrity.SigningRequired}
	verified, err := v.VerifyEngineArtifact(context.Background(), m, path, fileResolver(dir))
	if err != nil {
		t.Fatalf("VerifyEngineArtifact: %v", err)
	}
	if !verified {
		t.Fatal("expected verified=true for a correctly signed, correctly hashed artifact")
	}
}

type ed25519Verifier struct{ pub ed25519.PublicKey }

func ed25519VerifierFor(pub ed25519.PublicKey) trust.Verifier { return ed25519Verifier{pub: pub} }

func (v ed25519Verifier) Verify(_ context.Context, req trust.VerifyRequest) (trust.VerifyResult, error) {
	if ed25519.Verify(v.pub, req.Payload, req.SignatureBytes) {
		return trust.VerifyResult{OK: true}, nil
	}
	return trust.VerifyResult{OK: false, Reason: "signature_invalid"}, nil
}

// TestVerifyEngineArtifact_TamperedBytes_NeverPasses is the F2-adjacent
// planted proof at the verification layer: a correctly signed manifest
// whose declared digest does not match the artifact's REAL on-disk bytes
// (simulating corruption/tampering after signing) must fail with
// ErrDigestMismatch — regardless of the signature being perfectly valid.
// This is the check install.go relies on to guarantee a tampered
// artifact is never unpacked, never quarantine-cleared, and never run.
func TestVerifyEngineArtifact_TamperedBytes_NeverPasses(t *testing.T) {
	dir := t.TempDir()
	path, correctSha := writeArtifact(t, dir, "engine.zip", []byte("original bytes"))
	// Tamper with the file AFTER computing the digest the manifest will
	// declare — the manifest still says "correctSha", but the bytes on
	// disk no longer hash to it.
	if err := os.WriteFile(path, []byte("TAMPERED BYTES, DIFFERENT LENGTH TOO"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	m := EngineManifest("1.0.0", "engine.zip", correctSha, &manifest.SignatureRef{
		Kind: "ed25519_detached", Locator: "engine.zip.sig",
	})
	if err := os.WriteFile(filepath.Join(dir, "engine.zip.sig"), []byte("sig"), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	v := Verifier{TrustVerifier: fakeTrustVerifier{ok: true}, Policy: integrity.SigningRequired}
	verified, err := v.VerifyEngineArtifact(context.Background(), m, path, fileResolver(dir))
	if verified {
		t.Fatal("verified=true for a tampered artifact — must never happen")
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err=%v, want ErrDigestMismatch", err)
	}
}

// TestVerifyEngineArtifact_SigningRequired_RefusesUnsigned pins the
// engine-download default policy (design §6.2: "SigningPolicy:
// Required"): a manifest with no signature at all is refused outright,
// never silently treated as "nothing to verify".
func TestVerifyEngineArtifact_SigningRequired_RefusesUnsigned(t *testing.T) {
	dir := t.TempDir()
	path, sha := writeArtifact(t, dir, "engine.zip", []byte("bytes"))
	m := EngineManifest("1.0.0", "engine.zip", sha, nil)
	v := DefaultEngineVerifier(fakeTrustVerifier{ok: true}, nil)
	verified, err := v.VerifyEngineArtifact(context.Background(), m, path, fileResolver(dir))
	if verified || err == nil {
		t.Fatalf("expected refusal for an unsigned manifest under SigningRequired, got verified=%v err=%v", verified, err)
	}
}

// TestVerifyFileSHA256 covers the standalone digest helper directly.
func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path, sha := writeArtifact(t, dir, "f.bin", []byte("hello world"))
	if err := VerifyFileSHA256(path, sha); err != nil {
		t.Fatalf("VerifyFileSHA256 (correct digest): %v", err)
	}
	if err := VerifyFileSHA256(path, "sha256:0000000000000000000000000000000000000000000000000000000000000000"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err=%v, want ErrDigestMismatch", err)
	}
}
