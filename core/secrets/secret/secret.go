// Package secret defines the in-memory representation of a resolved
// credential and the StdlibSecret baseline implementation.
//
// A Secret is the only object in the harness that holds a resolved
// credential value, and its API is shaped to make leakage hard:
//
//   - The value is `[]byte`. Strings are immutable, can be interned,
//     and can leak through panic traces and `fmt` formatting; bytes can
//     be zeroed. The lint analyzer in `core/secrets/lint` mechanically
//     enforces this on credential-shaped names.
//
//   - The value is exposed only through Use(fn func([]byte) error).
//     Callers receive a borrowed view scoped to fn's execution; after
//     fn returns, the implementation is free to zero the buffer.
//
//   - Destroy() zeroizes the buffer with an explicit loop followed by
//     runtime.KeepAlive to defeat compiler dead-store elimination.
//
// Idiomatic call site:
//
//	secret, err := resolver.Resolve(ctx, ref, "llm:anthropic")
//	if err != nil { return err }
//	defer secret.Destroy()
//	if err := secret.Use(func(v []byte) error { return llmCall(ctx, v) }); err != nil {
//	    return err
//	}
//
// Spec mapping: FR-013, NFR-003, D6, D7, plan §4 (Zeroize on Destroy).
package secret

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

// Secret is the runtime representation of a resolved credential. The
// interface is `[]byte`-only on its credential surface (Use receives a
// `[]byte`); the only string-typed accessor is ReferenceID, which
// returns a redaction-safe identifier per ref.CredentialReference.ID.
type Secret interface {
	// Use grants fn a borrowed view of the credential bytes. The
	// slice is valid only for the duration of fn; implementations
	// MAY zeroize after fn returns. Use must reject calls after
	// Destroy with ErrSecretDestroyed.
	//
	// Use is single-threaded by contract: callers MUST NOT pass the
	// slice to goroutines that outlive fn.
	Use(fn func(value []byte) error) error
	// Destroy zeroizes the underlying buffer and releases any
	// platform resources. Idempotent: a second call is a safe no-op.
	Destroy()
	// ReferenceID returns the redaction-safe reference id (see
	// ref.CredentialReference.ID). It is the only string the Secret
	// will yield about its origin.
	ReferenceID() string
}

// Errors returned by Secret implementations.
var (
	// ErrSecretDestroyed is returned by Use when the Secret has
	// already been destroyed.
	ErrSecretDestroyed = errors.New("secret: already destroyed")
	// ErrSecretInUse is returned when concurrent Use is detected.
	ErrSecretInUse = errors.New("secret: concurrent use not permitted")
)

// StdlibSecret is the default Secret implementation. It backs the
// credential bytes with a `[]byte`, zeroizes via an explicit loop on
// Destroy, and uses runtime.KeepAlive to prevent the compiler from
// eliding the zero loop.
//
// StdlibSecret is sufficient for the SOC 2 baseline. memguard-backed
// hardening (locked pages, encryption-at-rest in memory) is opt-in and
// lands behind a build tag in WP14.
type StdlibSecret struct {
	mu          sync.Mutex
	buf         []byte
	destroyed   bool
	inUse       bool
	referenceID string
	consumerID  string
	acquiredAt  time.Time
}

// NewStdlibSecret takes ownership of buf. The caller MUST NOT retain
// any reference to buf after this call; the Secret will zeroize it on
// Destroy.
//
// referenceID is the redaction-safe id from
// ref.CredentialReference.ID; consumerID is the FR-016 scoping tag
// (may be empty).
func NewStdlibSecret(buf []byte, referenceID, consumerID string) *StdlibSecret {
	return &StdlibSecret{
		buf:         buf,
		referenceID: referenceID,
		consumerID:  consumerID,
		acquiredAt:  time.Now(),
	}
}

// Use implements Secret.Use. It serializes concurrent callers; the
// second concurrent caller receives ErrSecretInUse rather than racing
// on the buffer.
func (s *StdlibSecret) Use(fn func(value []byte) error) error {
	if fn == nil {
		return errors.New("secret: nil Use closure")
	}
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return ErrSecretDestroyed
	}
	if s.inUse {
		s.mu.Unlock()
		return ErrSecretInUse
	}
	s.inUse = true
	buf := s.buf
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inUse = false
		s.mu.Unlock()
		// runtime.KeepAlive prevents the compiler from treating the
		// caller's borrow as dead before fn returns. Without it the
		// optimiser is permitted to elide the zero loop in Destroy if
		// it can prove no further reads from buf occur.
		runtime.KeepAlive(buf)
	}()

	return fn(buf)
}

// Destroy implements Secret.Destroy. It zeroes the buffer with an
// explicit loop followed by runtime.KeepAlive. The second call is a
// safe no-op.
func (s *StdlibSecret) Destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return
	}
	zeroize(s.buf)
	s.buf = nil
	s.destroyed = true
}

// ReferenceID implements Secret.ReferenceID.
func (s *StdlibSecret) ReferenceID() string {
	return s.referenceID
}

// ConsumerID returns the FR-016 scoping tag. Safe for logging.
func (s *StdlibSecret) ConsumerID() string {
	return s.consumerID
}

// AcquiredAt is the wall-clock time at which the Secret was created.
// Safe for logging.
func (s *StdlibSecret) AcquiredAt() time.Time {
	return s.acquiredAt
}

// IsDestroyed is exposed for tests and for the cache eviction path.
func (s *StdlibSecret) IsDestroyed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destroyed
}

// Len returns the length of the underlying buffer, or 0 if destroyed.
// It does NOT yield the value.
func (s *StdlibSecret) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return 0
	}
	return len(s.buf)
}

// zeroize fills b with zeros. It is a function (not inlined) so that
// the compiler is less aggressive about eliding it in callers that may
// not appear to read b again. runtime.KeepAlive in Destroy and Use is
// the real guarantee; this is an additional belt.
//
//go:noinline
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// Compile-time assertion that StdlibSecret satisfies Secret.
var _ Secret = (*StdlibSecret)(nil)
