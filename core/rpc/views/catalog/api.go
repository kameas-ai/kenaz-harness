// Package catalog is the view-scoped RPC surface for catalog publish/list/
// install/uninstall (fleet-share-and-sync-01NDFSEX14 WP02).
//
// The Capabilities surface's fleet-catalog browse (frontend
// views/capabilities/catalogBrowse.ts — the Marketplace view folded into it
// in install-framework-01DOGF0B Phase 4) and per-kind Publish dialogs bind
// here. Fork-removal recipe: delete this package + the catalog browse in
// views/capabilities/ + views/catalog/PublishDialog.vue + per-kind
// "Publish to team" buttons.
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
	// Installed is per kind (install-framework-01DOGF0B WP01): for
	// kind=skill it means the skill is in the skill store (live-registered);
	// for every other kind it means a downloaded payload sits under
	// <dataDir>/installed/ — residue nothing consumes, which the UI offers
	// to remove but does not call "installed" (see docs/unwired-ledger.md).
	Installed bool `json:"installed"`
	// Lifecycle / LifecycleReason / SupersededBy: the version's org
	// lifecycle from the unsigned catalog wire (skill-library-01SKLIB01
	// WP02); "" = active / pre-0114 fleet.
	Lifecycle       string `json:"lifecycle,omitempty"`
	LifecycleReason string `json:"lifecycle_reason,omitempty"`
	SupersededBy    string `json:"superseded_by,omitempty"`
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

	// Install refuses every kind with fleet.ErrCatalogKindNotInstallable,
	// naming the kind and the working alternative, and writes nothing
	// (install-framework-01DOGF0B WP02). It used to write an opaque
	// payload under <DataDir>/installed/ that no runtime consumer reads —
	// a badge-only install (docs/unwired-ledger.md). Each kind gets a
	// consumed install through the provider framework (WP05–WP07).
	// Signature verification (register C-2) is still unimplemented and
	// moot here until a kind installs again.
	Catalog_Install(ctx context.Context, catalogID, version string) error

	// Uninstall removes the local installed/<kind>/<id>@<version>/
	// directory — residue a pre-WP02 release's Install left. There is
	// nothing to unregister: nothing ever registered it.
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
