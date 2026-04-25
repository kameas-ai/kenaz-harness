package oskeychain

import (
	"context"
	"errors"

	"kaneaz-harness/core/trust"
)

// ErrNotImplemented is returned by every method until the real
// zalando/go-keyring-backed implementation lands. Until then the
// dispatcher fails closed (NFR-007) because Health reports
// HealthUnavailable.
var ErrNotImplemented = errors.New("oskeychain: backend not implemented at v1.0 (interface stub — register the real backend in WP05 follow-up)")

// Backend is the [trust.Backend] surface for OS-keychain-backed
// signing. v1.0 ships an interface-conformant stub so the dispatcher
// type-checks and the test matrix can probe fail-closed semantics
// without a live keyring.
type Backend struct{}

// New constructs the v1.0 stub backend. Once zalando/go-keyring is
// vendored and the platform-specific implementations land, swap this
// constructor for the real one — interface remains stable.
func New() *Backend { return &Backend{} }

// Kind implements [trust.Backend].
func (*Backend) Kind() trust.BackendKind { return trust.BackendOSKeychain }

// Health implements [trust.Backend]. The stub reports HealthUnavailable
// so the dispatcher fails closed (NFR-007). Production implementation
// will probe the platform's Keychain / Secret Service / Credential
// Manager and only return HealthOK when reachable.
func (*Backend) Health(_ context.Context) trust.HealthStatus {
	return trust.HealthUnavailable
}

// SupportedAlgorithms implements [trust.Backend]. Reports the algorithm
// set the production backend will support; with Health=Unavailable the
// dispatcher will refuse to call Sign before reaching this list.
func (*Backend) SupportedAlgorithms() []trust.Algorithm {
	return []trust.Algorithm{trust.AlgEd25519}
}

// Sign implements [trust.Backend]. Always refuses in the v1.0 stub.
func (*Backend) Sign(_ context.Context, _ trust.BackendRef, _ trust.Algorithm, _ []byte) ([]byte, error) {
	return nil, ErrNotImplemented
}

// PublicKey implements [trust.Backend]. Always refuses in the v1.0 stub.
func (*Backend) PublicKey(_ context.Context, _ trust.BackendRef) (trust.PublicKey, error) {
	return trust.PublicKey{}, ErrNotImplemented
}
