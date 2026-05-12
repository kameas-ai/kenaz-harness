// Package secrets is the view-scoped RPC surface that drives the
// Model-Accessible Secrets panel in Settings. It exposes the
// ExposureIndex as a read-only list, and lets the frontend expose
// (add) or revoke (remove) session-scoped secrets by locator.
//
// Wire shapes are deliberately minimal — no plaintext is ever returned
// to the frontend (FR-005a). The panel renders ref tokens (@secret:<locator>)
// and metadata only.
//
// Spec mapping: model-secret-references-01KW7M5A WP10.
package secrets

import (
	"context"
	"time"
)

// SecretRow is the frontend wire shape for one exposed secret.
// It never contains the plaintext value (FR-005a).
type SecretRow struct {
	// Ref is the @secret:<locator> token the model writes in tool args.
	Ref string `json:"ref"`
	// Locator is the bare locator string (user:<name> / org:<name> etc.).
	Locator string `json:"locator"`
	// Description is the human-readable label supplied at expose time.
	Description string `json:"description"`
	// Kind is the kind hint (bearer, basic, header, raw, aws_cred).
	Kind string `json:"kind"`
	// Scope is the scope this secret is valid in (session, project, agent, workflow).
	Scope string `json:"scope"`
	// ExposedAt is the RFC3339 UTC timestamp when the secret was exposed.
	ExposedAt time.Time `json:"exposedAt"`
}

// ExposeRequest is the wire shape for Secrets_Expose.
type ExposeRequest struct {
	// Locator is the bare locator, e.g. "user:github-pat".
	// Must not start with "@secret:".
	Locator string `json:"locator"`
	// Description is the human-readable label shown in the panel.
	Description string `json:"description"`
	// Kind is one of: bearer, basic, header, raw, aws_cred.
	// Defaults to "raw" when empty.
	Kind string `json:"kind"`
	// Plaintext is the secret value. Zeroed server-side immediately after
	// being handed to the ExposureIndex.
	Plaintext string `json:"plaintext"`
}

// SecretsAPI is the view-scoped surface backing the Model-Accessible
// Secrets panel. Implementations MUST be safe for concurrent use.
type SecretsAPI interface {
	// ListSecrets returns all currently exposed secrets for the session.
	// Plaintext is never included in the result (FR-005a).
	ListSecrets(ctx context.Context) ([]SecretRow, error)

	// ExposeSecret adds a secret to the ExposureIndex under the given
	// locator. The plaintext in req is zeroed before this method returns.
	// Returns ErrLocatorInvalid when the locator is malformed, or
	// ErrLocatorAlreadyExposed when the locator is already present.
	ExposeSecret(ctx context.Context, req ExposeRequest) error

	// RevokeSecret removes a secret from the ExposureIndex by locator.
	// Returns ErrLocatorNotFound when the locator is not present.
	RevokeSecret(ctx context.Context, locator string) error
}
