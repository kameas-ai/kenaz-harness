// Package permissions is the view-scoped RPC surface for the universal
// interactive permission system (mission cedar-credential-policy-01KQ8TDE,
// WP02). It exposes three operations to the frontend:
//
//   - Resolve(requestID, decision) — handles the modal's
//     Allow once / Allow always / Deny buttons by routing the choice
//     back into the cedar prompt registry.
//   - ListGrants() — enumerates persisted Cedar `.cedar` files in
//     <DataDir>/policy/ alongside the per-process transient (Allow once)
//     cache so the Settings panel can render the user's accumulated
//     grants.
//   - RevokeGrant(grantID) — deletes the underlying `.cedar` file and
//     triggers an engine reload.
//
// The permissions view is the ONLY caller of `Registry.Resolve` from
// the rpc layer — the cedar package keeps the prompt machinery
// transport-agnostic, and this view bridges it to the Wails-bound
// surface.
package permissions

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Decision is the closed-set string the modal emits. Mirrors
// cedar.PromptDecision through the RPC boundary; the alias avoids
// JS-side type duplication.
type Decision = cedar.PromptDecision

// Closed-set string constants for the three modal buttons. Re-exported
// here so the frontend's TS client can refer to a single canonical
// place without reaching into the cedar package.
const (
	DecisionAllowOnce   = cedar.DecisionAllowOnce
	DecisionAllowAlways = cedar.DecisionAllowAlways
	DecisionDeny        = cedar.DecisionDeny
)

// PendingRequest is the modal-render payload (mirror of
// cedar.PendingRequest). Surfaced by ListGrants so the frontend can
// reconcile in-flight prompts after a reload.
type PendingRequest = cedar.PendingRequest

// Grant is one row in the Settings panel's permission list. Either
// PolicyFile is non-empty (a persisted `.cedar` file) OR Transient is
// true (an in-memory Allow-once cache hit). Never both.
type Grant struct {
	// ID is the stable identifier the frontend round-trips to RevokeGrant.
	// For persisted grants this is the .cedar filename. For transient
	// grants it is the resource-key string ("bash::git status" etc.).
	ID string `json:"id"`

	// PolicyFile is the on-disk filename when the grant is persisted.
	// Empty for transient grants.
	PolicyFile string `json:"policy_file,omitempty"`

	// Transient marks an Allow-once cache entry that lives only for the
	// process lifetime.
	Transient bool `json:"transient"`

	// ResourceKey is the family-prefixed resource string. Used by the
	// frontend to bucket grants per-family without parsing the policy
	// file body. Empty when the policy filename does not match the
	// `<family>_allow_*.cedar` convention.
	ResourceKey string `json:"resource_key,omitempty"`

	// Family is the resource family the grant covers, when derivable
	// from the filename ("bash", "fs", "cred", "tool"). Empty when
	// neither the filename nor the resource-key encodes it.
	Family string `json:"family,omitempty"`
}

// PermissionsAPI is the view-scoped accessor consumed by the
// frontend's permission Settings panels and the four prompt modals.
type PermissionsAPI interface {
	// Resolve routes a user-driven decision back to the cedar prompt
	// registry. requestID was emitted on the family's broker topic;
	// decision is one of allow_once / allow_always / deny. Returns
	// ErrUnknownRequest when the request id has already resolved
	// (timeout, cancellation, prior Resolve).
	Resolve(ctx context.Context, requestID string, decision Decision) error

	// ListGrants enumerates accumulated grants. Persisted `.cedar`
	// files in <DataDir>/policy/ matching `<family>_allow_*.cedar` are
	// returned alongside the per-process transient (Allow once) cache.
	// When family is non-empty ("bash" / "fs" / "cred" / "tool") only
	// grants of that family are returned; empty string returns all.
	ListGrants(ctx context.Context, family string) ([]Grant, error)

	// RevokeGrant removes a grant. For persisted grants the underlying
	// `.cedar` file is deleted and the engine is reloaded. For
	// transient grants the cache entry is dropped (in-memory only).
	RevokeGrant(ctx context.Context, grantID string) error

	// ListPending returns in-flight pending prompts. The frontend uses
	// this to reconcile its modal queue on app start / after a hot
	// reload.
	ListPending(ctx context.Context) ([]PendingRequest, error)
}
