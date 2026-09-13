// Package catalog is the view-scoped RPC surface for catalog publish/list/
// install/uninstall (fleet-share-and-sync-01NDFSEX14 WP02).
//
// The frontend Marketplace view and per-kind Publish dialogs bind here.
// Fork-removal recipe: delete this package + MarketplaceView.vue +
// PublishDialog.vue + per-kind "Publish to team" buttons.
package catalog

import "context"

// CatalogItemView is the wire-safe shape returned to the frontend.
type CatalogItemView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	PublishedAt string `json:"published_at,omitempty"` // RFC3339
	Installed   bool   `json:"installed"`
}

// PublishInput is the form the frontend submits when publishing an item.
type PublishInput struct {
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	// PayloadJSON is the serialized workflow / pack / bundle content.
	// HARD RULE: must not contain credential bytes; validated at the
	// credential-hygiene CI gate.
	PayloadJSON string `json:"payload_json"`
}

// CatalogFilter is the frontend filter shape.
type CatalogFilter struct {
	Kind       string `json:"kind,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// CatalogAPI is the RPC surface exposed to the Wails frontend.
type CatalogAPI interface {
	// Publish signs and uploads a catalog item.
	// Requires capability catalog_publish (Team+ for team/org_public,
	// Pro for private).
	Catalog_Publish(ctx context.Context, input PublishInput) (CatalogItemView, error)

	// List returns catalog items matching the filter (metadata only).
	Catalog_List(ctx context.Context, filter CatalogFilter) ([]CatalogItemView, error)

	// Install downloads the item and extracts it to
	// <DataDir>/installed/<kind>/<id>@<version>/. Signature verification
	// is SKIPPED today (fleet-enforcement-truth-01PMZ505 WP10, register
	// C-2, 2026-08-19, owner alec) — no per-device catalog signing key
	// source exists in or out of this repo. See
	// core/rpc/views/catalog/impl.go's pubKeyBase64 doc and
	// docs/unwired-ledger.md.
	Catalog_Install(ctx context.Context, catalogID, version string) error

	// Uninstall removes the local install directory and unregisters
	// the item.
	Catalog_Uninstall(ctx context.Context, kind, catalogID, version string) error

	// Installed returns locally installed catalog items.
	Catalog_Installed(ctx context.Context) ([]CatalogItemView, error)

	// Unpublish withdraws catalogID from the org listing. Distinct from
	// Uninstall: this removes the org-visible listing on the server;
	// Uninstall removes only the local copy. Server-authorized — the
	// item's owner or a fleet admin may withdraw it
	// (core/fleet/catalog.go's Unpublish doc); the harness has no
	// publisher field to evaluate that rule locally and does not
	// attempt to (register C-3/C-8). Returns fleet.ErrCatalogForbidden
	// on a 403, distinct from fleet.ErrCatalogNotInTier.
	//
	// (fleet-enforcement-truth-01PMZ505 WP11.)
	Catalog_Unpublish(ctx context.Context, catalogID string) error
}
