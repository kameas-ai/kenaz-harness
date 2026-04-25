//go:build !test_software_backend
// +build !test_software_backend

package software

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/trust"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends"
)

// ErrSoftwareBackendDisabled is returned by every entry point in the
// no-build-tag stub so accidental enablement in production binaries is
// impossible. The companion guard test asserts this contract.
var ErrSoftwareBackendDisabled = errors.New("software backend: disabled (build with -tags test_software_backend to enable; never use in production)")

// Backend is the no-op stub the production build compiles. All methods
// refuse to operate so any caller that survives the build tag boundary
// fails closed.
type Backend struct{}

// New returns a stub backend. The stub satisfies
// [backends.SigningBackend] purely so callers can hold a typed handle;
// every operation refuses.
func New() *Backend { return &Backend{} }

// Register is the stub form of registration — it intentionally does not
// register the backend, and tests assert that lookup of "software" on a
// production-build registry returns ErrBackendNotRegistered.
func Register(_ *backends.Registry, _ *Backend) {
	// Deliberately empty. Production builds must not see a software
	// backend in the registry.
}

// Kind reports the BackendKind even in the stub build so type
// assertions in tests can inspect it.
func (*Backend) Kind() trust.BackendKind { return trust.BackendSoftware }

// Health reports HealthUnavailable in the stub build so a misconfigured
// dispatcher fails closed (NFR-007) rather than silently appearing
// healthy.
func (*Backend) Health(_ context.Context) trust.HealthStatus { return trust.HealthUnavailable }

// SupportedAlgorithms reports nothing in the stub build.
func (*Backend) SupportedAlgorithms() []trust.Algorithm { return nil }

// Sign always refuses in the stub build.
func (*Backend) Sign(_ context.Context, _ trust.BackendRef, _ trust.Algorithm, _ []byte) ([]byte, error) {
	return nil, ErrSoftwareBackendDisabled
}

// PublicKey always refuses in the stub build.
func (*Backend) PublicKey(_ context.Context, _ trust.BackendRef) (trust.PublicKey, error) {
	return trust.PublicKey{}, ErrSoftwareBackendDisabled
}
