//go:build test_software_backend
// +build test_software_backend

package software

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"runtime"
	"sync"

	"kaneaz-harness/core/trust"
	"kaneaz-harness/core/trust/backends"
	"kaneaz-harness/core/trust/internal/fingerprint"
)

// Backend is the test-only in-memory [backends.SigningBackend].
//
// It holds Ed25519 keypairs keyed by ref.Path and produces detached
// signatures via stdlib crypto/ed25519. Health is always HealthOK in
// this build; production binaries do not compile this file.
type Backend struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PrivateKey
	pubs map[string]ed25519.PublicKey
}

// New constructs an empty test-only software backend.
func New() *Backend {
	return &Backend{
		keys: make(map[string]ed25519.PrivateKey),
		pubs: make(map[string]ed25519.PublicKey),
	}
}

// Register registers the backend on the given registry. Used in tests
// that wire up a sign-capable engine.
func Register(r *backends.Registry, b *Backend) {
	r.Register(b)
}

// Kind implements [backends.SigningBackend].
func (b *Backend) Kind() trust.BackendKind { return trust.BackendSoftware }

// Health implements [backends.SigningBackend].
func (b *Backend) Health(_ context.Context) trust.HealthStatus { return trust.HealthOK }

// SupportedAlgorithms implements [backends.SigningBackend].
func (b *Backend) SupportedAlgorithms() []trust.Algorithm {
	return []trust.Algorithm{trust.AlgEd25519}
}

// Sign implements [backends.SigningBackend].
func (b *Backend) Sign(_ context.Context, ref trust.BackendRef, alg trust.Algorithm, payload []byte) ([]byte, error) {
	if alg != trust.AlgEd25519 {
		return nil, trust.ErrAlgorithmNotSupported
	}
	b.mu.RLock()
	priv, ok := b.keys[ref.Path]
	b.mu.RUnlock()
	if !ok {
		return nil, errors.New("software: key not found at ref.Path")
	}
	sig := ed25519.Sign(priv, payload)
	// Keep the private buffer alive across the signing call so the GC
	// cannot scribble it before crypto/ed25519 returns. Mirrors the
	// pattern documented in secrets-keychain D6/D7.
	runtime.KeepAlive(priv)
	return sig, nil
}

// PublicKey implements [backends.SigningBackend].
func (b *Backend) PublicKey(_ context.Context, ref trust.BackendRef) (trust.PublicKey, error) {
	b.mu.RLock()
	pub, ok := b.pubs[ref.Path]
	b.mu.RUnlock()
	if !ok {
		return trust.PublicKey{}, errors.New("software: key not found at ref.Path")
	}
	bytes := append([]byte(nil), pub...)
	return trust.PublicKey{
		Algorithm:   trust.AlgEd25519,
		Bytes:       bytes,
		Fingerprint: fingerprint.Compute(bytes),
	}, nil
}

// GenerateAndStore generates a fresh Ed25519 keypair and stores it
// under refPath. Returns the public key for installation as a trust
// anchor.
func (b *Backend) GenerateAndStore(refPath string) (trust.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return trust.PublicKey{}, err
	}
	b.mu.Lock()
	b.keys[refPath] = priv
	b.pubs[refPath] = pub
	b.mu.Unlock()
	pubBytes := append([]byte(nil), pub...)
	return trust.PublicKey{
		Algorithm:   trust.AlgEd25519,
		Bytes:       pubBytes,
		Fingerprint: fingerprint.Compute(pubBytes),
	}, nil
}

// Close zeroes every cached private key. Test fixtures should call it
// in t.Cleanup so race detectors do not see stale key bytes after the
// test completes.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, priv := range b.keys {
		for i := range priv {
			priv[i] = 0
		}
	}
	b.keys = make(map[string]ed25519.PrivateKey)
	b.pubs = make(map[string]ed25519.PublicKey)
	return nil
}
