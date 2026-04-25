package oskeychain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/trust"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends/oskeychain"
)

// TestStubFailsClosed — the v1.0 stub reports HealthUnavailable so the
// sign dispatcher fails closed (NFR-007).
func TestStubFailsClosed(t *testing.T) {
	b := oskeychain.New()
	if h := b.Health(context.Background()); h != trust.HealthUnavailable {
		t.Fatalf("stub Health = %v, want HealthUnavailable", h)
	}
	if _, err := b.Sign(context.Background(), trust.BackendRef{Path: "x"}, trust.AlgEd25519, []byte("p")); !errors.Is(err, oskeychain.ErrNotImplemented) {
		t.Fatalf("stub Sign returned %v, want ErrNotImplemented", err)
	}
}

// TestStubRegisteredFailsClosedThroughDispatcher — registering the stub
// and asking the dispatcher to use it must surface ErrBackendUnavailable
// (the typed fail-closed error), proving NFR-007 holds end-to-end.
func TestStubRegisteredFailsClosedThroughDispatcher(t *testing.T) {
	r := backends.NewRegistry()
	r.Register(oskeychain.New())
	eng, err := trust.NewEngineWithEmitter(trust.Config{}, r, trust.NewMemoryEmitter())
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	_, err = eng.Sign(context.Background(), []byte("p"),
		trust.IdentityRef{Backend: trust.BackendOSKeychain, Path: "alice", Algorithm: trust.AlgEd25519},
		trust.SignOptions{})
	if !errors.Is(err, trust.ErrBackendUnavailable) {
		t.Fatalf("Sign through unavailable oskeychain returned %v, want ErrBackendUnavailable", err)
	}
}
