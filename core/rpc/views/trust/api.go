// Package trust defines the TrustAPI view-scoped accessor.
//
// Per FR-020 / C-004 the UI never sees a resolved credential value.
// `GetSecretReference` returns metadata only — the resolved value
// stays inside core/secrets-keychain.
package trust

import "context"

// SecretReference is reference-only metadata about a stored secret.
// It deliberately has NO `Value`/`Secret`/`Password`/`APIKey`/`Token`
// field — privacy CI invariant (WP14) blocks any such field at lint.
type SecretReference struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"`
	CreatedAt string `json:"createdAt"`
}

// TrustAPI is the view-scoped accessor for trust posture + secret
// references. Resolution is intentionally absent.
type TrustAPI interface {
	ListSecretReferences(ctx context.Context) ([]SecretReference, error)
	GetSecretReference(ctx context.Context, id string) (SecretReference, error)
}
