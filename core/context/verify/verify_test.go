package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	pack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// fakeTrust is the canonical in-test stand-in for the real trust mission's
// verifier. The contract surface is single-method [TrustVerifier], so the
// fake is a function-shaped struct.
type fakeTrust struct {
	out VerifyOutput
	err error
	// recordedInput captures the last call so tests can assert on it.
	recordedInput *VerifyInput
}

func (f *fakeTrust) Verify(_ context.Context, in VerifyInput) (VerifyOutput, error) {
	cp := in
	f.recordedInput = &cp
	return f.out, f.err
}

func okPack() *pack.ContextPack {
	return &pack.ContextPack{
		Ref: pack.PackRef{
			Name: "acme-org-context", Version: "1.0.0",
			Layer:       pack.LayerOrg,
			ContentHash: "sha256:packhash",
		},
		Signature: pack.SignatureRef{
			Path:      "signatures/pack.sig",
			Algorithm: "sigstore-bundle",
			AnchorID:  "trust://acme/anchors/root",
		},
	}
}

func writeEnvelope(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "signatures"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "signatures", "pack.sig"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestVerifyPack_HappyPath(t *testing.T) {
	root := t.TempDir()
	writeEnvelope(t, root, "envelope-bytes")

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	ft := &fakeTrust{out: VerifyOutput{
		AnchorID:    "trust://acme/anchors/root",
		Algorithm:   "sigstore-bundle",
		ContentHash: "sha256:packhash",
		CacheState:  CacheFresh,
		GraceState:  GraceNone,
		VerifiedAt:  now,
	}}
	v := &PackVerifier{Trust: ft, PackRoot: root, Now: func() time.Time { return now }}

	rec, err := v.VerifyPack(context.Background(), okPack())
	if err != nil {
		t.Fatalf("VerifyPack: %v", err)
	}
	if rec.AnchorID != "trust://acme/anchors/root" {
		t.Errorf("AnchorID = %q", rec.AnchorID)
	}
	if rec.CacheState != CacheFresh {
		t.Errorf("CacheState = %q", rec.CacheState)
	}
	// Confirm trust was actually invoked with the right inputs.
	if ft.recordedInput == nil {
		t.Fatalf("trust verifier not invoked")
	}
	if string(ft.recordedInput.Envelope) != "envelope-bytes" {
		t.Errorf("envelope mismatch: %q", ft.recordedInput.Envelope)
	}
	if string(ft.recordedInput.Payload) != "sha256:packhash" {
		t.Errorf("payload should be the pack content hash; got %q", ft.recordedInput.Payload)
	}
}

func TestVerifyPack_TamperedEnvelopeMapsTrustCode(t *testing.T) {
	root := t.TempDir()
	writeEnvelope(t, root, "tampered")

	ft := &fakeTrust{err: &ProvenanceError{
		Code:    TrustSignatureInvalid,
		Message: "signature does not validate against payload",
	}}
	v := &PackVerifier{Trust: ft, PackRoot: root}
	_, err := v.VerifyPack(context.Background(), okPack())
	var pe *ProvenanceError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProvenanceError, got %v", err)
	}
	if pe.Code != TrustSignatureInvalid {
		t.Errorf("Code = %q, want %q", pe.Code, TrustSignatureInvalid)
	}
}

func TestVerifyPack_AllTrustCodesPropagateUnchanged(t *testing.T) {
	cases := []TrustErrorCode{
		TrustSignatureInvalid,
		TrustAlgorithmNotPermitted,
		TrustAnchorMissing,
		TrustAnchorRemoved,
		TrustKeyRevoked,
		TrustKeyExpired,
		TrustIdentityCollision,
		TrustClockSkew,
		TrustPrecedenceAmbiguity,
	}
	for _, code := range cases {
		t.Run(string(code), func(t *testing.T) {
			root := t.TempDir()
			writeEnvelope(t, root, "x")
			ft := &fakeTrust{err: &ProvenanceError{Code: code, Message: "x"}}
			v := &PackVerifier{Trust: ft, PackRoot: root}
			_, err := v.VerifyPack(context.Background(), okPack())
			var pe *ProvenanceError
			if !errors.As(err, &pe) || pe.Code != code {
				t.Fatalf("code did not propagate: got %v want %s", err, code)
			}
		})
	}
}

func TestVerifyPack_NoSignatureRefRejected(t *testing.T) {
	v := &PackVerifier{Trust: &fakeTrust{}, PackRoot: t.TempDir()}
	p := okPack()
	p.Signature = pack.SignatureRef{}
	_, err := v.VerifyPack(context.Background(), p)
	var pe *ProvenanceError
	if !errors.As(err, &pe) || pe.Code != TrustAnchorMissing {
		t.Errorf("expected TrustAnchorMissing, got %v", err)
	}
}

func TestVerifyPack_GraceStateSurfaced(t *testing.T) {
	root := t.TempDir()
	writeEnvelope(t, root, "x")
	ft := &fakeTrust{out: VerifyOutput{
		AnchorID:   "anchor",
		GraceState: GraceInGrace,
		CacheState: CacheWarm,
		VerifiedAt: time.Now(),
	}}
	v := &PackVerifier{Trust: ft, PackRoot: root}
	rec, err := v.VerifyPack(context.Background(), okPack())
	if err != nil {
		t.Fatalf("VerifyPack: %v", err)
	}
	if rec.GraceState != GraceInGrace {
		t.Errorf("GraceState = %q, want in_grace", rec.GraceState)
	}
}

func TestVerifyPack_MissingEnvelopeFile(t *testing.T) {
	root := t.TempDir()
	v := &PackVerifier{Trust: &fakeTrust{}, PackRoot: root}
	_, err := v.VerifyPack(context.Background(), okPack())
	var pe *ProvenanceError
	if !errors.As(err, &pe) || pe.Code != TrustSignatureInvalid {
		t.Fatalf("expected signature_invalid, got %v", err)
	}
}

func TestVerifyEnvelope_UnknownErrorMappedDefensively(t *testing.T) {
	ft := &fakeTrust{err: errors.New("opaque trust failure")}
	v := &PackVerifier{Trust: ft}
	_, err := v.VerifyEnvelope(context.Background(), okPack(), []byte("env"))
	var pe *ProvenanceError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProvenanceError; got %v", err)
	}
	if pe.Code != TrustSignatureInvalid {
		t.Errorf("default mapping = %q, want signature_invalid", pe.Code)
	}
}
