// Package fleet — catalog.go
//
// Catalog publish / list / install / uninstall for workflows, agent packs,
// and bundles. Wire contract: §2a of kenaz-fleet/docs/contract-harness-sync.md.
//
// Fork-removal recipe: delete this file + core/fleet/catalog_install.go +
// core/rpc/views/catalog/ + frontend/src/views/marketplace/ + the
// "Publish to team" menu items on workflow/pack/bundle views.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ── types ────────────────────────────────────────────────────────────────────

// CatalogItemKind enumerates the content kinds that can be published to the
// catalog. Values match the wire key (lowercase).
type CatalogItemKind string

const (
	CatalogKindWorkflow  CatalogItemKind = "workflow"
	CatalogKindAgentPack CatalogItemKind = "agent_pack"
	CatalogKindBundle    CatalogItemKind = "bundle"
)

// CatalogVisibility controls who can see a catalog item.
type CatalogVisibility string

const (
	CatalogVisPrivate   CatalogVisibility = "private"
	CatalogVisTeam      CatalogVisibility = "team"
	CatalogVisOrgPublic CatalogVisibility = "org_public"
)

// CatalogItem is one published item returned by List or used in publish
// round-trips. PayloadBytes is the raw (opaque) content; Signature is the
// base64-encoded ed25519 signature produced by the publishing device's
// DeviceSigner. Both fields may be empty on List responses (metadata-only
// variant).
type CatalogItem struct {
	ID          string            `json:"catalog_id"`
	Kind        CatalogItemKind   `json:"kind"`
	Slug        string            `json:"slug"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Visibility  CatalogVisibility `json:"visibility"`
	// PayloadBytes is set on publish requests and on the full fetch
	// (GET /api/v1/catalog/{id}@{ver}). It is NOT returned on List.
	PayloadBytes []byte `json:"payload,omitempty"`
	// Signature is the base64 ed25519 signature over PayloadBytes,
	// produced by the publishing device's key.
	Signature   string    `json:"signature,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// CatalogFilter controls which items List returns.
type CatalogFilter struct {
	Kind       CatalogItemKind   `json:"kind,omitempty"`
	Visibility CatalogVisibility `json:"visibility,omitempty"`
	OrgID      string            `json:"org,omitempty"`
}

// publishRequest is the JSON body for POST /api/v1/catalog/publish.
type publishRequest struct {
	Kind        CatalogItemKind   `json:"kind"`
	Slug        string            `json:"slug"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Visibility  CatalogVisibility `json:"visibility"`
	Payload     []byte            `json:"payload"`
	Signature   string            `json:"signature"`
	PubkeyFP    string            `json:"pubkey_fp"`
}

// publishResponse is the JSON shape returned by POST /api/v1/catalog/publish.
type publishResponse struct {
	CatalogID   string    `json:"catalog_id"`
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
}

// ── errors ────────────────────────────────────────────────────────────────────

// ErrCatalogNotInTier is returned when the visibility scope requested requires
// a higher tier than the user's current subscription.
var ErrCatalogNotInTier = fmt.Errorf("fleet/catalog: capability not available in current tier")

// ErrCatalogSignatureMismatch is returned by Install when signature
// verification fails.
var ErrCatalogSignatureMismatch = fmt.Errorf("fleet/catalog: payload signature mismatch")

// ErrCatalogPayloadTooLarge is returned by Publish when the payload exceeds
// the 10MB limit.
var ErrCatalogPayloadTooLarge = fmt.Errorf("fleet/catalog: payload exceeds 10MB limit")

const catalogMaxPayloadBytes = 10 * 1024 * 1024 // 10MB (NFR-001)

// ── Catalog client methods ───────────────────────────────────────────────────

// Publish serialises the payload, signs it with the device key from dataDir,
// and POSTs to /api/v1/catalog/publish. Returns the assigned CatalogItem
// (with ID and PublishedAt populated from the server response).
//
// HARD RULE: PayloadBytes is treated as an opaque blob. The caller is
// responsible for ensuring it contains no plaintext credentials.
func (c *Client) Publish(
	ctx context.Context,
	signer *DeviceSigner,
	kind CatalogItemKind,
	slug, version, description string,
	visibility CatalogVisibility,
	payload []byte,
) (CatalogItem, error) {
	if c == nil || c.isNop {
		return CatalogItem{}, ErrFleetDisabled
	}
	if len(payload) > catalogMaxPayloadBytes {
		return CatalogItem{}, ErrCatalogPayloadTooLarge
	}
	sig, fp, err := signer.Sign(payload)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/catalog: sign payload: %w", err)
	}

	req := publishRequest{
		Kind:        kind,
		Slug:        slug,
		Version:     version,
		Description: description,
		Visibility:  visibility,
		Payload:     payload,
		Signature:   sig,
		PubkeyFP:    fp,
	}
	resp, err := c.PostJSON(ctx, "/api/v1/catalog/publish", req)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/catalog: publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return CatalogItem{}, ErrCatalogNotInTier
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return CatalogItem{}, fmt.Errorf("fleet/catalog: publish: status %d: %s", resp.StatusCode, body)
	}
	var pr publishResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/catalog: publish: decode response: %w", err)
	}
	return CatalogItem{
		ID:          pr.CatalogID,
		Kind:        kind,
		Slug:        slug,
		Version:     pr.Version,
		Description: description,
		Visibility:  visibility,
		Signature:   sig,
		PublishedAt: pr.PublishedAt,
	}, nil
}

// List fetches catalog metadata from GET /api/v1/catalog/list.
// Only metadata is returned (no PayloadBytes).
func (c *Client) List(ctx context.Context, filters CatalogFilter) ([]CatalogItem, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}
	q := url.Values{}
	if filters.OrgID != "" {
		q.Set("org", filters.OrgID)
	}
	if filters.Kind != "" {
		q.Set("kind", string(filters.Kind))
	}
	if filters.Visibility != "" {
		q.Set("visibility", string(filters.Visibility))
	}
	path := "/api/v1/catalog/list"
	if len(q) > 0 {
		path = path + "?" + q.Encode()
	}
	resp, err := c.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fleet/catalog: list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet/catalog: list: status %d: %s", resp.StatusCode, body)
	}
	var items []CatalogItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("fleet/catalog: list: decode: %w", err)
	}
	return items, nil
}

// Unpublish removes a catalog item via DELETE /api/v1/catalog/{id}.
// Only the item's owner or a fleet admin can unpublish.
func (c *Client) Unpublish(ctx context.Context, catalogID string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	resp, err := c.Delete(ctx, "/api/v1/catalog/"+catalogID)
	if err != nil {
		return fmt.Errorf("fleet/catalog: unpublish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return ErrCatalogNotInTier
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/catalog: unpublish: status %d: %s", resp.StatusCode, body)
	}
	return nil
}
