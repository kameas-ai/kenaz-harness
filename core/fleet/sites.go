// Package fleet — sites.go
//
// Client methods for the Fleet Sites API (/api/v1/sites/*).
// Wire contract: kenaz-fleet/docs/contract-harness-sites.md §1 + §5.
//
// All methods guard on isNop: when HARNESS_FLEET_DISABLED=1 is active
// (or in tests using NewClient with the flag set), every method returns
// ErrFleetDisabled immediately.
//
// SiteDeploy bypasses the shared do() helper because bundle uploads may be
// hundreds of MB — the helper buffers the entire body for 5xx retry, which
// is not acceptable for streaming. Instead, SiteDeploy builds a single
// one-shot request with a context timeout long enough for large files.
// The server's identical-sha256 short-circuit makes retrying unnecessary in
// the common case: if the upload succeeds but the response is lost, the
// caller can re-upload and the server returns the existing deployment.
//
// Mission: sites-foundation-01NSITE04, WP05.
package fleet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sites API sentinel errors. Callers may use errors.Is to distinguish them.
var (
	// ErrSlugConflict is returned when the chosen site slug is already taken
	// within the org (409 slug_conflict from the Fleet API).
	ErrSlugConflict = errors.New("fleet/sites: site slug is already taken in this org")

	// ErrOrgSlugRequired is returned when the org slug has not yet been set
	// (422 org_slug_required). Set it with PUT /api/v1/orgs/{org_id}/slug first.
	ErrOrgSlugRequired = errors.New("fleet/sites: org slug is required but not yet set")

	// ErrInvalidSlug is returned for a slug that violates the grammar or is
	// reserved (422 invalid_slug).
	ErrInvalidSlug = errors.New("fleet/sites: site slug is invalid or reserved")

	// ErrBundleTooLarge is returned when the bundle exceeds the server-side
	// size or file-count caps (413 bundle_too_large).
	ErrBundleTooLarge = errors.New("fleet/sites: bundle too large or too many files")

	// ErrBundleDigestMismatch is returned when the sha256 in the
	// X-Kameas-Bundle-Sha256 header does not match the server's computed
	// digest of the uploaded bytes (422 bundle_digest_mismatch).
	ErrBundleDigestMismatch = errors.New("fleet/sites: bundle sha256 mismatch")

	// ErrQuotaExceeded is returned when the org has reached a resource quota
	// (429 quota_exceeded). The error message includes the quota name.
	ErrQuotaExceeded = errors.New("fleet/sites: quota exceeded")

	// ErrSiteNotFound is returned for 404 responses on site operations.
	ErrSiteNotFound = errors.New("fleet/sites: site not found")
)

// sitesUploadTimeout is the per-call context timeout for SiteDeploy.
// Large static bundles (≤512 MiB) and dynamic source bundles (≤64 MiB)
// may take significant time to upload. The default Client httpTimeout (30s)
// is too short; we use a generous upload timeout here instead.
// This value is intentionally not a quota constant — the server enforces
// caps; we just need enough time to stream the bytes.
const sitesUploadTimeout = 10 * time.Minute

// ----- wire types -----

// SiteRecord is the fleet API representation of a site.
type SiteRecord struct {
	ID             string     `json:"id"`
	Slug           string     `json:"slug"`
	Kind           string     `json:"kind"`
	OrgID          string     `json:"org_id"`
	ActiveDeployID string     `json:"active_deployment_id,omitempty"`
	URL            string     `json:"url,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// DeploymentRecord is the fleet API representation of a deployment.
type DeploymentRecord struct {
	ID         string    `json:"id"`
	SiteID     string    `json:"site_id"`
	Status     string    `json:"status"` // uploading | building | deploying | live | failed
	URL        string    `json:"url,omitempty"`
	SHA256Hex  string    `json:"sha256,omitempty"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SiteEnvEntry is one env var name + metadata (never a value).
type SiteEnvEntry struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SetAt       time.Time `json:"set_at,omitempty"`
}

// siteErrorEnvelope matches the Fleet API error response shape.
type siteErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ----- SiteCreate -----

// SiteCreate creates a new site record within the signed-in org.
// Returns ErrFleetDisabled on nop clients; ErrSlugConflict on 409;
// ErrCapabilityNotInTier on 403; ErrOrgSlugRequired / ErrInvalidSlug on 422.
func (c *Client) SiteCreate(ctx context.Context, slug, kind string) (SiteRecord, error) {
	if c == nil || c.isNop {
		return SiteRecord{}, ErrFleetDisabled
	}

	body := struct {
		Slug string `json:"slug"`
		Kind string `json:"kind"`
	}{Slug: slug, Kind: kind}

	resp, err := c.PostJSON(ctx, "/api/v1/sites", body)
	if err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: create %q: %w", slug, err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: create %q: %w", slug, err)
	}

	var rec SiteRecord
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: create %q: decode response: %w", slug, err)
	}
	return rec, nil
}

// ----- SiteDeploy -----

// SiteDeploy streams bundle (a gzip tarball) to the Fleet API as a new
// deployment for siteID. sha256Hex must be the hex-encoded sha256 of the
// compressed bytes about to be streamed; manifestJSON is the raw
// kameas-site.json bytes (base64-encoded in the X-Kameas-Manifest header
// per the wire contract §1).
//
// SiteDeploy uses a per-call upload timeout (sitesUploadTimeout) rather than
// the default Client.httpTimeout, because large bundles require more time.
//
// Identical-sha256 short-circuit: if the server has already seen this bundle,
// it returns the existing live deployment (200). The caller should treat this
// as success (no special handling needed per spec FR-302).
func (c *Client) SiteDeploy(
	ctx context.Context,
	siteID string,
	manifestJSON []byte,
	bundle io.Reader,
	sha256Hex string,
) (DeploymentRecord, error) {
	if c == nil || c.isNop {
		return DeploymentRecord{}, ErrFleetDisabled
	}
	if !c.profile.Configured() {
		return DeploymentRecord{}, ErrProfileNotConfigured
	}

	ts, err := LoadTokens()
	if err != nil {
		return DeploymentRecord{}, ErrNotSignedIn
	}

	reqURL, err := c.APIURL(ctx, "/api/v1/sites/"+siteID+"/deployments")
	if err != nil {
		return DeploymentRecord{}, err
	}

	// Use a fresh context with the upload timeout; honour parent cancellation.
	uploadCtx, cancel := context.WithTimeout(ctx, sitesUploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(uploadCtx, http.MethodPost, reqURL, bundle)
	if err != nil {
		return DeploymentRecord{}, fmt.Errorf("fleet/sites: deploy %q: build request: %w", siteID, err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.AccessToken)
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("X-Kameas-Bundle-Sha256", sha256Hex)
	req.Header.Set("X-Kameas-Manifest", base64.StdEncoding.EncodeToString(manifestJSON))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DeploymentRecord{}, fmt.Errorf("fleet/sites: deploy %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return DeploymentRecord{}, fmt.Errorf("fleet/sites: deploy %q: %w", siteID, err)
	}

	var rec DeploymentRecord
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return DeploymentRecord{}, fmt.Errorf("fleet/sites: deploy %q: decode response: %w", siteID, err)
	}
	return rec, nil
}

// ----- SitesList -----

// SitesList returns all sites in the signed-in org.
func (c *Client) SitesList(ctx context.Context) ([]SiteRecord, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}

	resp, err := c.Get(ctx, "/api/v1/sites")
	if err != nil {
		return nil, fmt.Errorf("fleet/sites: list: %w", err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return nil, fmt.Errorf("fleet/sites: list: %w", err)
	}

	var sites []SiteRecord
	if err := json.NewDecoder(resp.Body).Decode(&sites); err != nil {
		return nil, fmt.Errorf("fleet/sites: list: decode: %w", err)
	}
	return sites, nil
}

// ----- SiteStatus -----

// SiteStatus returns the current state of a site including its deployments.
func (c *Client) SiteStatus(ctx context.Context, siteID string) (SiteRecord, error) {
	if c == nil || c.isNop {
		return SiteRecord{}, ErrFleetDisabled
	}

	resp, err := c.Get(ctx, "/api/v1/sites/"+siteID)
	if err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: status %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: status %q: %w", siteID, err)
	}

	var rec SiteRecord
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return SiteRecord{}, fmt.Errorf("fleet/sites: status %q: decode: %w", siteID, err)
	}
	return rec, nil
}

// ----- SiteLogs -----

// SiteLogs returns build + runtime logs for a dynamic site. For static sites
// the server returns 404 (surfaced as ErrSiteNotFound).
// tailLines caps the number of lines returned (server enforces ≤ 2000).
func (c *Client) SiteLogs(ctx context.Context, siteID string, tailLines int) (string, error) {
	if c == nil || c.isNop {
		return "", ErrFleetDisabled
	}

	path := fmt.Sprintf("/api/v1/sites/%s/logs", siteID)
	if tailLines > 0 {
		path = fmt.Sprintf("%s?tail_lines=%d", path, tailLines)
	}
	resp, err := c.Get(ctx, path)
	if err != nil {
		return "", fmt.Errorf("fleet/sites: logs %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return "", fmt.Errorf("fleet/sites: logs %q: %w", siteID, err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("fleet/sites: logs %q: read: %w", siteID, err)
	}
	return string(body), nil
}

// ----- SiteDelete -----

// SiteDelete deletes a site (creator or org_admin only). The server tombstones
// the site and GCs artifacts after 7 days.
func (c *Client) SiteDelete(ctx context.Context, siteID string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}

	resp, err := c.Delete(ctx, "/api/v1/sites/"+siteID)
	if err != nil {
		return fmt.Errorf("fleet/sites: delete %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	return mapSiteError(resp)
}

// ----- SiteEnvSet -----

// SiteEnvSet sets one or more environment variable values for siteID.
// Values are write-only (GET returns only names + timestamps, never values).
// This is the ONLY way secrets reach a site — they must never ride in bundles.
func (c *Client) SiteEnvSet(ctx context.Context, siteID string, vars map[string]string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}

	body := struct {
		Vars map[string]string `json:"vars"`
	}{Vars: vars}

	resp, err := c.Put(ctx, "/api/v1/sites/"+siteID+"/env", mustMarshalReader(body))
	if err != nil {
		return fmt.Errorf("fleet/sites: env set %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	return mapSiteError(resp)
}

// ----- SiteEnvList -----

// SiteEnvList returns env var names and metadata for siteID. Values are never
// returned (write-only via SiteEnvSet per the API contract).
func (c *Client) SiteEnvList(ctx context.Context, siteID string) ([]SiteEnvEntry, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}

	resp, err := c.Get(ctx, "/api/v1/sites/"+siteID+"/env")
	if err != nil {
		return nil, fmt.Errorf("fleet/sites: env list %q: %w", siteID, err)
	}
	defer resp.Body.Close()

	if err := mapSiteError(resp); err != nil {
		return nil, fmt.Errorf("fleet/sites: env list %q: %w", siteID, err)
	}

	var entries []SiteEnvEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("fleet/sites: env list %q: decode: %w", siteID, err)
	}
	return entries, nil
}

// ----- error mapping -----

// mapSiteError inspects resp.StatusCode and, for non-2xx responses, reads the
// error envelope and returns a typed sentinel or a wrapped generic error.
// The response body is consumed; callers must not read it after calling this.
//
// Mapping follows contract §5:
//   403 capability_not_in_tier → ErrCapabilityNotInTier
//   404                        → ErrSiteNotFound
//   409 slug_conflict          → ErrSlugConflict
//   413 bundle_too_large       → ErrBundleTooLarge
//   422 org_slug_required      → ErrOrgSlugRequired
//   422 invalid_slug           → ErrInvalidSlug
//   422 bundle_digest_mismatch → ErrBundleDigestMismatch
//   429 quota_exceeded         → ErrQuotaExceeded (message includes quota name)
//   2xx                        → nil
func mapSiteError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	var env siteErrorEnvelope
	_ = json.Unmarshal(body, &env)

	msg := env.Message
	if msg == "" {
		msg = string(body)
	}

	switch resp.StatusCode {
	case http.StatusForbidden:
		// 403 may come with code="capability_not_in_tier" or without.
		return fmt.Errorf("%w (server: %s)", ErrCapabilityNotInTier, msg)

	case http.StatusNotFound:
		return fmt.Errorf("%w (server: %s)", ErrSiteNotFound, msg)

	case http.StatusConflict: // 409
		return fmt.Errorf("%w (server: %s)", ErrSlugConflict, msg)

	case http.StatusRequestEntityTooLarge: // 413
		return fmt.Errorf("%w (server: %s)", ErrBundleTooLarge, msg)

	case http.StatusUnprocessableEntity: // 422
		switch env.Code {
		case "org_slug_required":
			return fmt.Errorf("%w (server: %s)", ErrOrgSlugRequired, msg)
		case "invalid_slug":
			return fmt.Errorf("%w (server: %s)", ErrInvalidSlug, msg)
		case "bundle_digest_mismatch":
			return fmt.Errorf("%w (server: %s)", ErrBundleDigestMismatch, msg)
		default:
			// Surface 422 verbatim (e.g. unsupported_runtime — server validates enum).
			return fmt.Errorf("fleet/sites: server error 422 %s: %s", env.Code, msg)
		}

	case http.StatusTooManyRequests: // 429
		return fmt.Errorf("%w (quota: %s, server: %s)", ErrQuotaExceeded, env.Code, msg)
	}

	return fmt.Errorf("fleet/sites: server error %d (code=%s): %s", resp.StatusCode, env.Code, msg)
}

// mustMarshalReader marshals v to JSON and returns an io.Reader backed by a
// bytes.Buffer. Panics only on marshal errors that should be impossible (all
// fields are primitive types).
func mustMarshalReader(v any) io.Reader {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("fleet/sites: marshal: %v", err))
	}
	return bytes.NewReader(data)
}
