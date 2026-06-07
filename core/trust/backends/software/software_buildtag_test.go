//go:build !test_software_backend
// +build !test_software_backend

package software_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust"
	"github.com/kameas-ai/kenaz-harness/core/trust/backends"
	"github.com/kameas-ai/kenaz-harness/core/trust/backends/software"
)

// TestStubRefusesSign — the production build's stub backend refuses
// every Sign call. Guards against accidental use in release binaries
// (per WP04 T003 + plan §6.2).
func TestStubRefusesSign(t *testing.T) {
	b := software.New()
	_, err := b.Sign(context.Background(), trust.BackendRef{Path: "x"}, trust.AlgEd25519, []byte("p"))
	if !errors.Is(err, software.ErrSoftwareBackendDisabled) {
		t.Fatalf("stub Sign returned %v, want ErrSoftwareBackendDisabled", err)
	}
}

// TestStubHealthUnavailable — the dispatcher's fail-closed posture
// (NFR-007) needs the stub to report HealthUnavailable so a misconfigured
// production build cannot accidentally produce signatures.
func TestStubHealthUnavailable(t *testing.T) {
	b := software.New()
	if got := b.Health(context.Background()); got != trust.HealthUnavailable {
		t.Fatalf("stub Health = %v, want HealthUnavailable", got)
	}
}

// TestStubRegistrationIsNoop — production build's Register intentionally
// does nothing so an attacker cannot smuggle the in-memory backend into
// the registry by forgetting the build tag.
func TestStubRegistrationIsNoop(t *testing.T) {
	r := backends.NewRegistry()
	b := software.New()
	software.Register(r, b)
	if _, err := r.Lookup(trust.BackendSoftware); !errors.Is(err, trust.ErrBackendNotRegistered) {
		t.Fatalf("after stub Register, Lookup returned %v, want ErrBackendNotRegistered", err)
	}
}
