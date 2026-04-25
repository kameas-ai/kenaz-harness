// Package bundle defines the BundleAPI view-scoped accessor.
package bundle

import "context"

// Bundle is reference-only bundle metadata.
type Bundle struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Tier    string `json:"tier"`
}

// BundleAPI is the view-scoped accessor for bundle listing/pinning.
type BundleAPI interface {
	List(ctx context.Context) ([]Bundle, error)
	Get(ctx context.Context, id string) (Bundle, error)
}
