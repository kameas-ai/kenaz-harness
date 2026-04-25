// Package credref bridges the connector's CredentialReference type to
// the upstream core/secrets.CredentialReference / Backend (plan §6.1).
//
// This package intentionally adds NO caching of resolved credentials —
// the upstream secrets backend owns the TTL cache (secrets FR-010). The
// bridge's only responsibilities are:
//
//   - Translate llm.CredentialReference → secrets.CredentialReference.
//   - Resolve a reference at request time.
//
// NOTE — POST-MERGE STUB: the implementation that landed in the parallel
// llm-connector worktree referenced shapes (*secrets.Secret pointer,
// secrets.Backend.PreflightAll, ref.Kind-as-string) that the parallel
// secrets-keychain worktree did not produce. The bridge needs a follow-up
// integration WP that adapts to the real secrets API (interface-typed
// Secret with Destroy(), Backend.Resolve only, RefKind as enum). Until
// that lands the package exposes only the ToSecretsRef converter so the
// rest of llm-connector compiles; Resolve / Preflight return ErrNoBridge.
package credref

import (
	"context"
	"errors"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

// ErrNoBridge is returned by stubbed bridge methods until the
// llm↔secrets integration WP lands.
var ErrNoBridge = errors.New("credref: secrets bridge not yet wired")

// Resolver is the placeholder bridge type. Constructed against a real
// secrets.Backend once the integration WP lands.
type Resolver struct {
	backend secrets.Backend
}

// New returns a Resolver backed by b.
func New(b secrets.Backend) *Resolver { return &Resolver{backend: b} }

// ToSecretsRef converts an llm.CredentialReference to the secrets-package
// shape. The Kind string lookup is best-effort; unknown kinds map to
// ref.RefUnknown and downstream resolution returns ErrUnknownKind.
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

// Resolve fetches a credential. STUB — returns ErrNoBridge until the
// llm↔secrets integration WP lands.
func (r *Resolver) Resolve(ctx context.Context, profileID string, _ llm.CredentialReference) (secrets.Secret, error) {
	_ = ctx
	_ = profileID
	return nil, ErrNoBridge
}

// Preflight returns a flat-rejection slice. STUB — returns ErrNoBridge
// for every profile until the integration WP lands.
func (r *Resolver) Preflight(_ context.Context, profs []llm.ProviderProfile) []llm.PreflightResult {
	out := make([]llm.PreflightResult, len(profs))
	for i, p := range profs {
		out[i] = llm.PreflightResult{
			ProfileID: p.ID,
			Kind:      p.Kind,
			Resolved:  false,
			Err:       ErrNoBridge,
			Message:   "credref: bridge not yet wired",
		}
	}
	return out
}
