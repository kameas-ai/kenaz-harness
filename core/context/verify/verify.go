// Package verify is the provenance-verification facade (WP05). It calls
// the a2a-signed-cards-trust verification API exactly once per pack, maps
// the trust taxonomy 1:1 (FR-017 codes from mission KQ18P9), and produces
// [ProvenanceRecord] values consumed downstream by the audit emitter and
// the fail-closed policy. Per C-003 we never reimplement signing or
// trust anchors.
//
// The trust mission's public verifier interface is consumed via the
// [TrustVerifier] adapter declared in this package. Until trust lands,
// callers register an implementation through their own DI; tests use the
// in-package fakes in verify_test.go.
package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// TrustErrorCode mirrors the typed error taxonomy declared by mission
// a2a-signed-cards-trust FR-017. We surface the codes 1:1 — no new codes
// are introduced in core/context/.
type TrustErrorCode string

const (
	TrustSignatureInvalid     TrustErrorCode = "signature_invalid"
	TrustAlgorithmNotPermitted TrustErrorCode = "algorithm_not_permitted"
	TrustAnchorMissing        TrustErrorCode = "anchor_missing"
	TrustAnchorRemoved        TrustErrorCode = "anchor_removed"
	TrustKeyRevoked           TrustErrorCode = "key_revoked"
	TrustKeyExpired           TrustErrorCode = "key_expired"
	TrustIdentityCollision    TrustErrorCode = "identity_collision"
	TrustClockSkew            TrustErrorCode = "clock_skew"
	TrustPrecedenceAmbiguity  TrustErrorCode = "precedence_ambiguity"
)

// CacheState captures whether the verifying anchor was retrieved from a
// fresh fetch or a local cache. Carried into the audit record per plan
// §4.2.
type CacheState string

const (
	CacheUnknown CacheState = "unknown"
	CacheFresh   CacheState = "fresh"
	CacheWarm    CacheState = "warm"
	CacheStale   CacheState = "stale"
)

// GraceState carries whether the verification consumed an
// intermediate-key rotation grace period (Risk R8). The trust mission
// (KQ18P9 FR-013) handles the policy; we only surface the state.
type GraceState string

const (
	GraceNone     GraceState = "none"
	GraceInGrace  GraceState = "in_grace"
	GraceExpired  GraceState = "expired"
)

// VerifyPolicy exposes per-call trust-layer knobs (algorithm allowlist,
// clock-skew tolerance, etc.). The trust mission owns its full shape;
// this package passes it through opaquely.
type VerifyPolicy struct {
	AllowedAlgorithms []string
	ClockSkewTolerance time.Duration
	RequireFreshAnchor bool
}

// VerifyInput is what the trust verifier receives.
type VerifyInput struct {
	Payload   []byte         // the signed bytes (e.g. canonical pack manifest)
	Envelope  []byte         // detached signature envelope from signatures/pack.sig
	AnchorID  string         // claimed anchor identity from the manifest
	Algorithm string         // claimed signing algorithm
	Now       time.Time      // injected clock for deterministic tests
	Policy    VerifyPolicy
}

// VerifyOutput is what the trust verifier returns on success. On failure
// it returns a [ProvenanceError] with a non-empty Code.
type VerifyOutput struct {
	AnchorID    string
	Algorithm   string
	ContentHash string
	CacheState  CacheState
	GraceState  GraceState
	VerifiedAt  time.Time
}

// TrustVerifier is the trust-layer surface the context package consumes.
// The real implementation lives in core/trust/ once that mission lands;
// until then this is the only contract surface that crosses the boundary.
type TrustVerifier interface {
	Verify(ctx context.Context, in VerifyInput) (VerifyOutput, error)
}

// ProvenanceError is the typed error this package exposes when trust
// verification fails. Code is one of [TrustErrorCode] — surfaced 1:1
// from the trust mission. No new codes are introduced here.
type ProvenanceError struct {
	Code    TrustErrorCode
	Pack    pack.PackRef
	Message string
	Cause   error
}

// Error implements error.
func (e *ProvenanceError) Error() string {
	return fmt.Sprintf("provenance: %s [%s]: %s", e.Code, e.Pack, e.Message)
}

// Unwrap exposes the cause for errors.As.
func (e *ProvenanceError) Unwrap() error { return e.Cause }

// Is matches by code only — see pack.Error for the same convention.
func (e *ProvenanceError) Is(target error) bool {
	var t *ProvenanceError
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == "" || t.Code == e.Code
}

// ProvenanceRecord is the verification artefact attached to each
// [ResolutionSnapshot] layer (plan §3, §4.2).
type ProvenanceRecord struct {
	Pack        pack.PackRef `json:"pack"`
	AnchorID    string       `json:"anchor_id"`
	Algorithm   string       `json:"algorithm"`
	ContentHash string       `json:"content_hash"`
	CacheState  CacheState   `json:"cache_state"`
	GraceState  GraceState   `json:"grace_state"`
	VerifiedAt  time.Time    `json:"verified_at"`
}

// PackVerifier is the per-pack verification facade. It reads the
// detached signature envelope from the pack's signatures/pack.sig,
// builds a [VerifyInput], and calls the trust verifier exactly once.
type PackVerifier struct {
	Trust  TrustVerifier
	Policy VerifyPolicy
	// PackRoot is the directory where signature files live for each pack.
	// When provided, [VerifyPack] will read signatures/pack.sig from there.
	// When empty, callers must supply the envelope via VerifyEnvelope.
	PackRoot string
	// Now is an injected clock (defaults to time.Now).
	Now func() time.Time
}

// VerifyPack verifies one pack by loading its detached envelope from
// disk under PackRoot. Returns a [ProvenanceRecord] on success.
func (v *PackVerifier) VerifyPack(ctx context.Context, p *pack.ContextPack) (ProvenanceRecord, error) {
	if v == nil || v.Trust == nil {
		return ProvenanceRecord{}, errors.New("verify: no trust verifier configured")
	}
	if p == nil {
		return ProvenanceRecord{}, errors.New("verify: nil pack")
	}
	if p.Signature.Empty() {
		return ProvenanceRecord{}, &ProvenanceError{
			Code:    TrustAnchorMissing,
			Pack:    p.Ref,
			Message: "pack carries no signature reference",
		}
	}

	root := v.PackRoot
	if root == "" {
		return ProvenanceRecord{}, errors.New("verify: PackRoot not configured")
	}
	envPath := filepath.Join(root, p.Signature.Path)
	envelope, err := os.ReadFile(envPath)
	if err != nil {
		return ProvenanceRecord{}, &ProvenanceError{
			Code:    TrustSignatureInvalid,
			Pack:    p.Ref,
			Message: "could not read detached signature envelope",
			Cause:   err,
		}
	}
	return v.VerifyEnvelope(ctx, p, envelope)
}

// VerifyEnvelope is the lower-level form: caller provides the envelope
// bytes directly (useful for callers loading from a content-addressable
// cache instead of disk).
func (v *PackVerifier) VerifyEnvelope(ctx context.Context, p *pack.ContextPack, envelope []byte) (ProvenanceRecord, error) {
	if v == nil || v.Trust == nil {
		return ProvenanceRecord{}, errors.New("verify: no trust verifier configured")
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	in := VerifyInput{
		Payload:   []byte(p.Ref.ContentHash), // sign over the Merkle aggregate
		Envelope:  envelope,
		AnchorID:  p.Signature.AnchorID,
		Algorithm: p.Signature.Algorithm,
		Now:       now(),
		Policy:    v.Policy,
	}
	out, err := v.Trust.Verify(ctx, in)
	if err != nil {
		// Map the error 1:1. If the trust mission already returns a
		// *ProvenanceError, surface it unchanged. Otherwise wrap with
		// signature_invalid as a defensive default.
		var pe *ProvenanceError
		if errors.As(err, &pe) {
			pe.Pack = p.Ref
			return ProvenanceRecord{}, pe
		}
		return ProvenanceRecord{}, &ProvenanceError{
			Code:    TrustSignatureInvalid,
			Pack:    p.Ref,
			Message: "trust verifier rejected pack",
			Cause:   err,
		}
	}
	return ProvenanceRecord{
		Pack:        p.Ref,
		AnchorID:    out.AnchorID,
		Algorithm:   out.Algorithm,
		ContentHash: out.ContentHash,
		CacheState:  out.CacheState,
		GraceState:  out.GraceState,
		VerifiedAt:  out.VerifiedAt,
	}, nil
}
