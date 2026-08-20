package trust

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EngineVerifier adapts a TrustEngine to the narrow Verifier surface
// consumed by core/bundle/integrity, core/context (pack distribution),
// and policy artifact loaders. It translates a VerifyRequest into the
// engine's Envelope/VerifyOptions shape and projects the structured
// VerificationResult down to the small (OK, Reason) tuple.
//
// The adapter is intentionally additive: the engine continues to be
// reachable directly for callers that need the full pipeline (audit
// emission, rotation, anchor resolution). DIRECTIVE_001 is preserved
// because the adapter lives next to the producer; consumers depend
// only on the public Verifier interface.
type EngineVerifier struct {
	engine TrustEngine
	// installAnchors, when true, causes Verify to install any anchors
	// supplied in the request before delegating. Useful for the bundle
	// resolver, which discovers anchors out-of-band and needs the engine
	// to recognise them on the same call. Disabled by default to keep
	// the adapter side-effect free for callers that manage anchors
	// separately.
	installAnchors bool
}

// EngineVerifierOption configures a NewEngineVerifier construction.
type EngineVerifierOption func(*EngineVerifier)

// WithRequestAnchorInstall makes the adapter install request-supplied
// anchors into the engine before each Verify call. The engine de-duplicates
// installs by AnchorID; repeat installs of the same anchor are no-ops.
func WithRequestAnchorInstall() EngineVerifierOption {
	return func(v *EngineVerifier) { v.installAnchors = true }
}

// NewEngineVerifier wraps engine in a Verifier. Returns an error if
// engine is nil.
func NewEngineVerifier(engine TrustEngine, opts ...EngineVerifierOption) (*EngineVerifier, error) {
	if engine == nil {
		return nil, errors.New("trust: NewEngineVerifier: engine is nil")
	}
	v := &EngineVerifier{engine: engine}
	for _, opt := range opts {
		opt(v)
	}
	return v, nil
}

// Verify implements Verifier by delegating to the underlying TrustEngine.
//
// Translation rules:
//   - req.Signature.KeyID populates Envelope.KeyID.
//   - req.Signature.Algorithm is parsed into the engine's Algorithm
//     constant. Unknown algorithms surface as a non-OK result with
//     Reason="algorithm_unsupported"; the engine's policy gate produces
//     the same outcome internally.
//   - req.Anchors are installed (if WithRequestAnchorInstall is set)
//     before the verify call. Install failures are reported as the
//     adapter's failure reason.
//   - The engine's RejectionCode is stringified via .String() and used
//     as Reason. Accepted results return OK=true and Reason="".
func (v *EngineVerifier) Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error) {
	if v == nil || v.engine == nil {
		return VerifyResult{}, errors.New("trust: EngineVerifier: nil engine")
	}

	if v.installAnchors {
		for _, a := range req.Anchors {
			if err := v.engine.InstallAnchor(ctx, a); err != nil {
				// Idempotent reinstalls are tolerated by every concrete
				// AnchorStore; a real failure (e.g. identity collision)
				// is surfaced to the caller as a non-OK result rather
				// than an error so the bundle layer can map it to its
				// own ErrSignatureInvalid envelope.
				return VerifyResult{
					OK:     false,
					Reason: fmt.Sprintf("anchor_install_failed: %v", err),
				}, nil
			}
		}
	}

	alg, err := parseAlgorithm(req.Signature.Algorithm)
	if err != nil {
		return VerifyResult{OK: false, Reason: err.Error()}, nil
	}

	env := Envelope{
		Payload:   req.Payload,
		Signature: req.SignatureBytes,
		Algorithm: alg,
		KeyID:     req.Signature.KeyID,
		// IssuedAt is intentionally the zero value, not time.Now(). A
		// detached ed25519 signature (the only algorithm with working
		// verification math today — see spec F-2) carries no issuance
		// timestamp on the wire: neither trust.SignatureRef nor
		// manifest.SignatureRef has a time field. Stamping time.Now()
		// here would make the clock-skew gate (verify.go step 6)
		// unconditionally pass while claiming to have checked
		// something real — a silent lie. ClockSkewPolicy.Within treats
		// a zero IssuedAt as "skip this gate" (policy.go), so leaving
		// it zero is the honest way to say "this signature kind is
		// timeless" rather than faking a check that never happens.
		IssuedAt: time.Time{},
	}

	res, vErr := v.engine.Verify(ctx, req.Payload, env, VerifyOptions{})
	if vErr != nil {
		var re *RejectionError
		if errors.As(vErr, &re) {
			return VerifyResult{OK: false, Reason: string(re.Code)}, nil
		}
		return VerifyResult{OK: false, Reason: vErr.Error()}, nil
	}

	switch res.Decision {
	case DecisionAccepted:
		return VerifyResult{OK: true}, nil
	case DecisionRejected:
		reason := string(res.RejectionCode)
		if reason == "" {
			reason = res.Detail
		}
		return VerifyResult{OK: false, Reason: reason}, nil
	default:
		return VerifyResult{
			OK:     false,
			Reason: fmt.Sprintf("unexpected decision: %v", res.Decision),
		}, nil
	}
}

// parseAlgorithm canonicalizes the wire-string algorithm id used by
// SignatureRef into the engine's Algorithm constant. Unknown ids return
// an error whose Error() string is suitable for the (OK, Reason) tuple.
func parseAlgorithm(id string) (Algorithm, error) {
	switch id {
	case "", string(AlgEd25519):
		return AlgEd25519, nil
	case string(AlgECDSAP256):
		return AlgECDSAP256, nil
	case string(AlgRSAPSSSHA256):
		return AlgRSAPSSSHA256, nil
	default:
		return AlgEd25519, fmt.Errorf("algorithm_unsupported: %q", id)
	}
}

// Compile-time assertion: *EngineVerifier satisfies Verifier.
var _ Verifier = (*EngineVerifier)(nil)
