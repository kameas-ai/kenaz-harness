package integrity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// fakeVerifier is a deterministic trust.Verifier stand-in — no engine,
// no anchors, just a scripted response so this package's tests exercise
// VerifyManifestSignatures's own logic (resolver plumbing, payload
// selection, policy branching) without depending on core/trust's
// pipeline (that's core/trust's own test suite's job).
type fakeVerifier struct {
	// wantSignatureBytes, if non-nil, asserts the bytes VerifyManifestSignatures
	// resolved and handed to Verify.
	wantSignatureBytes []byte
	ok                 bool
	reason             string
	err                error
}

func (f *fakeVerifier) Verify(_ context.Context, req trust.VerifyRequest) (trust.VerifyResult, error) {
	if f.wantSignatureBytes != nil && string(req.SignatureBytes) != string(f.wantSignatureBytes) {
		return trust.VerifyResult{}, fmt.Errorf("fakeVerifier: SignatureBytes=%q want %q", req.SignatureBytes, f.wantSignatureBytes)
	}
	if f.err != nil {
		return trust.VerifyResult{}, f.err
	}
	return trust.VerifyResult{OK: f.ok, Reason: f.reason}, nil
}

func TestFileResolver(t *testing.T) {
	dir := t.TempDir()
	sigPath := filepath.Join(dir, "kenaz.yaml.sig")
	want := []byte("raw-signature-bytes")
	if err := os.WriteFile(sigPath, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolve := integrity.FileResolver(dir)

	t.Run("relative locator resolves against bundleRoot", func(t *testing.T) {
		got, err := resolve("kenaz.yaml.sig")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("got=%q want=%q", got, want)
		}
	})

	t.Run("absolute locator is read as-is", func(t *testing.T) {
		got, err := resolve(sigPath)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("got=%q want=%q", got, want)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := resolve("does-not-exist.sig"); err == nil {
			t.Fatal("expected error for missing signature file")
		}
	})

	t.Run("empty locator errors", func(t *testing.T) {
		if _, err := resolve(""); err == nil {
			t.Fatal("expected error for empty locator")
		}
	})
}

func testManifest(withSig bool) *manifest.Manifest {
	m := &manifest.Manifest{
		SchemaVersion: 1,
		Name:          "test-bundle",
		Version:       "1.0.0",
		Artifacts: []manifest.ArtifactDescriptor{
			{Name: "a", Kind: "provider_profile", Path: "a.yaml", ContentHash: "sha256:aaaa"},
		},
	}
	if withSig {
		m.Signatures = []manifest.SignatureRef{
			{Kind: "ed25519_detached", Locator: "kenaz.yaml.sig", Algorithm: "ed25519", KeyID: "deadbeef"},
		}
	}
	return m
}

// TestVerifyManifestSignatures_NoSignature_OptionalPolicy_Skips covers
// the "nothing to verify" branch — verified must be false (there is
// nothing positive to report), err nil.
func TestVerifyManifestSignatures_NoSignature_OptionalPolicy_Skips(t *testing.T) {
	m := testManifest(false)
	verified, err := integrity.VerifyManifestSignatures(context.Background(), m, &fakeVerifier{ok: true}, nil, integrity.SigningOptional, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verified {
		t.Fatal("verified=true for an unsigned manifest — should be false (nothing was verified)")
	}
}

// TestVerifyManifestSignatures_SignedAndOK_ReturnsVerifiedTrue is
// UNIT-4's G-2 precondition: a real positive verification must set
// verified=true so the caller (bundle Install) can persist a tier of
// "signed" backed by an actual result, not by ref presence.
func TestVerifyManifestSignatures_SignedAndOK_ReturnsVerifiedTrue(t *testing.T) {
	dir := t.TempDir()
	sigBytes := []byte("sig-bytes")
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), sigBytes, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := testManifest(true)
	v := &fakeVerifier{ok: true, wantSignatureBytes: sigBytes}
	verified, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningOptional, integrity.FileResolver(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verified {
		t.Fatal("verified=false for a signature that passed verification")
	}
}

// TestVerifyManifestSignatures_UsesSigningPayloadNotCanonicalBytes pins
// UNIT-2 step 4: the payload handed to the verifier must be
// m.SigningPayload(), not m.CanonicalBytes() (which would fold in the
// very signature entry being verified).
func TestVerifyManifestSignatures_UsesSigningPayloadNotCanonicalBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := testManifest(true)

	var gotPayload []byte
	v := &recordingVerifier{result: trust.VerifyResult{OK: true}}
	_, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningOptional, integrity.FileResolver(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotPayload = v.lastReq.Payload
	if string(gotPayload) != string(m.SigningPayload()) {
		t.Fatalf("payload handed to verifier != SigningPayload()")
	}
	if string(gotPayload) == string(m.CanonicalBytes()) {
		t.Fatalf("payload handed to verifier == CanonicalBytes() — should exclude Signatures (SigningPayload)")
	}
}

type recordingVerifier struct {
	result  trust.VerifyResult
	lastReq trust.VerifyRequest
}

func (r *recordingVerifier) Verify(_ context.Context, req trust.VerifyRequest) (trust.VerifyResult, error) {
	r.lastReq = req
	return r.result, nil
}

// TestVerifyManifestSignatures_BadSignature_SigningRequired_ErrorsRequired
// and the SigningOptional sibling below pin the existing policy-branch
// behaviour (unchanged by UNIT-2, but the resolver/payload rewiring
// must not regress it).
func TestVerifyManifestSignatures_BadSignature_SigningRequired_ErrorsRequired(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := testManifest(true)
	v := &fakeVerifier{ok: false, reason: "signature_invalid"}
	verified, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningRequired, integrity.FileResolver(dir))
	if verified {
		t.Fatal("verified=true on a rejected signature")
	}
	if !errors.Is(err, bundle.ErrSignatureRequired) {
		t.Fatalf("err=%v, want ErrSignatureRequired", err)
	}
}

func TestVerifyManifestSignatures_BadSignature_SigningOptional_ErrorsInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := testManifest(true)
	v := &fakeVerifier{ok: false, reason: "signature_invalid"}
	verified, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningOptional, integrity.FileResolver(dir))
	if verified {
		t.Fatal("verified=true on a rejected signature")
	}
	if !errors.Is(err, bundle.ErrSignatureInvalid) {
		t.Fatalf("err=%v, want ErrSignatureInvalid", err)
	}
}

// TestVerifyManifestSignatures_NilResolver_EmptyBytes confirms nil
// resolver is tolerated: the request goes through with empty
// SignatureBytes (matching pre-UNIT-2 behaviour for callers that
// haven't adopted a resolver yet).
func TestVerifyManifestSignatures_NilResolver_EmptyBytes(t *testing.T) {
	m := testManifest(true)
	v := &fakeVerifier{ok: false, reason: "signature_invalid", wantSignatureBytes: []byte{}}
	_, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningOptional, nil)
	if err == nil {
		t.Fatal("expected error (fake verifier rejects)")
	}
}

// TestVerifyManifestSignatures_ResolverError_WrapsInvalid confirms a
// resolver failure (e.g. missing .sig file) surfaces as
// ErrSignatureInvalid rather than propagating a bare os.PathError.
func TestVerifyManifestSignatures_ResolverError_WrapsInvalid(t *testing.T) {
	m := testManifest(true)
	resolve := integrity.FileResolver(t.TempDir()) // .sig file does not exist
	_, err := integrity.VerifyManifestSignatures(context.Background(), m, &fakeVerifier{ok: true}, nil, integrity.SigningOptional, resolve)
	if !errors.Is(err, bundle.ErrSignatureInvalid) {
		t.Fatalf("err=%v, want ErrSignatureInvalid", err)
	}
}

// end-to-end sanity: a real ed25519 signature over SigningPayload,
// verified by a fake verifier that decodes it itself, proves the
// resolver + payload plumbing produces bytes an actual verifier could
// use (core/trust's own tests own the "does the engine accept it"
// question — this one only proves this package hands over the right
// bytes).
func TestVerifyManifestSignatures_RealEd25519RoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := testManifest(true)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	sig := ed25519.Sign(priv, m.SigningPayload())
	if err := os.WriteFile(filepath.Join(dir, "kenaz.yaml.sig"), sig, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v := &ed25519Verifier{pub: pub}
	verified, err := integrity.VerifyManifestSignatures(context.Background(), m, v, nil, integrity.SigningOptional, integrity.FileResolver(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verified {
		t.Fatal("expected verified=true for a correctly signed manifest")
	}
}

type ed25519Verifier struct{ pub ed25519.PublicKey }

func (v *ed25519Verifier) Verify(_ context.Context, req trust.VerifyRequest) (trust.VerifyResult, error) {
	if ed25519.Verify(v.pub, req.Payload, req.SignatureBytes) {
		return trust.VerifyResult{OK: true}, nil
	}
	return trust.VerifyResult{OK: false, Reason: "signature_invalid"}, nil
}
