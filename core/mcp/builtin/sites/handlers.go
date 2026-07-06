// Package sites — handlers.go
//
// Tool handler implementations for the fleet-sites MCP server.
// Each handler is a thin adapter over core/sites + core/fleet/sites.go.
//
// Error handling contract (FR-101):
//   - Fleet disabled, not signed in, capability absent, and network errors
//     all return (nil, error) so HandleEnvelope surfaces them as
//     {isError:true} tool results — never as JSON-RPC errors that would
//     break the caller's tool loop.
//   - server §5 error codes and messages are surfaced verbatim.
//
// Mission: sites-mcp-server-01NSITE05 WP02.
package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	coresites "github.com/kameas-ai/kenaz-harness/core/sites"
)

// ----- sites_deploy -----

type sitesDeployArgs struct {
	Path           string `json:"path"`
	Wait           *bool  `json:"wait"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
}

func makeHandleSitesDeploy(fc *fleet.Client, dataDir string) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		caps := loadCaps(dataDir)
		if err := authCheck(fc, caps); err != nil {
			return nil, err
		}

		var args sitesDeployArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("sites_deploy: invalid arguments: %w", err)
		}
		if args.Path == "" {
			return nil, fmt.Errorf("sites_deploy: path is required")
		}

		// Default wait=true, timeout=300.
		wait := true
		if args.Wait != nil {
			wait = *args.Wait
		}
		timeout := 300
		if args.TimeoutSeconds != nil && *args.TimeoutSeconds > 0 {
			timeout = *args.TimeoutSeconds
		}

		// Parse and validate the manifest.
		manifest, err := coresites.Parse(args.Path)
		if err != nil {
			return nil, fmt.Errorf("sites_deploy: %w", err)
		}

		// Pack the directory.
		var buf bytes.Buffer
		packResult, err := coresites.Pack(ctx, args.Path, manifest, &buf, coresites.PackOptions{})
		if err != nil {
			return nil, fmt.Errorf("sites_deploy: pack: %w", err)
		}

		// Marshal the manifest JSON for the X-Kameas-Manifest header.
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("sites_deploy: marshal manifest: %w", err)
		}

		// Ensure the site exists (create on first deploy).
		siteID, siteURL, err := ensureSite(ctx, fc, manifest.Name, string(manifest.Type))
		if err != nil {
			return nil, fmt.Errorf("sites_deploy: ensure site: %w", err)
		}

		// Upload the bundle.
		deployment, err := fc.SiteDeploy(ctx, siteID, manifestJSON, &buf, packResult.SHA256Hex)
		if err != nil {
			return nil, fmt.Errorf("sites_deploy: upload: %w", err)
		}

		// Use the URL from the deployment record if available; fall back to the site URL.
		deployURL := deployment.URL
		if deployURL == "" {
			deployURL = siteURL
		}

		// Record in recents (best-effort).
		if dataDir != "" {
			_ = coresites.RecordRecent(dataDir, args.Path, manifest.Name, string(manifest.Type), deployment.ID, deployURL)
		}

		result := map[string]any{
			"site":          manifest.Name,
			"site_id":       siteID,
			"deployment_id": deployment.ID,
			"status":        deployment.Status,
			"url":           deployURL,
			"sha256":        packResult.SHA256Hex,
		}
		if len(packResult.Excluded) > 0 {
			result["excluded_files"] = packResult.Excluded
		}

		if !wait {
			return result, nil
		}

		// Poll for completion.
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		pollCtx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()

		finalDeploy, err := pollDeployment(pollCtx, fc, siteID, deployment.ID)
		if err != nil {
			// Return what we have; timeout is not a fatal error.
			result["poll_error"] = err.Error()
			return result, nil
		}
		result["status"] = finalDeploy.Status
		if finalDeploy.URL != "" {
			result["url"] = finalDeploy.URL
		}
		if finalDeploy.Error != "" {
			result["deploy_error"] = finalDeploy.Error
		}
		return result, nil
	}
}

// ensureSite looks up the site by slug or creates it if not found.
func ensureSite(ctx context.Context, fc *fleet.Client, slug, kind string) (id, url string, err error) {
	sites, listErr := fc.SitesList(ctx)
	if listErr != nil {
		return "", "", fmt.Errorf("list sites: %w", listErr)
	}
	for _, s := range sites {
		if s.Slug == slug {
			return s.ID, s.URL, nil
		}
	}
	// Not found — create.
	rec, createErr := fc.SiteCreate(ctx, slug, kind)
	if createErr != nil {
		return "", "", fmt.Errorf("create site: %w", createErr)
	}
	return rec.ID, rec.URL, nil
}

// pollDeployment polls GET /sites/{id} until deployment reaches a terminal state.
func pollDeployment(ctx context.Context, fc *fleet.Client, siteID, deploymentID string) (fleet.DeploymentRecord, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fleet.DeploymentRecord{}, fmt.Errorf("timed out waiting for deployment %s", deploymentID)
		case <-ticker.C:
			site, err := fc.SiteStatus(ctx, siteID)
			if err != nil {
				continue // transient error — keep polling
			}
			if site.ActiveDeployID == deploymentID {
				// Active deployment — it's live.
				return fleet.DeploymentRecord{
					ID:     deploymentID,
					SiteID: siteID,
					Status: "live",
					URL:    site.URL,
				}, nil
			}
			// If the site has no active deployment and this isn't it, check if failed.
			// We can't see individual deployment status without a dedicated endpoint,
			// so we poll until it becomes active or timeout.
		}
	}
}

// ----- sites_list -----

func makeHandleSitesList(fc *fleet.Client) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		// sites_list is a read tool but still needs auth (no capability check needed per spec).
		if fc == nil || fleet.Disabled() {
			return nil, fmt.Errorf("Fleet integration is disabled (HARNESS_FLEET_DISABLED=1).")
		}
		if _, err := fleet.LoadTokens(); err != nil {
			return nil, fmt.Errorf("Not signed in to Fleet. Sign in from the harness (Settings → Fleet) and retry.")
		}

		sites, err := fc.SitesList(ctx)
		if err != nil {
			return nil, fmt.Errorf("sites_list: %w", err)
		}
		return map[string]any{"sites": sites}, nil
	}
}

// ----- sites_status -----

type sitesStatusArgs struct {
	Site         string `json:"site"`
	DeploymentID string `json:"deployment_id"`
}

func makeHandleSitesStatus(fc *fleet.Client) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if fc == nil || fleet.Disabled() {
			return nil, fmt.Errorf("Fleet integration is disabled (HARNESS_FLEET_DISABLED=1).")
		}
		if _, err := fleet.LoadTokens(); err != nil {
			return nil, fmt.Errorf("Not signed in to Fleet. Sign in from the harness (Settings → Fleet) and retry.")
		}

		var args sitesStatusArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("sites_status: invalid arguments: %w", err)
		}
		if args.Site == "" {
			return nil, fmt.Errorf("sites_status: site is required")
		}

		rec, err := fc.SiteStatus(ctx, args.Site)
		if err != nil {
			return nil, fmt.Errorf("sites_status: %w", err)
		}
		return rec, nil
	}
}

// ----- sites_logs -----

type sitesLogsArgs struct {
	Site         string `json:"site"`
	DeploymentID string `json:"deployment_id"`
	TailLines    int    `json:"tail_lines"`
}

func makeHandleSitesLogs(fc *fleet.Client) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if fc == nil || fleet.Disabled() {
			return nil, fmt.Errorf("Fleet integration is disabled (HARNESS_FLEET_DISABLED=1).")
		}
		if _, err := fleet.LoadTokens(); err != nil {
			return nil, fmt.Errorf("Not signed in to Fleet. Sign in from the harness (Settings → Fleet) and retry.")
		}

		var args sitesLogsArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("sites_logs: invalid arguments: %w", err)
		}
		if args.Site == "" {
			return nil, fmt.Errorf("sites_logs: site is required")
		}
		tailLines := args.TailLines
		if tailLines <= 0 {
			tailLines = 100
		}
		if tailLines > 2000 {
			tailLines = 2000
		}

		logs, err := fc.SiteLogs(ctx, args.Site, tailLines)
		if err != nil {
			return nil, fmt.Errorf("sites_logs: %w", err)
		}
		return map[string]any{
			"site":       args.Site,
			"tail_lines": tailLines,
			"logs":       logs,
		}, nil
	}
}

// ----- sites_delete -----

type sitesDeleteArgs struct {
	Site    string `json:"site"`
	Confirm *bool  `json:"confirm"`
}

func makeHandleSitesDelete(fc *fleet.Client) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if fc == nil || fleet.Disabled() {
			return nil, fmt.Errorf("Fleet integration is disabled (HARNESS_FLEET_DISABLED=1).")
		}
		if _, err := fleet.LoadTokens(); err != nil {
			return nil, fmt.Errorf("Not signed in to Fleet. Sign in from the harness (Settings → Fleet) and retry.")
		}

		var args sitesDeleteArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("sites_delete: invalid arguments: %w", err)
		}
		if args.Site == "" {
			return nil, fmt.Errorf("sites_delete: site is required")
		}
		// FR-103: confirm must be literally true.
		if args.Confirm == nil || !*args.Confirm {
			return nil, fmt.Errorf("sites_delete: confirm must be true to delete site %q. Pass {\"confirm\": true} to proceed. This action cannot be undone.", args.Site)
		}

		if err := fc.SiteDelete(ctx, args.Site); err != nil {
			return nil, fmt.Errorf("sites_delete: %w", err)
		}
		return map[string]any{
			"site":    args.Site,
			"deleted": true,
			"message": fmt.Sprintf("Site %q deleted. Artifacts will be garbage-collected after 7 days.", args.Site),
		}, nil
	}
}
