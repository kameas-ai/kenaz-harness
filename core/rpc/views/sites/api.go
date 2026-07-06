// Package sites provides the view-scoped RPC surface for fleet-hosted sites
// (sites-ui-01NSITE06). The five methods are thin shells over the same
// core/sites packager and core/fleet/sites.go client used by the MCP server.
//
// Error contract (FR-005):
//   - ErrFleetDisabled, ErrNotSignedIn, ErrCapabilityNotInTier are returned
//     as typed sentinels so the frontend can render contextual messages without
//     string matching.
//   - All other errors are wrapped with descriptive context.
//
// Deploy progress events (FR-002 / NFR-003):
//   - Emitted on topic "sites:deploy:progress" via the existing broker.Publish
//     pattern. Each event carries a DeployProgressEvent payload.
//
// Mission: sites-ui-01NSITE06.
package sites

import (
	"context"
	"time"
)

// SiteSummary is the wire shape returned by Sites_List (FR-001).
// URL is taken from the Fleet API response — never constructed locally.
type SiteSummary struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"` // "static" | "dynamic"
	URL        string    `json:"url,omitempty"`
	Status     string    `json:"status"` // "live" | "building" | "deploying" | "failed" | "unknown"
	DeployedAt time.Time `json:"deployedAt,omitempty"`
}

// DeployProgressEvent is the payload emitted on "sites:deploy:progress" during
// Sites_Deploy (FR-002 / NFR-003). stage cycles through:
//
//	"validating" → "packing" → "uploading" → "waiting" → "done" | "error"
type DeployProgressEvent struct {
	Stage   string `json:"stage"`   // one of the values above
	Message string `json:"message"` // human-readable status line
	URL     string `json:"url,omitempty"` // populated on stage="done"
}

// SitesAPI is the Wails-bound RPC surface for the Sites view.
// Implementations are constructed via New and registered in core/rpc/api.go.
type SitesAPI interface {
	// Sites_List returns the current org's sites as SiteSummary records.
	// Returns ErrFleetDisabled, ErrNotSignedIn, or ErrCapabilityNotInTier
	// when the precondition is not met.
	Sites_List(ctx context.Context) ([]SiteSummary, error)

	// Sites_Deploy validates the manifest in rootDir, packs it, uploads to
	// Fleet, and (by default) polls until the deployment reaches a terminal
	// state. Progress events are emitted on "sites:deploy:progress". The
	// recent-deploy list is updated on success.
	// Returns ErrFleetDisabled, ErrNotSignedIn, or ErrCapabilityNotInTier
	// on precondition failure; other errors are wrapped fleet/pack errors.
	Sites_Deploy(ctx context.Context, rootDir string) (SiteSummary, error)

	// Sites_Status returns the current state of a single site by name/slug.
	Sites_Status(ctx context.Context, site string) (SiteSummary, error)

	// Sites_Logs returns up to tailLines lines of build+runtime log for the
	// given site. tailLines is clamped to 2000 (contract maximum, FR-003).
	// Returns ErrSiteNotFound (wrapped) for static sites (server 404).
	Sites_Logs(ctx context.Context, site string, tailLines int) (string, error)

	// Sites_Delete deletes the named site. The frontend enforces a two-step
	// confirm dialog before calling this method (FR-004); the RPC does not
	// re-verify a bool guard.
	Sites_Delete(ctx context.Context, site string) error
}
