// Package bundle defines the BundleAPI view-scoped accessor.
package bundle

import "context"

// Bundle is reference-only bundle metadata.
type Bundle struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Tier          string     `json:"tier"`
	Source        string     `json:"source,omitempty"`
	Signature     string     `json:"signature,omitempty"`
	ArtifactCount int        `json:"artifactCount"`
	Artifacts     []Artifact `json:"artifacts,omitempty"`
}

// Artifact is a named entry inside a bundle. Reference-only — no
// payload bytes ever traverse the rpc surface.
type Artifact struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	ContentHash string `json:"contentHash"`
}

// BundleAPI is the view-scoped accessor for bundle listing/pinning.
type BundleAPI interface {
	List(ctx context.Context) ([]Bundle, error)
	Get(ctx context.Context, id string) (Bundle, error)
}
