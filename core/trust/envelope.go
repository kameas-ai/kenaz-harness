package trust

// ValidateEnvelopeShape performs a minimal pre-check the verify pipeline
// runs at step 1 (plan §4.2): non-empty payload, signature, algorithm,
// key id. Real A2A v1 conformance lands in core/trust/envelope/ in WP09
// when a2a-go is consumed.
//
// Return value: nil when the envelope passes shape; [ErrInvalidEnvelope]
// otherwise. The caller (verify pipeline) maps this to
// [RejSignatureInvalid].
func ValidateEnvelopeShape(env Envelope) error {
	if len(env.Payload) == 0 {
		return ErrInvalidEnvelope
	}
	if len(env.Signature) == 0 {
		return ErrInvalidEnvelope
	}
	if env.Algorithm == "" {
		return ErrInvalidEnvelope
	}
	if env.KeyID == "" {
		return ErrInvalidEnvelope
	}
	return nil
}
