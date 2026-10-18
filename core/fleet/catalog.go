// Package fleet — catalog.go
//
// Catalog publish / list / install / uninstall for workflows, agent packs,
// and bundles. Wire contract: §2a of kenaz-fleet/docs/contract-harness-sync.md.
//
// Fork-removal recipe: delete this file + core/fleet/catalog_install.go +
// core/rpc/views/catalog/ + frontend/src/views/catalog/ (PublishDialog) +
// the catalog browse in frontend/src/views/capabilities/ (catalogBrowse.ts,
// CatalogListingDetail.vue) + the "Publish to team" menu items on
// workflow/pack/bundle views.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
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
	CatalogKindWorkflow CatalogItemKind = "workflow"
	// CatalogKindPack is fleet's catalog kind for an agent pack. The wire
	// string is "pack" (fleet service/handlers_catalog.go catalogKinds; DB
	// CHECK in migration 0061); the harness's capability kind stays
	// "agent_pack" (install.KindAgentPack, the UI's "Agent packs") and is
	// translated at the catalog boundary (CatalogKindForCapability /
	// CapabilityKindForCatalog). Sending "agent_pack" got 400 invalid_kind.
	CatalogKindPack   CatalogItemKind = "pack"
	CatalogKindBundle CatalogItemKind = "bundle"
	// CatalogKindSkill is the catalog kind for user slash-command skills
	// (fleet-skills-sync-01NDFSEX18). The wire contract is identical to
	// other catalog kinds (§2a + §3 of contract-harness-sync.md); only
	// the payload encoding differs (slashcmd.Skill JSON, ≤ 256KB).
	CatalogKindSkill CatalogItemKind = "skill"
)

// capabilityKindAgentPack is the harness capability kind for an agent pack
// (install.KindAgentPack — not imported to keep core/fleet free of
// core/install).
const capabilityKindAgentPack = "agent_pack"

// CatalogKindForCapability maps a harness capability kind to fleet's catalog
// kind ("agent_pack" → "pack"; the others are identical strings).
func CatalogKindForCapability(kind string) CatalogItemKind {
	if kind == capabilityKindAgentPack {
		return CatalogKindPack
	}
	return CatalogItemKind(kind)
}

// CapabilityKindForCatalog maps fleet's catalog kind to the harness
// capability kind ("pack" → "agent_pack").
func CapabilityKindForCatalog(kind CatalogItemKind) string {
	if kind == CatalogKindPack {
		return capabilityKindAgentPack
	}
	return string(kind)
}

// CatalogVisibility controls who can see a catalog item.
type CatalogVisibility string

const (
	CatalogVisPrivate   CatalogVisibility = "private"
	CatalogVisTeam      CatalogVisibility = "team"
	CatalogVisOrgPublic CatalogVisibility = "org_public"
)

// CatalogItem is one published item returned by List or fetch, or used in
// publish round-trips. PayloadBytes is the raw (opaque) content; Signature is
// the base64-encoded ed25519 signature produced by the publishing device's
// DeviceSigner. Both fields may be empty on List responses (metadata-only
// variant).
//
// Decoded from fleet's CatalogItemMetaAPI (list items) and
// CatalogFetchResponse (fetch), which key the id as "id"
// (service/handlers_catalog.go:115-157). "catalog_id" is ONLY the publish
// response's key (publishResponse below) — reading it here left every
// listed / fetched item with an empty ID (audit §0-C).
type CatalogItem struct {
	ID          string            `json:"id"`
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

	// Lifecycle is the version's org lifecycle (skill-library-01SKLIB01
	// WP01, fleet migrations 0114-0116): "active" | "deprecated" |
	// "revoked". Carried by BOTH the list and the fetch response — the
	// UNSIGNED catalog wire only; it never enters the signed config bundle
	// (fleet §5.4 rule, ruling OQ-5 = H4a). Empty means a pre-0114 fleet:
	// treat as active (EffectiveLifecycle). An unknown value is preserved
	// verbatim for display, never an error (forward compatibility).
	Lifecycle string `json:"lifecycle,omitempty"`
	// LifecycleReason is the admin's reason for a deprecate/revoke.
	// Additive forward-compat only: fleet's live list and fetch responses
	// do NOT carry it today (only GET /catalog/entries/{kind}/{slug} does);
	// decoded here so a later fleet that adds it to list/fetch reaches the
	// UI without a harness change.
	LifecycleReason string `json:"lifecycle_reason,omitempty"`
	// SupersededBy is the catalog id of a newer version of the same entry
	// the org points deprecated users at (optional).
	SupersededBy string `json:"superseded_by,omitempty"`
	// RevokedAt is set on list rows whose lifecycle is "revoked".
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Catalog lifecycle states (fleet service/lookups_catalog_library.go).
const (
	CatalogLifecycleActive     = "active"
	CatalogLifecycleDeprecated = "deprecated"
	CatalogLifecycleRevoked    = "revoked"
)

// EffectiveLifecycle is the item's lifecycle with a pre-0114 fleet's
// missing key read as "active". Unknown values pass through unchanged.
func (c CatalogItem) EffectiveLifecycle() string {
	if c.Lifecycle == "" {
		return CatalogLifecycleActive
	}
	return c.Lifecycle
}

// IsRevoked reports an EXPLICIT lifecycle=revoked. Absence of the key, an
// unknown value, or absence of the row are never revocation (WP03's
// safety rule).
func (c CatalogItem) IsRevoked() bool { return c.Lifecycle == CatalogLifecycleRevoked }

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

// ErrCatalogForbidden is returned by Unpublish on a 403: the caller is
// neither the item's owner nor a fleet admin. Distinct from
// ErrCatalogNotInTier — a 403 on DELETE means "not the owner and not an
// admin", not "requires a higher tier" (fleet-enforcement-truth-01PMZ505
// WP11, register C-3/C-8). Before this, Unpublish's 403 mapped to
// ErrCatalogNotInTier, which would have told a publisher trying to
// withdraw someone else's item to upgrade their subscription.
var ErrCatalogForbidden = fmt.Errorf("fleet/catalog: not the item's owner or a fleet admin")

// ErrCatalogSignatureMismatch is returned by Install when signature
// verification fails.
var ErrCatalogSignatureMismatch = fmt.Errorf("fleet/catalog: payload signature mismatch")

// ErrCatalogPayloadTooLarge is returned by Publish when the payload exceeds
// the 10MB limit.
var ErrCatalogPayloadTooLarge = fmt.Errorf("fleet/catalog: payload exceeds 10MB limit")

const catalogMaxPayloadBytes = 10 * 1024 * 1024 // 10MB (NFR-001)

// ErrCatalogItemRevoked is returned by FetchCatalogItem when fleet answers
// 410 item_revoked: the org revoked this version and it can no longer be
// installed (skill-library-01SKLIB01 WP01, fleet S3). Its message is the
// user-facing copy the install surfaces show; it is terminal — never
// retried.
var ErrCatalogItemRevoked = errors.New("this version was revoked by your org and can no longer be installed")

// catalogCodeItemRevoked is fleet's error code on the 410 (httpcore
// ErrorResponse {"code","message","details"}).
const catalogCodeItemRevoked = "item_revoked"

// CatalogStatusError is a non-success HTTP answer from a catalog list or
// fetch, with fleet's error code when the body carried one. Its message is
// the same "fleet/catalog: <op>: status N: <body>" text these paths always
// returned; callers that must tell a tier lapse (403) or a missing route
// (404) from a transient failure use errors.As.
type CatalogStatusError struct {
	Op     string // "list" or "fetch <id>@<ver>"
	Status int
	Code   string // fleet ErrorResponse.code, "" when absent
	Body   string
}

func (e *CatalogStatusError) Error() string {
	return fmt.Sprintf("fleet/catalog: %s: status %d: %s", e.Op, e.Status, e.Body)
}

// newCatalogStatusError reads (a bounded prefix of) the body and fleet's
// {"code": ...} out of it.
func newCatalogStatusError(op string, resp *http.Response) *CatalogStatusError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	e := &CatalogStatusError{Op: op, Status: resp.StatusCode, Body: string(body)}
	var env struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &env) == nil {
		e.Code = env.Code
	}
	return e
}

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

// catalogListResponse is fleet's CatalogListResponse envelope
// (service/handlers_catalog.go:110-112) — {"items": [...]}, never a bare
// array.
type catalogListResponse struct {
	Items []CatalogItem `json:"items"`
}

// List fetches catalog metadata from GET /api/v1/catalog/list.
// Only metadata is returned (no PayloadBytes).
//
// It NEVER sends fleet's optional ?lifecycle= filter: fleet's no-param
// default includes REVOKED rows (with lifecycle:"revoked", never a payload),
// and the WP03 revocation sweep depends on seeing them — a filtered list
// would make a revoked install indistinguishable from an absent row, which
// the sweep must never act on (research/fleet-answers-2026-10-06, OQ-2).
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
		return nil, newCatalogStatusError("list", resp)
	}
	var lr catalogListResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("fleet/catalog: list: decode: %w", err)
	}
	if lr.Items == nil {
		lr.Items = []CatalogItem{}
	}
	return lr.Items, nil
}

// Unpublish removes a catalog item via DELETE /api/v1/catalog/{id}.
// Only the item's owner or a fleet admin can unpublish.
func (c *Client) Unpublish(ctx context.Context, catalogID string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	resp, err := c.Delete(ctx, "/api/v1/catalog/"+url.PathEscape(catalogID))
	if err != nil {
		return fmt.Errorf("fleet/catalog: unpublish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		// C-8: on DELETE, 403 means "not the owner and not an admin" —
		// a different fact than ErrCatalogNotInTier's "needs a higher
		// tier" (which Publish's 403 correctly means, and is left alone).
		return ErrCatalogForbidden
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/catalog: unpublish: status %d: %s", resp.StatusCode, body)
	}
	return nil
}
