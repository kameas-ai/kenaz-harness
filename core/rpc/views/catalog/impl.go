package catalog

import (
	"context"
	"fmt"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// API implements CatalogAPI backed by fleet.Client.
type API struct {
	client  *corefleet.Client
	signer  *corefleet.DeviceSigner
	dataDir string
	// pubKeyBase64 is the fleet-issued signing public key for install
	// verification. Empty until the server-side per-device key lookup
	// is implemented; verification is skipped when empty.
	pubKeyBase64 string
	// emitter is optional; nil emitter means audit events are silently dropped.
	emitter auditEmitter
}

// auditEmitter is the minimal interface the catalog RPC needs.
type auditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

var _ CatalogAPI = (*API)(nil)

// NewAPI constructs a CatalogAPI backed by the given fleet client.
// signer and dataDir are required for publish and install operations.
func NewAPI(client *corefleet.Client, signer *corefleet.DeviceSigner, dataDir string) *API {
	return &API{client: client, signer: signer, dataDir: dataDir}
}

// WithEmitter sets the audit emitter.
func (a *API) WithEmitter(em auditEmitter) *API {
	a.emitter = em
	return a
}

// WithPubKey sets the fleet-level signing public key for install verification.
func (a *API) WithPubKey(pubKeyBase64 string) *API {
	a.pubKeyBase64 = pubKeyBase64
	return a
}

// Catalog_Publish implements CatalogAPI.
func (a *API) Catalog_Publish(ctx context.Context, input PublishInput) (CatalogItemView, error) {
	if a.client == nil {
		return CatalogItemView{}, corefleet.ErrFleetDisabled
	}
	if a.signer == nil {
		return CatalogItemView{}, fmt.Errorf("catalog: signer not configured")
	}
	kind := corefleet.CatalogItemKind(input.Kind)
	vis := corefleet.CatalogVisibility(input.Visibility)
	payload := []byte(input.PayloadJSON)

	item, err := a.client.Publish(ctx, a.signer, kind, input.Slug, input.Version,
		input.Description, vis, payload)
	if err != nil {
		return CatalogItemView{}, err
	}

	// Emit audit event.
	if a.emitter != nil {
		_ = a.emitter.EmitFleetEvent(ctx, contextaudit.KindFleetCatalogPublished,
			contextaudit.FleetCatalogPublishedPayload{
				CatalogID: item.ID,
				Kind:      string(item.Kind),
				Slug:      item.Slug,
				Version:   item.Version,
			})
	}

	return catalogItemToView(item, false), nil
}

// Catalog_List implements CatalogAPI.
func (a *API) Catalog_List(ctx context.Context, filter CatalogFilter) ([]CatalogItemView, error) {
	if a.client == nil {
		return nil, corefleet.ErrFleetDisabled
	}
	items, err := a.client.List(ctx, corefleet.CatalogFilter{
		Kind:       corefleet.CatalogItemKind(filter.Kind),
		Visibility: corefleet.CatalogVisibility(filter.Visibility),
	})
	if err != nil {
		return nil, err
	}

	// Compute installed set for badge.
	installedSet := map[string]bool{}
	if a.dataDir != "" {
		if installed, err := corefleet.InstalledItems(a.dataDir); err == nil {
			for _, it := range installed {
				installedSet[it.ID+"@"+it.Version] = true
			}
		}
	}

	out := make([]CatalogItemView, len(items))
	for i, it := range items {
		out[i] = catalogItemToView(it, installedSet[it.ID+"@"+it.Version])
	}
	return out, nil
}

// Catalog_Install implements CatalogAPI.
func (a *API) Catalog_Install(ctx context.Context, catalogID, version string) error {
	if a.client == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.client.Install(ctx, a.dataDir, a.pubKeyBase64, catalogID, version)
}

// Catalog_Uninstall implements CatalogAPI.
func (a *API) Catalog_Uninstall(_ context.Context, kind, catalogID, version string) error {
	return a.client.Uninstall(a.dataDir, corefleet.CatalogItemKind(kind), catalogID, version)
}

// Catalog_Installed implements CatalogAPI.
func (a *API) Catalog_Installed(_ context.Context) ([]CatalogItemView, error) {
	if a.dataDir == "" {
		return nil, nil
	}
	items, err := corefleet.InstalledItems(a.dataDir)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItemView, len(items))
	for i, it := range items {
		out[i] = catalogItemToView(it, true)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func catalogItemToView(it corefleet.CatalogItem, installed bool) CatalogItemView {
	v := CatalogItemView{
		ID:          it.ID,
		Kind:        string(it.Kind),
		Slug:        it.Slug,
		Version:     it.Version,
		Description: it.Description,
		Visibility:  string(it.Visibility),
		Installed:   installed,
	}
	if !it.PublishedAt.IsZero() {
		v.PublishedAt = it.PublishedAt.UTC().Format(time.RFC3339)
	}
	return v
}
