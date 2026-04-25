// Package credref bridges the connector's CredentialReference type to
// the upstream core/secrets.Reference / Backend (plan §6.1, WP03).
//
// This package intentionally adds NO caching of resolved credentials —
// the upstream secrets backend owns the TTL cache (secrets FR-010).
// The bridge's only responsibilities are:
//
//   - Translate CredentialReference → secrets.Reference.
//   - Resolve a reference at request time.
//   - Provide a Zeroize helper that adapters call as soon as the wire
//     request has been built.
package credref

import (
	"context"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Resolver is the bridge. Implementations forward to a secrets.Backend.
type Resolver struct {
	backend secrets.Backend
}

// New returns a Resolver backed by b.
func New(b secrets.Backend) *Resolver {
	return &Resolver{backend: b}
}

// ToSecretsRef converts an llm.CredentialReference to a secrets.Reference.
func ToSecretsRef(ref llm.CredentialReference) secrets.Reference {
	return secrets.Reference{Kind: ref.Kind, Locator: ref.Locator}
}

// Resolve fetches the credential bytes for ref via the backend. The
// caller MUST call Zeroize on the returned Secret as soon as the wire
// request has been built (secrets FR-013).
func (r *Resolver) Resolve(ctx context.Context, profileID string, ref llm.CredentialReference) (*secrets.Secret, error) {
	if r == nil || r.backend == nil {
		return nil, &llm.ErrCredentialResolution{
			ProfileID: profileID, Ref: ref,
			Cause: secrets.ErrUnknownKind,
		}
	}
	s, err := r.backend.Resolve(ctx, ToSecretsRef(ref))
	if err != nil {
		return nil, &llm.ErrCredentialResolution{ProfileID: profileID, Ref: ref, Cause: err}
	}
	return s, nil
}

// Zeroize clears the secret's underlying buffer. Safe on a nil receiver
// or a nil Secret.
func Zeroize(s *secrets.Secret) {
	if s == nil {
		return
	}
	s.Zeroize()
}

// Preflight wraps backend.PreflightAll using the profile's reference.
// The returned slice is in the same order as profs.
func (r *Resolver) Preflight(ctx context.Context, profs []llm.ProviderProfile) []llm.PreflightResult {
	if r == nil || r.backend == nil {
		out := make([]llm.PreflightResult, len(profs))
		for i, p := range profs {
			out[i] = llm.PreflightResult{
				ProfileID: p.ID, Kind: p.Kind, Resolved: false,
				Err:     secrets.ErrUnknownKind,
				Message: "no secrets backend configured",
			}
		}
		return out
	}
	refs := make([]secrets.Reference, len(profs))
	for i, p := range profs {
		refs[i] = ToSecretsRef(p.Cred)
	}
	outcomes := r.backend.PreflightAll(ctx, refs)
	results := make([]llm.PreflightResult, len(profs))
	for i, p := range profs {
		oc := outcomes[i]
		results[i] = llm.PreflightResult{
			ProfileID: p.ID, Kind: p.Kind, Resolved: oc.Resolved, Err: oc.Err,
		}
		if oc.Err != nil {
			results[i].Message = oc.Err.Error()
		}
	}
	return results
}
