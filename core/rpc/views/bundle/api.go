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

// InstallRequest is the parameter shape for a localpath bundle install.
// Beta scope (v0.3.0): only the local_path channel is supported here.
// The resolver mission will replace this minimal install path with the
// full multi-channel pipeline.
type InstallRequest struct {
	// Kind identifies the channel kind. Must be "local_path" for v0.3.0.
	Kind string `json:"kind"`
	// Path is the absolute filesystem path to a directory that contains
	// a kaneaz.yaml manifest at its root.
	Path string `json:"path"`
}

// BundleAPI is the view-scoped accessor for bundle listing/pinning.
type BundleAPI interface {
	List(ctx context.Context) ([]Bundle, error)
	Get(ctx context.Context, id string) (Bundle, error)
	// Install registers a bundle in the lockfile by reading + validating
	// a kaneaz.yaml manifest at req.Path. Beta-scoped to local_path; the
	// resolver mission owns the production multi-channel pipeline.
	Install(ctx context.Context, req InstallRequest) (Bundle, error)
	// Remove drops the bundle whose name matches id from the lockfile.
	// Removing a missing bundle is a no-op (returns nil).
	Remove(ctx context.Context, id string) error
}
