package trust

import (
	"time"
)

// AlgorithmPolicy implements FR-004 — the operator-configurable allow-list
// of acceptable signing algorithms. Default per Open Question 2 is
// Ed25519-only at v1.0; ECDSA-P256 and RSA-PSS are interface-conformant
// slots filled by operator opt-in in v1.x.
type AlgorithmPolicy struct {
	// Allowed contains every Algorithm the engine accepts. An empty
	// slice means "accept nothing"; the [DefaultAlgorithmPolicy] helper
	// returns the v1.0 default of Ed25519-only.
	Allowed []Algorithm
}

// DefaultAlgorithmPolicy returns the v1.0 default — Ed25519-only.
//
// Per plan Open Question 2 / §7 phasing, this is the working default
// until an operator overrides it via Config.
func DefaultAlgorithmPolicy() AlgorithmPolicy {
	return AlgorithmPolicy{Allowed: []Algorithm{AlgEd25519}}
}

// Permits reports whether the supplied algorithm is on the allow-list.
//
// Verify pipeline step 2 (plan §4.2) — a false return maps to
// [RejAlgorithmNotPermit].
func (p AlgorithmPolicy) Permits(alg Algorithm) bool {
	for _, a := range p.Allowed {
		if a == alg {
			return true
		}
	}
	return false
}

// ClockSkewPolicy implements FR-016 — configurable tolerance for envelope
// issued_at / expires_at against the verifier clock. Beyond Tolerance the
// pipeline returns [RejClockSkewExceeded] rather than [RejSignatureInvalid].
type ClockSkewPolicy struct {
	// Tolerance is the symmetric window applied around now for issued_at
	// and expires_at. Default is 5 minutes.
	Tolerance time.Duration
}

// DefaultClockSkewPolicy returns the v1.0 default tolerance of 5 minutes.
func DefaultClockSkewPolicy() ClockSkewPolicy {
	return ClockSkewPolicy{Tolerance: 5 * time.Minute}
}

// Within reports whether t is within Tolerance of now (inclusive).
func (p ClockSkewPolicy) Within(t, now time.Time) bool {
	if t.IsZero() {
		// Zero envelope timestamps are treated as "skip this gate" —
		// allowed for back-compat with envelopes that omit the field.
		return true
	}
	tol := p.Tolerance
	if tol < 0 {
		tol = -tol
	}
	delta := t.Sub(now)
	if delta < 0 {
		delta = -delta
	}
	return delta <= tol
}

// ChainDepthPolicy implements the spec edge-case defense against chain
// complexity attacks — rejects envelopes whose chain length exceeds Max.
type ChainDepthPolicy struct {
	// Max is the maximum allowed chain length. Zero means "no chain
	// permitted" (raw key only). The v1.0 default is 4 — operator-set
	// in Config.
	Max int
}

// DefaultChainDepthPolicy returns the v1.0 default — chain depth 4.
func DefaultChainDepthPolicy() ChainDepthPolicy {
	return ChainDepthPolicy{Max: 4}
}

// Permits reports whether a chain of n intermediates is allowed.
func (p ChainDepthPolicy) Permits(n int) bool {
	return n <= p.Max
}
