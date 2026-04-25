// Package credref bridges the connector's CredentialReference type to
// the upstream core/secrets.Backend (plan §6.1).
//
// This package intentionally adds NO caching of resolved credentials —
// the upstream secrets backend owns the TTL cache (secrets FR-010). The
// bridge's only responsibilities are:
//
//   - Translate llm.CredentialReference → secrets.CredentialReference.
//   - Resolve a reference at request time and surface it as a *Cred handle.
//
// Cred is shaped to satisfy two callers that landed in parallel WPs and
// converged here:
//
//   - core/llm/registry calls Use(fn) / Destroy() on the returned value
//     (the canonical secret.Secret discipline from secrets-keychain).
//   - older credref tests call s.Bytes() / Zeroize(s).
//
// Both surfaces operate on the same underlying buffer; Zeroize is an
// alias for Destroy that also accepts nil.
package credref

import (
	"context"
	"errors"
	"fmt"
	"sync"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// ErrNoBridge is retained for back-compat with callers that branched on
// it during the parallel-merge window. New code should match against
// llm.ErrCredentialResolution.
var ErrNoBridge = errors.New("credref: secrets bridge not configured")

// Resolver is the bridge between llm.CredentialReference and
// secrets.Backend.
type Resolver struct {
	backend secrets.Backend
}

// New returns a Resolver backed by b. b may be nil; in that case Resolve
// returns ErrCredentialResolution wrapping ErrNoBridge.
func New(b secrets.Backend) *Resolver { return &Resolver{backend: b} }

// Cred is the bridge's resolved-credential handle. It satisfies
// secret.Secret (Use/Destroy/ReferenceID) so the registry pipeline can
// consume it without a separate adapter, and it exposes Bytes() for
// callers that want a snapshot view.
type Cred struct {
	mu        sync.Mutex
	buf       []byte
	refID     string
	destroyed bool
}

// Bytes returns the underlying buffer. After Destroy / Zeroize it
// returns nil. The returned slice aliases the buffer; callers MUST NOT
// retain it past the next Destroy call.
func (c *Cred) Bytes() []byte {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.destroyed {
		return nil
	}
	return c.buf
}

// Use grants fn a borrowed view of the credential bytes.
func (c *Cred) Use(fn func([]byte) error) error {
	if c == nil {
		return errors.New("credref: nil Cred")
	}
	if fn == nil {
		return errors.New("credref: nil Use closure")
	}
	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		return secret.ErrSecretDestroyed
	}
	buf := c.buf
	c.mu.Unlock()
	return fn(buf)
}

// Destroy zeroizes the underlying buffer and releases the handle.
// Idempotent.
func (c *Cred) Destroy() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.destroyed {
		return
	}
	for i := range c.buf {
		c.buf[i] = 0
	}
	c.buf = nil
	c.destroyed = true
}

// ReferenceID returns the redaction-safe id from the originating
// CredentialReference.
func (c *Cred) ReferenceID() string {
	if c == nil {
		return ""
	}
	return c.refID
}

// Zeroize is an alias for (*Cred).Destroy that tolerates a nil pointer.
// Retained for the bridge's original test surface.
func Zeroize(c *Cred) {
	if c == nil {
		return
	}
	c.Destroy()
}

// ToSecretsRef converts an llm.CredentialReference to the secrets-package
// shape. The Kind string lookup is best-effort; unknown kinds map to
// ref.RefUnknown and downstream resolution returns an error.
func ToSecretsRef(r llm.CredentialReference) secrets.CredentialReference {
	return secrets.CredentialReference{
		Kind:    refKindFromString(r.Kind),
		Locator: r.Locator,
	}
}

// refKindFromString maps the llm-side string Kind to the secrets RefKind
// enum. Local copy of the wire-name table; keep in sync with
// core/secrets/ref/reference.go (kindFromWire).
func refKindFromString(s string) ref.RefKind {
	switch s {
	case "env":
		return ref.RefEnv
	case "keychain":
		return ref.RefKeychain
	case "file":
		return ref.RefFile
	case "aws_profile":
		return ref.RefAWSProfile
	case "aws_kms":
		return ref.RefAWSKMS
	case "yubikey_piv":
		return ref.RefYubikeyPIV
	case "pkcs11":
		return ref.RefPKCS11
	default:
		return ref.RefUnknown
	}
}

// Resolve fetches a credential from the configured secrets.Backend.
//
// On success the returned *Cred owns a private copy of the credential
// bytes; the caller MUST call Destroy / Zeroize when finished. Errors
// are wrapped in llm.ErrCredentialResolution so callers can match via
// errors.As without importing this package.
func (r *Resolver) Resolve(ctx context.Context, profileID string, lref llm.CredentialReference) (*Cred, error) {
	if r == nil || r.backend == nil {
		return nil, &llm.ErrCredentialResolution{
			ProfileID: profileID,
			Ref:       lref,
			Cause:     ErrNoBridge,
		}
	}
	sref := ToSecretsRef(lref)
	if sref.Kind == ref.RefUnknown {
		return nil, &llm.ErrCredentialResolution{
			ProfileID: profileID,
			Ref:       lref,
			Cause:     fmt.Errorf("unknown credential kind %q", lref.Kind),
		}
	}
	sref.ConsumerID = profileID
	s, err := r.backend.Resolve(ctx, sref)
	if err != nil {
		return nil, &llm.ErrCredentialResolution{
			ProfileID: profileID,
			Ref:       lref,
			Cause:     err,
		}
	}
	defer s.Destroy()
	c := &Cred{refID: s.ReferenceID()}
	if useErr := s.Use(func(value []byte) error {
		c.buf = append([]byte(nil), value...)
		return nil
	}); useErr != nil {
		return nil, &llm.ErrCredentialResolution{
			ProfileID: profileID,
			Ref:       lref,
			Cause:     useErr,
		}
	}
	return c, nil
}

// Preflight resolves each profile's credential reference and reports
// per-profile outcomes for use by registry.PreflightAll.
func (r *Resolver) Preflight(ctx context.Context, profs []llm.ProviderProfile) []llm.PreflightResult {
	out := make([]llm.PreflightResult, len(profs))
	for i, p := range profs {
		c, err := r.Resolve(ctx, p.ID, p.Cred)
		if err != nil {
			out[i] = llm.PreflightResult{
				ProfileID: p.ID,
				Kind:      p.Kind,
				Resolved:  false,
				Err:       err,
				Message:   err.Error(),
			}
			continue
		}
		c.Destroy()
		out[i] = llm.PreflightResult{
			ProfileID: p.ID,
			Kind:      p.Kind,
			Resolved:  true,
		}
	}
	return out
}
