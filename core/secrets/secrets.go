// Package secrets is a stub implementation of the indirect credential
// reference machinery owned by the secrets-keychain-01KQ1A3M mission.
//
// The full implementation lives in that mission. This package exposes the
// shape required by the ACP orchestration mission so that the ACP code
// can compile, link, and test against a credible interface today.
//
// When secrets-keychain lands, callers replace the local Backend impls
// here with the real keychain-backed Backend; the public types
// (Reference, Secret, Backend, Kind constants) are intended to be
// drop-in compatible.
package secrets

import (
	"context"
	"errors"
	"fmt"
)

// Kind enumerates the supported credential reference kinds.
type Kind string

const (
	KindEnv        Kind = "env"
	KindKeychain   Kind = "keychain"
	KindFile       Kind = "file"
	KindAWSProfile Kind = "aws_profile"
	KindKMS        Kind = "kms"
)

// Reference is an indirect, plaintext-free pointer to a credential.
//
// Callers MUST NOT marshal Reference values to log entries or
// configuration outputs in a form that includes the resolved bytes;
// only Kind and Lookup are safe to surface for diagnostics.
type Reference struct {
	Kind   Kind   `json:"kind"`
	Lookup string `json:"lookup"`
}

// String returns a redaction-safe description suitable for diagnostics
// and event-log payloads. It MUST never include the resolved secret
// bytes.
func (r Reference) String() string {
	return fmt.Sprintf("%s://%s", r.Kind, r.Lookup)
}

// Secret holds resolved credential bytes. Callers MUST call Zeroize once
// the credential has been consumed to minimise its in-memory residency.
type Secret struct {
	bytes []byte
}

// NewSecret wraps b in a Secret. The caller transfers ownership; the
// returned Secret will mutate b on Zeroize.
func NewSecret(b []byte) *Secret {
	return &Secret{bytes: b}
}

// Bytes returns the resolved credential bytes. Callers MUST NOT retain
// the slice past the next Zeroize.
func (s *Secret) Bytes() []byte {
	if s == nil {
		return nil
	}
	return s.bytes
}

// Zeroize wipes the underlying bytes in place.
func (s *Secret) Zeroize() {
	if s == nil {
		return
	}
	for i := range s.bytes {
		s.bytes[i] = 0
	}
	s.bytes = nil
}

// ErrReferenceUnresolved is returned when a Reference cannot be resolved
// (env unset, keychain entry missing, file unreadable, etc.).
var ErrReferenceUnresolved = errors.New("secrets: reference unresolved")

// Backend resolves indirect credential references into Secret values.
// Implementations MUST never persist the resolved bytes outside a
// returned Secret.
type Backend interface {
	Resolve(ctx context.Context, ref Reference) (*Secret, error)
	PreflightAll(ctx context.Context, refs []Reference) []PreflightResult
}

// PreflightResult reports whether a single Reference is currently
// resolvable.
type PreflightResult struct {
	Reference Reference
	Success   bool
	Error     error
}

// StaticBackend is an in-memory Backend useful for tests and the v1
// stub. Real backends live in core/secrets/{env,keychain,file,...}/.
type StaticBackend struct {
	// Values is keyed by the canonical Reference.String().
	Values map[string][]byte
}

// NewStaticBackend constructs a StaticBackend pre-populated with values.
func NewStaticBackend(values map[string][]byte) *StaticBackend {
	if values == nil {
		values = map[string][]byte{}
	}
	return &StaticBackend{Values: values}
}

// Resolve returns the configured value for ref or
// ErrReferenceUnresolved.
func (b *StaticBackend) Resolve(_ context.Context, ref Reference) (*Secret, error) {
	if b == nil || b.Values == nil {
		return nil, fmt.Errorf("%w: %s", ErrReferenceUnresolved, ref.String())
	}
	v, ok := b.Values[ref.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrReferenceUnresolved, ref.String())
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return NewSecret(cp), nil
}

// PreflightAll runs Resolve over every reference and reports the
// outcome.
func (b *StaticBackend) PreflightAll(ctx context.Context, refs []Reference) []PreflightResult {
	out := make([]PreflightResult, len(refs))
	for i, ref := range refs {
		s, err := b.Resolve(ctx, ref)
		if s != nil {
			s.Zeroize()
		}
		out[i] = PreflightResult{Reference: ref, Success: err == nil, Error: err}
	}
	return out
}
