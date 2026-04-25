// Package algo is the internal algorithm registry for core/trust/.
//
// Public algorithm constants live in core/trust (trust.Algorithm); this
// package owns the *math* and the per-algorithm metadata. Adding a new
// algorithm is a single-package change here plus adding the constant in
// core/trust/types.go (NFR-006).
package algo

import (
	"kaneaz-harness/core/trust"
)

// Metadata describes a registered algorithm's runtime parameters.
type Metadata struct {
	Algorithm   trust.Algorithm
	JOSEName    string // canonical JOSE alg name for envelopes
	KeySize     int    // bytes — public key size
	SigSize     int    // bytes — signature size (0 if variable)
	Implemented bool   // false for v1.0 ECDSA / RSA-PSS slots
}

// Verifier verifies a signature; signature math lives behind this
// interface so callers do not have to type-switch on Algorithm.
type Verifier interface {
	Verify(pubKey, payload, signature []byte) error
}

// Registry holds metadata + verifier per registered algorithm.
type Registry struct {
	entries map[trust.Algorithm]entry
}

type entry struct {
	meta     Metadata
	verifier Verifier
}

// New builds a fresh Registry pre-populated with the v1.0 set.
func New() *Registry {
	r := &Registry{entries: make(map[trust.Algorithm]entry)}
	r.register(Metadata{
		Algorithm:   trust.AlgEd25519,
		JOSEName:    "EdDSA",
		KeySize:     32,
		SigSize:     64,
		Implemented: true,
	}, ed25519Verifier{})
	r.register(Metadata{
		Algorithm:   trust.AlgECDSAP256,
		JOSEName:    "ES256",
		KeySize:     65, // uncompressed P-256 point
		SigSize:     64,
		Implemented: false,
	}, notImplementedVerifier{alg: trust.AlgECDSAP256})
	r.register(Metadata{
		Algorithm:   trust.AlgRSAPSSSHA256,
		JOSEName:    "PS256",
		KeySize:     0, // variable
		SigSize:     0, // variable
		Implemented: false,
	}, notImplementedVerifier{alg: trust.AlgRSAPSSSHA256})
	return r
}

func (r *Registry) register(m Metadata, v Verifier) {
	r.entries[m.Algorithm] = entry{meta: m, verifier: v}
}

// Lookup returns metadata for an algorithm. The bool reports whether the
// algorithm is registered at all (vs not implemented yet — that is the
// Implemented field on Metadata).
func (r *Registry) Lookup(alg trust.Algorithm) (Metadata, bool) {
	e, ok := r.entries[alg]
	if !ok {
		return Metadata{}, false
	}
	return e.meta, true
}

// Verify dispatches to the verifier registered for alg. Returns
// [trust.ErrAlgorithmNotImplemented] for v1.0 unimplemented slots
// (ECDSA-P256, RSA-PSS) and [trust.ErrAlgorithmNotSupported] when the
// algorithm is unknown to the registry.
func (r *Registry) Verify(alg trust.Algorithm, pubKey, payload, signature []byte) error {
	e, ok := r.entries[alg]
	if !ok {
		return trust.ErrAlgorithmNotSupported
	}
	return e.verifier.Verify(pubKey, payload, signature)
}

// Implemented enumerates algorithms with a real verifier; useful for
// preflight when reporting which anchors map to working math.
func (r *Registry) Implemented() []trust.Algorithm {
	out := make([]trust.Algorithm, 0, len(r.entries))
	for k, e := range r.entries {
		if e.meta.Implemented {
			out = append(out, k)
		}
	}
	return out
}

type notImplementedVerifier struct{ alg trust.Algorithm }

func (n notImplementedVerifier) Verify(_, _, _ []byte) error {
	return trust.ErrAlgorithmNotImplemented
}
