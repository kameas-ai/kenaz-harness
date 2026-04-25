package trust

import (
	"context"
	"time"
)

// BackendDispatcher is the surface the sign dispatcher uses to resolve a
// backend. It mirrors backends.Registry but is declared here to avoid an
// import cycle between core/trust/ (which defines the engine) and
// core/trust/backends/ (which defines the SigningBackend interface that
// imports core/trust types).
//
// In production, the engine receives a *backends.Registry via
// [NewEngineWithEmitter]; tests can pass any implementation.
type BackendDispatcher interface {
	Lookup(kind BackendKind) (Backend, error)
}

// Backend is the cross-package alias for backends.SigningBackend used by
// the sign dispatcher. Defined here as an interface with the exact
// methods so core/trust/ does not import core/trust/backends/. The
// concrete *backends.SigningBackend automatically satisfies this
// interface at construction time.
type Backend interface {
	Kind() BackendKind
	Health(ctx context.Context) HealthStatus
	SupportedAlgorithms() []Algorithm
	Sign(ctx context.Context, ref BackendRef, alg Algorithm, payload []byte) ([]byte, error)
	PublicKey(ctx context.Context, ref BackendRef) (PublicKey, error)
}

// signDeps bundles the collaborators the sign dispatcher needs.
type signDeps struct {
	dispatcher BackendDispatcher
	emitter    Emitter
}

// runSign implements [TrustEngine.Sign] — the v1.0 fail-closed signing
// dispatcher (plan §4.3).
//
// Steps:
//  1. Resolve IdentityRef.Backend via dispatcher.
//  2. Verify backend supports IdentityRef.Algorithm.
//  3. Call backend.Health(ctx); on != HealthOK emit
//     trust/backend-unavailable and return ErrBackendUnavailable.
//  4. Call backend.Sign(ctx, ref, alg, payload).
//  5. Wrap signature bytes in an [Envelope] shell. Real A2A v1
//     conformance lands in WP09.
//
// The dispatcher *never* falls back to a different backend on
// unavailability — that would be the silent fallback NFR-007 explicitly
// forbids.
func runSign(ctx context.Context, d signDeps, payload []byte, ident IdentityRef, opts SignOptions) (Envelope, error) {
	if d.dispatcher == nil {
		return Envelope{}, ErrBackendNotRegistered
	}
	backend, err := d.dispatcher.Lookup(ident.Backend)
	if err != nil {
		return Envelope{}, err
	}

	// Algorithm support check before health probe — cheaper.
	supported := false
	for _, a := range backend.SupportedAlgorithms() {
		if a == ident.Algorithm {
			supported = true
			break
		}
	}
	if !supported {
		return Envelope{}, ErrAlgorithmNotSupported
	}

	// Fail-closed health check (NFR-007).
	status := backend.Health(ctx)
	if status != HealthOK {
		emitAuditOrLog(ctx, d.emitter, Event{
			Kind:        EventBackendUnavailable,
			OccurredAt:  time.Now().UTC(),
			BackendKind: ident.Backend,
			Detail:      status.String(),
		})
		return Envelope{}, ErrBackendUnavailable
	}

	ref := BackendRef{Path: ident.Path}
	sig, err := backend.Sign(ctx, ref, ident.Algorithm, payload)
	if err != nil {
		return Envelope{}, err
	}

	pub, err := backend.PublicKey(ctx, ref)
	if err != nil {
		return Envelope{}, err
	}

	issued := opts.IssuedAt
	if issued.IsZero() {
		issued = time.Now().UTC()
	}
	expires := time.Time{}
	if opts.Lifetime > 0 {
		expires = issued.Add(opts.Lifetime)
	}

	env := Envelope{
		Payload:   payload,
		Signature: sig,
		Algorithm: ident.Algorithm,
		KeyID:     pub.Fingerprint,
		IssuedAt:  issued,
		ExpiresAt: expires,
		Chain:     append([]string(nil), opts.ChainHints...),
		AgentID:   opts.AgentID,
	}
	return env, nil
}
