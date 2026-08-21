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

// InstallRequest is the parameter shape for a bundle install
// (bundle-download-and-verify-01PMZ909). Kind selects the channel:
// "local_path" reads Path; "http_mirror" (UNIT-6; not reachable in
// production until UNIT-7 registers its factory — see
// core/rpc/api.go) reads URL. Exactly one of Path / URL is meaningful
// per Kind; the unused field is ignored by that channel's Factory.
type InstallRequest struct {
	// Kind identifies the channel kind ("local_path", "http_mirror",
	// …). Registry.Open refuses any kind with no registered factory.
	Kind string `json:"kind"`
	// Path is the absolute filesystem path to a directory that contains
	// a kenaz.yaml manifest at its root. Used by local_path.
	Path string `json:"path"`
	// URL is the mirror root a network channel fetches from. Used by
	// http_mirror (UNIT-6).
	URL string `json:"url,omitempty"`
}

// BundleAPI is the view-scoped accessor for bundle listing/pinning.
type BundleAPI interface {
	List(ctx context.Context) ([]Bundle, error)
	Get(ctx context.Context, id string) (Bundle, error)
	// Install fetches a bundle through the channel named by req.Kind —
	// see InstallRequest — verifies its signatures and every artifact's
	// content hash, and registers it in the lockfile only after every
	// fetch has succeeded.
	Install(ctx context.Context, req InstallRequest) (Bundle, error)
	// Remove drops the bundle whose name matches id from the lockfile.
	// Removing a missing bundle is a no-op (returns nil).
	Remove(ctx context.Context, id string) error
}
