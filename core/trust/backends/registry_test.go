package backends_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust"
	"github.com/kameas-ai/kenaz-harness/core/trust/backends"
)

// fakeBackend implements [backends.SigningBackend] for registry tests.
type fakeBackend struct {
	kind   trust.BackendKind
	health trust.HealthStatus
	algs   []trust.Algorithm
}

func (f *fakeBackend) Kind() trust.BackendKind                    { return f.kind }
func (f *fakeBackend) Health(_ context.Context) trust.HealthStatus { return f.health }
func (f *fakeBackend) SupportedAlgorithms() []trust.Algorithm     { return f.algs }
func (f *fakeBackend) Sign(_ context.Context, _ trust.BackendRef, _ trust.Algorithm, _ []byte) ([]byte, error) {
	return []byte("sig"), nil
}
func (f *fakeBackend) PublicKey(_ context.Context, _ trust.BackendRef) (trust.PublicKey, error) {
	return trust.PublicKey{}, nil
}

func TestRegistryLookupHit(t *testing.T) {
	r := backends.NewRegistry()
	b := &fakeBackend{kind: trust.BackendSoftware, health: trust.HealthOK, algs: []trust.Algorithm{trust.AlgEd25519}}
	r.Register(b)

	got, err := r.Lookup(trust.BackendSoftware)
	if err != nil {
		t.Fatalf("Lookup hit returned error: %v", err)
	}
	if got.Kind() != trust.BackendSoftware {
		t.Fatalf("Lookup returned kind %q, want %q", got.Kind(), trust.BackendSoftware)
	}
}

func TestRegistryLookupMiss(t *testing.T) {
	r := backends.NewRegistry()
	_, err := r.Lookup(trust.BackendOSKeychain)
	if !errors.Is(err, trust.ErrBackendNotRegistered) {
		t.Fatalf("Lookup miss returned %v, want ErrBackendNotRegistered", err)
	}
}

func TestRegistryKindsListsRegistered(t *testing.T) {
	r := backends.NewRegistry()
	r.Register(&fakeBackend{kind: trust.BackendSoftware, health: trust.HealthOK})
	r.Register(&fakeBackend{kind: trust.BackendAWSKMS, health: trust.HealthOK})
	kinds := r.Kinds()
	if len(kinds) != 2 {
		t.Fatalf("Kinds() returned %d, want 2", len(kinds))
	}
}
