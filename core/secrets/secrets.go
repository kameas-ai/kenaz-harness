// Package secrets is a STUB for the (separately developed)
// secrets-keychain mission. The connector depends only on the public
// types defined here; replacing this stub with the real package is a
// single-import-path change.
//
// Cross-mission contract (from the llm-connector plan §6.1):
//
//   - Reference is the indirect pointer (matches secrets FR-001).
//   - Backend.Resolve returns the resolved bytes (Secret) and the
//     caller is responsible for calling Secret.Zeroize after the
//     adapter has built its wire request (secrets FR-013).
//   - Backend.PreflightAll iterates every reference and reports
//     successes / failures.
//
// The stub implementation reads from os.Getenv for "env" references
// and from a fake in-memory map for "keychain" references, sufficient
// for the connector's unit and integration tests under -race.
package secrets

import (
	"context"
	"errors"
	"os"
	"sync"
)

// Reference matches secrets FR-001.
type Reference struct {
	Kind    string `json:"kind"    yaml:"kind"`
	Locator string `json:"locator" yaml:"locator"`
}

// Secret carries resolved credential bytes. Always treat as a transient
// buffer — call Zeroize() once the wire request is built.
type Secret struct {
	bytes []byte
}

// NewSecret wraps b. The caller transfers ownership; do not retain a
// reference to b after passing it here.
func NewSecret(b []byte) *Secret { return &Secret{bytes: b} }

// Bytes returns the raw bytes; only the adapter that owns the lifecycle
// should call this.
func (s *Secret) Bytes() []byte {
	if s == nil {
		return nil
	}
	return s.bytes
}

// String forces a redaction-friendly rendering. Never returns the raw
// secret material.
func (s *Secret) String() string { return "<redacted>" }

// Zeroize overwrites the underlying buffer with zeros and drops the
// reference. Idempotent.
func (s *Secret) Zeroize() {
	if s == nil {
		return
	}
	for i := range s.bytes {
		s.bytes[i] = 0
	}
	s.bytes = nil
}

// Backend is the resolver surface (matches secrets FR-002).
type Backend interface {
	Resolve(ctx context.Context, ref Reference) (*Secret, error)
	PreflightAll(ctx context.Context, refs []Reference) []PreflightOutcome
}

// PreflightOutcome reports per-reference resolvability.
type PreflightOutcome struct {
	Ref      Reference
	Resolved bool
	Err      error
}

// Sentinel errors.
var (
	ErrUnknownKind     = errors.New("secrets: unknown reference kind")
	ErrNotFound        = errors.New("secrets: reference not found")
	ErrEmptyLocator    = errors.New("secrets: locator is empty")
	ErrInvalidProfile  = errors.New("secrets: invalid aws profile")
)

// MemoryBackend is the in-memory test implementation. It reads "env"
// from os.Getenv and "keychain" / "file" / "aws_profile" / "kms" from
// an internal map. Tests pre-populate the map via Put.
type MemoryBackend struct {
	mu    sync.Mutex
	store map[string][]byte
}

// NewMemoryBackend returns an empty MemoryBackend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{store: map[string][]byte{}}
}

// Put pre-populates the keyed (kind, locator) entry.
func (b *MemoryBackend) Put(kind, locator string, value []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.store[kind+"|"+locator] = append([]byte(nil), value...)
}

// Resolve materializes ref into a Secret.
func (b *MemoryBackend) Resolve(_ context.Context, ref Reference) (*Secret, error) {
	if ref.Locator == "" {
		return nil, ErrEmptyLocator
	}
	switch ref.Kind {
	case "env":
		v := os.Getenv(ref.Locator)
		if v == "" {
			return nil, ErrNotFound
		}
		return NewSecret([]byte(v)), nil
	case "keychain", "file", "aws_profile", "kms":
		b.mu.Lock()
		defer b.mu.Unlock()
		val, ok := b.store[ref.Kind+"|"+ref.Locator]
		if !ok {
			return nil, ErrNotFound
		}
		out := append([]byte(nil), val...)
		return NewSecret(out), nil
	default:
		return nil, ErrUnknownKind
	}
}

// PreflightAll iterates refs and reports per-reference results.
func (b *MemoryBackend) PreflightAll(ctx context.Context, refs []Reference) []PreflightOutcome {
	out := make([]PreflightOutcome, 0, len(refs))
	for _, r := range refs {
		s, err := b.Resolve(ctx, r)
		if err != nil {
			out = append(out, PreflightOutcome{Ref: r, Resolved: false, Err: err})
			continue
		}
		s.Zeroize()
		out = append(out, PreflightOutcome{Ref: r, Resolved: true})
	}
	return out
}
