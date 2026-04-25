package trust_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/trust"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends"
	"github.com/sigil-tech/kaneaz-harness/core/trust/internal/fingerprint"
)

// stubBackend is a minimal in-test [backends.SigningBackend] used to
// exercise the sign dispatcher without depending on the build-tagged
// software backend. Health is configurable per case to drive
// fail-closed assertions (NFR-007).
type stubBackend struct {
	kind   trust.BackendKind
	health trust.HealthStatus
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
	signs  int
}

func newStubBackend(kind trust.BackendKind, h trust.HealthStatus) *stubBackend {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &stubBackend{kind: kind, health: h, priv: priv, pub: pub}
}

func (s *stubBackend) Kind() trust.BackendKind                    { return s.kind }
func (s *stubBackend) Health(_ context.Context) trust.HealthStatus { return s.health }
func (s *stubBackend) SupportedAlgorithms() []trust.Algorithm     { return []trust.Algorithm{trust.AlgEd25519} }
func (s *stubBackend) Sign(_ context.Context, _ trust.BackendRef, _ trust.Algorithm, payload []byte) ([]byte, error) {
	s.signs++
	return ed25519.Sign(s.priv, payload), nil
}
func (s *stubBackend) PublicKey(_ context.Context, _ trust.BackendRef) (trust.PublicKey, error) {
	return trust.PublicKey{
		Algorithm:   trust.AlgEd25519,
		Bytes:       append([]byte(nil), s.pub...),
		Fingerprint: fingerprint.Compute(s.pub),
	}, nil
}

// TestSignFailsClosedOnUnavailableBackend — the dispatcher must NEVER
// fall back to a different backend on unavailability (NFR-007).
//
// Case: oskeychain is unavailable, software is healthy and registered
// with a different kind. Calling Sign with IdentityRef.Backend =
// oskeychain must return ErrBackendUnavailable, not silently dispatch
// to the software backend.
func TestSignFailsClosedOnUnavailableBackend(t *testing.T) {
	reg := backends.NewRegistry()
	healthy := newStubBackend(trust.BackendSoftware, trust.HealthOK)
	unavailable := newStubBackend(trust.BackendOSKeychain, trust.HealthUnavailable)
	reg.Register(healthy)
	reg.Register(unavailable)

	emitter := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{}, reg, emitter)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}

	_, err = eng.Sign(context.Background(), []byte("payload"),
		trust.IdentityRef{Backend: trust.BackendOSKeychain, Path: "x", Algorithm: trust.AlgEd25519},
		trust.SignOptions{})
	if !errors.Is(err, trust.ErrBackendUnavailable) {
		t.Fatalf("Sign returned %v, want ErrBackendUnavailable", err)
	}
	if healthy.signs != 0 {
		t.Fatalf("dispatcher fell back to healthy backend: signs=%d (NFR-007 violation)", healthy.signs)
	}
	if unavailable.signs != 0 {
		t.Fatalf("unavailable backend signed (=%d) despite refused health", unavailable.signs)
	}
	// Audit emission must record backend-unavailable.
	events := emitter.Events()
	found := false
	for _, e := range events {
		if e.Kind == trust.EventBackendUnavailable {
			found = true
			break
		}
	}
	if !found {
		t.Error("no trust/backend-unavailable audit event emitted on fail-closed path")
	}
}

func TestSignSucceedsWithHealthyBackend(t *testing.T) {
	reg := backends.NewRegistry()
	b := newStubBackend(trust.BackendSoftware, trust.HealthOK)
	reg.Register(b)

	eng, err := trust.NewEngineWithEmitter(trust.Config{}, reg, trust.NewMemoryEmitter())
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	env, err := eng.Sign(context.Background(), []byte("payload"),
		trust.IdentityRef{Backend: trust.BackendSoftware, Path: "x", Algorithm: trust.AlgEd25519},
		trust.SignOptions{})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if env.KeyID == "" {
		t.Error("Envelope.KeyID empty (PublicKey fingerprint not propagated)")
	}
	if len(env.Signature) == 0 {
		t.Error("Envelope.Signature empty")
	}
	if b.signs != 1 {
		t.Errorf("backend.Sign called %d times, want 1", b.signs)
	}
}

func TestSignRejectsUnknownBackend(t *testing.T) {
	reg := backends.NewRegistry()
	eng, _ := trust.NewEngineWithEmitter(trust.Config{}, reg, trust.NewMemoryEmitter())
	_, err := eng.Sign(context.Background(), []byte("p"),
		trust.IdentityRef{Backend: trust.BackendAWSKMS, Algorithm: trust.AlgEd25519},
		trust.SignOptions{})
	if !errors.Is(err, trust.ErrBackendNotRegistered) {
		t.Fatalf("Sign with unknown backend returned %v, want ErrBackendNotRegistered", err)
	}
}

func TestSignRejectsUnsupportedAlgorithm(t *testing.T) {
	reg := backends.NewRegistry()
	reg.Register(newStubBackend(trust.BackendSoftware, trust.HealthOK))
	eng, _ := trust.NewEngineWithEmitter(trust.Config{}, reg, trust.NewMemoryEmitter())
	_, err := eng.Sign(context.Background(), []byte("p"),
		trust.IdentityRef{Backend: trust.BackendSoftware, Algorithm: trust.AlgECDSAP256},
		trust.SignOptions{})
	if !errors.Is(err, trust.ErrAlgorithmNotSupported) {
		t.Fatalf("Sign with unsupported algorithm returned %v, want ErrAlgorithmNotSupported", err)
	}
}
