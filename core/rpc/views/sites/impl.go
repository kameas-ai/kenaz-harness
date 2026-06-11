// Package sites — impl.go
//
// SitesImpl implements SitesAPI. Each method is a thin shell that:
//  1. Checks fleet preconditions (disabled / signed-out / cap-absent).
//  2. Delegates to core/sites (packager) and/or core/fleet/sites.go (HTTP).
//  3. Emits progress events via the Emitter interface.
//
// The logic mirrors core/mcp/builtin/sites/handlers.go but exposes typed
// return values (SiteSummary) instead of MCP tool-result maps, and uses
// the Wails event bus instead of MCP tool-call progress reporting.
//
// OSS boundary: no quota constants, cloud identifiers, or tier names.
// Error messages are either sentinel-wrapped or verbatim from the Fleet API.
//
// Mission: sites-ui-01NSITE06, WP01–WP04.
package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coresites "github.com/kameas-ai/kenaz-harness/core/sites"
)

// Emitter is the minimal interface for broadcasting Wails events.
// Satisfied by *rpc.StreamBroker and stubbed in tests.
type Emitter interface {
	Publish(topic string, payload any)
}

// noopEmitter discards all events. Used when no broker is wired (test chassis).
type noopEmitter struct{}

func (noopEmitter) Publish(_ string, _ any) {}

// FleetSitesClient is the fleet operations interface used by SitesImpl.
// The concrete *corefleet.Client satisfies this interface; tests supply a fake.
type FleetSitesClient interface {
	SitesList(ctx context.Context) ([]corefleet.SiteRecord, error)
	SiteCreate(ctx context.Context, slug, kind string) (corefleet.SiteRecord, error)
	SiteDeploy(ctx context.Context, siteID string, manifestJSON []byte, bundle io.Reader, sha256Hex string) (corefleet.DeploymentRecord, error)
	SiteStatus(ctx context.Context, siteID string) (corefleet.SiteRecord, error)
	SiteLogs(ctx context.Context, siteID string, tailLines int) (string, error)
	SiteDelete(ctx context.Context, siteID string) error
}

// verify *corefleet.Client satisfies FleetSitesClient at compile time.
var _ FleetSitesClient = (*corefleet.Client)(nil)

// progressTopic is the Wails event topic for deploy progress events (FR-002).
const progressTopic = "sites:deploy:progress"

// tailLinesMax is the server-enforced cap per contract §1 (FR-003).
const tailLinesMax = 2000

// pollInterval is the period between SiteStatus polls during Sites_Deploy.
const pollInterval = 3 * time.Second

// SitesImpl is the concrete implementation of SitesAPI.
type SitesImpl struct {
	// fleetEnabled tracks whether the fleet client is non-nop. Set by New via
	// the corefleet.Disabled() flag so tests can override via the env var.
	fleetEnabled bool
	client       FleetSitesClient
	dataDir      string
	emitter      Emitter
}

var _ SitesAPI = (*SitesImpl)(nil)

// New constructs a SitesImpl backed by the provided fleet client.
// emitter may be nil (noopEmitter is used). fc may be nil — all methods
// will return ErrFleetDisabled.
func New(fc *corefleet.Client, dataDir string, emitter Emitter) *SitesImpl {
	if emitter == nil {
		emitter = noopEmitter{}
	}
	return &SitesImpl{
		fleetEnabled: fc != nil && !corefleet.Disabled(),
		client:       fc, // *corefleet.Client satisfies FleetSitesClient
		dataDir:      dataDir,
		emitter:      emitter,
	}
}

// newWithClient constructs a SitesImpl using a FleetSitesClient interface.
// Used by tests that supply a fake client. fleetEnabled must match the
// fleet-disabled state the test is simulating.
func newWithClient(fc FleetSitesClient, fleetEnabled bool, dataDir string, emitter Emitter) *SitesImpl {
	if emitter == nil {
		emitter = noopEmitter{}
	}
	return &SitesImpl{
		fleetEnabled: fleetEnabled,
		client:       fc,
		dataDir:      dataDir,
		emitter:      emitter,
	}
}

// ----- precondition check -----

// checkPreConditions verifies fleet is enabled, the user is signed in, and
// the sites_hosting capability is present. Returns a typed sentinel error on
// failure so the frontend can render contextual messages (FR-005).
func (s *SitesImpl) checkPreConditions() error {
	if !s.fleetEnabled || corefleet.Disabled() {
		return corefleet.ErrFleetDisabled
	}
	if _, err := corefleet.LoadTokens(); err != nil {
		return corefleet.ErrNotSignedIn
	}
	caps := loadCaps(s.dataDir)
	if !caps.Has(corefleet.CapSitesHosting) {
		return corefleet.ErrCapabilityNotInTier
	}
	return nil
}

// loadCaps loads the capability snapshot from disk. Returns DefaultDenyCapabilities
// on any error. Mirrors the MCP handler helper.
func loadCaps(dataDir string) corefleet.Capabilities {
	if dataDir == "" {
		return corefleet.DefaultDenyCapabilities()
	}
	caps, err := corefleet.LoadCapabilities(dataDir)
	if err != nil {
		return corefleet.DefaultDenyCapabilities()
	}
	return caps
}

// ----- Sites_List (WP02) -----

// Sites_List calls SitesList and maps the wire records to SiteSummary (FR-001).
func (s *SitesImpl) Sites_List(ctx context.Context) ([]SiteSummary, error) {
	if err := s.checkPreConditions(); err != nil {
		return nil, err
	}
	recs, err := s.client.SitesList(ctx)
	if err != nil {
		return nil, fmt.Errorf("sites: list: %w", err)
	}
	out := make([]SiteSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, siteRecordToSummary(r))
	}
	return out, nil
}

// siteRecordToSummary converts a fleet.SiteRecord to the RPC wire shape.
// Status is "live" when an active deployment is set, otherwise "unknown".
func siteRecordToSummary(r corefleet.SiteRecord) SiteSummary {
	status := "unknown"
	if r.ActiveDeployID != "" {
		status = "live"
	}
	return SiteSummary{
		Name:       r.Slug,
		Kind:       r.Kind,
		URL:        r.URL,
		Status:     status,
		DeployedAt: r.UpdatedAt,
	}
}

// ----- Sites_Status (WP02) -----

// Sites_Status returns the current state of a single site by slug.
func (s *SitesImpl) Sites_Status(ctx context.Context, site string) (SiteSummary, error) {
	if err := s.checkPreConditions(); err != nil {
		return SiteSummary{}, err
	}
	rec, err := s.client.SiteStatus(ctx, site)
	if err != nil {
		return SiteSummary{}, fmt.Errorf("sites: status %q: %w", site, err)
	}
	return siteRecordToSummary(rec), nil
}

// ----- Sites_Deploy (WP03) -----

// Sites_Deploy validates the manifest in rootDir, packs it, uploads to fleet,
// polls until a terminal state, and emits progress events at each stage.
// Progress events use topic "sites:deploy:progress" (FR-002 / NFR-003).
func (s *SitesImpl) Sites_Deploy(ctx context.Context, rootDir string) (SiteSummary, error) {
	if err := s.checkPreConditions(); err != nil {
		return SiteSummary{}, err
	}

	// 1. Validate manifest.
	s.emitProgress("validating", "Validating manifest…", "")
	manifest, err := coresites.Parse(rootDir)
	if err != nil {
		s.emitProgress("error", err.Error(), "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: validate: %w", err)
	}

	// 2. Pack directory.
	s.emitProgress("packing", "Packing site files…", "")
	var buf bytes.Buffer
	packResult, err := coresites.Pack(ctx, rootDir, manifest, &buf, coresites.PackOptions{})
	if err != nil {
		s.emitProgress("error", err.Error(), "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: pack: %w", err)
	}

	// Marshal manifest JSON for the X-Kameas-Manifest header.
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		s.emitProgress("error", "marshal manifest: "+err.Error(), "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: marshal manifest: %w", err)
	}

	// Ensure the site record exists (create on first deploy, lookup on subsequent).
	siteID, siteURL, err := s.ensureSite(ctx, manifest.Name, string(manifest.Type))
	if err != nil {
		s.emitProgress("error", err.Error(), "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: ensure site: %w", err)
	}

	// 3. Upload.
	s.emitProgress("uploading", "Uploading bundle…", "")
	deployment, err := s.client.SiteDeploy(ctx, siteID, manifestJSON, &buf, packResult.SHA256Hex)
	if err != nil {
		s.emitProgress("error", err.Error(), "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: upload: %w", err)
	}

	// Use URL from deployment record if available, fall back to site URL.
	deployURL := deployment.URL
	if deployURL == "" {
		deployURL = siteURL
	}

	// Record in recents (best-effort, never fail the deploy on error).
	if s.dataDir != "" {
		_ = coresites.RecordRecent(s.dataDir, rootDir, manifest.Name, string(manifest.Type), deployment.ID, deployURL)
	}

	// 4. Poll for terminal state.
	s.emitProgress("waiting", "Waiting for deployment to go live…", "")
	const pollTimeout = 5 * time.Minute
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	finalStatus, finalURL := s.pollUntilTerminal(pollCtx, siteID, deployment.ID, deployURL)

	// 5. Emit final event.
	switch finalStatus {
	case "live":
		s.emitProgress("done", "Deployment live.", finalURL)
	case "failed":
		s.emitProgress("error", "Deployment failed.", "")
		return SiteSummary{}, fmt.Errorf("sites: deploy: deployment %s failed", deployment.ID)
	default:
		// Timed out — submit done with what we have; the site will come up.
		s.emitProgress("done", "Deployment submitted (status pending).", finalURL)
	}

	return SiteSummary{
		Name:       manifest.Name,
		Kind:       string(manifest.Type),
		URL:        finalURL,
		Status:     finalStatus,
		DeployedAt: time.Now().UTC(),
	}, nil
}

// pollUntilTerminal polls SiteStatus every pollInterval until the deployment
// is active ("live"), the context times out, or a hard failure is detected.
// Returns the final status string and the live URL (may be empty on timeout).
func (s *SitesImpl) pollUntilTerminal(ctx context.Context, siteID, deploymentID, fallbackURL string) (status, url string) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "pending", fallbackURL
		case <-ticker.C:
			rec, err := s.client.SiteStatus(ctx, siteID)
			if err != nil {
				continue // transient error; keep polling
			}
			if rec.ActiveDeployID == deploymentID {
				liveURL := rec.URL
				if liveURL == "" {
					liveURL = fallbackURL
				}
				return "live", liveURL
			}
		}
	}
}

// emitProgress is a helper that publishes a DeployProgressEvent to the event bus.
func (s *SitesImpl) emitProgress(stage, message, url string) {
	s.emitter.Publish(progressTopic, DeployProgressEvent{
		Stage:   stage,
		Message: message,
		URL:     url,
	})
}

// ensureSite looks up a site by slug or creates it if not found.
// Mirrors core/mcp/builtin/sites/handlers.go::ensureSite.
func (s *SitesImpl) ensureSite(ctx context.Context, slug, kind string) (id, siteURL string, err error) {
	sites, listErr := s.client.SitesList(ctx)
	if listErr != nil {
		return "", "", fmt.Errorf("list sites: %w", listErr)
	}
	for _, site := range sites {
		if site.Slug == slug {
			return site.ID, site.URL, nil
		}
	}
	// Not found — create.
	rec, createErr := s.client.SiteCreate(ctx, slug, kind)
	if createErr != nil {
		return "", "", fmt.Errorf("create site: %w", createErr)
	}
	return rec.ID, rec.URL, nil
}

// ----- Sites_Logs (WP04) -----

// Sites_Logs returns up to tailLines lines of build+runtime log. tailLines is
// clamped to tailLinesMax (2000) per contract §1 (FR-003).
func (s *SitesImpl) Sites_Logs(ctx context.Context, site string, tailLines int) (string, error) {
	if err := s.checkPreConditions(); err != nil {
		return "", err
	}
	// Clamp to the server-contract maximum (FR-003).
	if tailLines <= 0 {
		tailLines = 100
	}
	if tailLines > tailLinesMax {
		tailLines = tailLinesMax
	}
	logs, err := s.client.SiteLogs(ctx, site, tailLines)
	if err != nil {
		if errors.Is(err, corefleet.ErrSiteNotFound) {
			return "", corefleet.ErrSiteNotFound
		}
		return "", fmt.Errorf("sites: logs %q: %w", site, err)
	}
	return logs, nil
}

// ----- Sites_Delete (WP04) -----

// Sites_Delete deletes the named site. The frontend enforces the two-step
// confirm dialog before invoking this method (FR-004). No bool guard is
// applied here — deletion confirmation is a UI concern (spec §2).
func (s *SitesImpl) Sites_Delete(ctx context.Context, site string) error {
	if err := s.checkPreConditions(); err != nil {
		return err
	}
	if err := s.client.SiteDelete(ctx, site); err != nil {
		return fmt.Errorf("sites: delete %q: %w", site, err)
	}
	return nil
}
