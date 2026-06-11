package sites_test

// RPC view unit tests for core/rpc/views/sites (sites-ui-01NSITE06 WP01–WP04).
//
// Tests use a fake FleetSitesClient to avoid network/keychain dependencies.
// The fake is driven by the test's scenario rather than httptest — this
// mirrors how scheduledchat and agentgraph view tests work: test at the
// interface level, not the HTTP level (the fleet client's HTTP layer is
// already covered by core/fleet/sites_test.go).
//
// Precondition tests (fleet-disabled / signed-out / cap-absent) use the
// HARNESS_FLEET_DISABLED environment variable to exercise the real gate path.
// They're -short compatible: no network calls, no keychain access needed for
// the disabled/cap-absent paths.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	viewsites "github.com/kameas-ai/kenaz-harness/core/rpc/views/sites"
)

// ----- fake fleet client -----

// fakeFleetClient is a controllable implementation of FleetSitesClient.
type fakeFleetClient struct {
	// listResult / listErr control SitesList.
	listResult []corefleet.SiteRecord
	listErr    error

	// createResult / createErr control SiteCreate.
	createResult corefleet.SiteRecord
	createErr    error

	// deployResult / deployErr control SiteDeploy.
	deployResult corefleet.DeploymentRecord
	deployErr    error

	// statusResult / statusErr control SiteStatus.
	statusResult corefleet.SiteRecord
	statusErr    error

	// logsResult / logsErr control SiteLogs.
	logsResult string
	logsErr    error

	// deleteErr controls SiteDelete.
	deleteErr error

	// recorded tail lines from last SiteLogs call.
	lastTailLines int
}

func (f *fakeFleetClient) SitesList(_ context.Context) ([]corefleet.SiteRecord, error) {
	return f.listResult, f.listErr
}
func (f *fakeFleetClient) SiteCreate(_ context.Context, slug, kind string) (corefleet.SiteRecord, error) {
	if f.createResult.ID == "" {
		// Default: return a record derived from args.
		return corefleet.SiteRecord{ID: "site-" + slug, Slug: slug, Kind: kind, URL: "https://" + slug + ".example.com"}, f.createErr
	}
	return f.createResult, f.createErr
}
func (f *fakeFleetClient) SiteDeploy(_ context.Context, _ string, _ []byte, _ io.Reader, _ string) (corefleet.DeploymentRecord, error) {
	return f.deployResult, f.deployErr
}
func (f *fakeFleetClient) SiteStatus(_ context.Context, _ string) (corefleet.SiteRecord, error) {
	return f.statusResult, f.statusErr
}
func (f *fakeFleetClient) SiteLogs(_ context.Context, _ string, tailLines int) (string, error) {
	f.lastTailLines = tailLines
	return f.logsResult, f.logsErr
}
func (f *fakeFleetClient) SiteDelete(_ context.Context, _ string) error {
	return f.deleteErr
}

// ----- fake emitter -----

type fakeEmitter struct {
	events []viewsites.DeployProgressEvent
}

func (fe *fakeEmitter) Publish(_ string, payload any) {
	if ev, ok := payload.(viewsites.DeployProgressEvent); ok {
		fe.events = append(fe.events, ev)
	}
}

// ----- helper -----

// setupCapability writes a fresh capability snapshot with sites_hosting=true
// so checkPreConditions passes the capability gate. Returns the dataDir.
func setupCapability(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	caps := corefleet.Capabilities{
		Tier: "enterprise",
		Enabled: map[corefleet.Capability]bool{
			corefleet.CapSitesHosting: true,
		},
		// FetchedAt must be recent so Has() does not treat the snapshot as stale.
		FetchedAt: time.Now(),
		Source:    "test",
	}
	if err := corefleet.SaveCapabilities(dataDir, caps); err != nil {
		t.Fatalf("SaveCapabilities: %v", err)
	}
	return dataDir
}

// newImplWithCap builds a SitesImpl with fleetEnabled=true, the given fake
// client, and a capability-aware dataDir. Tokens are NOT injected; tests that
// need to pass the signed-in gate must do so separately.
func newImplWithCap(t *testing.T, fc *fakeFleetClient, em *fakeEmitter) (*viewsites.SitesImpl, string) {
	t.Helper()
	dataDir := setupCapability(t)
	impl := viewsites.NewForTesting(fc, true, dataDir, em)
	return impl, dataDir
}

// ----- precondition: fleet disabled -----

func TestSitesList_FleetDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	impl := viewsites.NewForTesting(&fakeFleetClient{}, false, t.TempDir(), nil)
	_, err := impl.Sites_List(t.Context())
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Sites_List: got %v, want ErrFleetDisabled", err)
	}
}

func TestSitesDeploy_FleetDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	impl := viewsites.NewForTesting(&fakeFleetClient{}, false, t.TempDir(), nil)
	_, err := impl.Sites_Deploy(t.Context(), t.TempDir())
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Sites_Deploy: got %v, want ErrFleetDisabled", err)
	}
}

func TestSitesStatus_FleetDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	impl := viewsites.NewForTesting(&fakeFleetClient{}, false, t.TempDir(), nil)
	_, err := impl.Sites_Status(t.Context(), "my-site")
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Sites_Status: got %v, want ErrFleetDisabled", err)
	}
}

func TestSitesLogs_FleetDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	impl := viewsites.NewForTesting(&fakeFleetClient{}, false, t.TempDir(), nil)
	_, err := impl.Sites_Logs(t.Context(), "my-site", 100)
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Sites_Logs: got %v, want ErrFleetDisabled", err)
	}
}

func TestSitesDelete_FleetDisabled(t *testing.T) {
	t.Setenv("HARNESS_FLEET_DISABLED", "1")
	impl := viewsites.NewForTesting(&fakeFleetClient{}, false, t.TempDir(), nil)
	err := impl.Sites_Delete(t.Context(), "my-site")
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Sites_Delete: got %v, want ErrFleetDisabled", err)
	}
}

// ----- precondition: not signed in -----

func TestSitesList_NotSignedIn(t *testing.T) {
	// With fleetEnabled=true but no tokens, LoadTokens returns an error → ErrNotSignedIn.
	impl := viewsites.NewForTesting(&fakeFleetClient{}, true, t.TempDir(), nil)
	_, err := impl.Sites_List(t.Context())
	if !errors.Is(err, corefleet.ErrNotSignedIn) {
		t.Errorf("Sites_List: got %v, want ErrNotSignedIn", err)
	}
}

// ----- tailLines clamp (FR-003, acceptance criterion 6) -----

func TestSitesLogs_TailLinesClampedTo2000(t *testing.T) {
	// Bypass preconditions: fleet-disabled returns ErrFleetDisabled before
	// reaching the clamp logic. We test the clamp with a signed-in setup.
	// Since injecting real tokens is fragile in CI, we test the clamp by
	// verifying the fake receives ≤2000 even when the caller passes 3000.
	//
	// We use a sub-implementation that skips the precondition check by
	// directly testing the clamping logic in Sites_Logs.
	//
	// The approach: populate a token so LoadTokens returns success, then
	// verify the fake client's lastTailLines is clamped.
	ts := corefleet.TokenSet{
		AccessToken:  "tok-access",
		RefreshToken: "tok-refresh",
	}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{logsResult: "log line\n"}
	em := &fakeEmitter{}
	impl, _ := newImplWithCap(t, fc, em)

	_, err := impl.Sites_Logs(t.Context(), "my-site", 3000)
	if err != nil {
		t.Fatalf("Sites_Logs: %v", err)
	}
	if fc.lastTailLines != 2000 {
		t.Errorf("tailLines sent to client = %d, want 2000", fc.lastTailLines)
	}
}

// ----- Sites_List happy path -----

func TestSitesList_ThreeSites(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{
		listResult: []corefleet.SiteRecord{
			{ID: "s1", Slug: "alpha", Kind: "static", URL: "https://alpha.example.com", ActiveDeployID: "d1"},
			{ID: "s2", Slug: "beta", Kind: "dynamic", URL: "https://beta.example.com"},
			{ID: "s3", Slug: "gamma", Kind: "static"},
		},
	}
	em := &fakeEmitter{}
	impl, _ := newImplWithCap(t, fc, em)

	sites, err := impl.Sites_List(t.Context())
	if err != nil {
		t.Fatalf("Sites_List: %v", err)
	}
	if len(sites) != 3 {
		t.Fatalf("len(sites) = %d, want 3", len(sites))
	}
	// Site with active deploy → status = "live"
	if sites[0].Status != "live" {
		t.Errorf("sites[0].Status = %q, want %q", sites[0].Status, "live")
	}
	// Site without active deploy → status = "unknown"
	if sites[1].Status != "unknown" {
		t.Errorf("sites[1].Status = %q, want %q", sites[1].Status, "unknown")
	}
	if sites[0].Name != "alpha" {
		t.Errorf("sites[0].Name = %q, want %q", sites[0].Name, "alpha")
	}
	if sites[0].URL != "https://alpha.example.com" {
		t.Errorf("sites[0].URL = %q, want %q", sites[0].URL, "https://alpha.example.com")
	}
}

// ----- Sites_Status -----

func TestSitesStatus_Live(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{
		statusResult: corefleet.SiteRecord{
			ID: "s1", Slug: "alpha", Kind: "static",
			URL: "https://alpha.example.com", ActiveDeployID: "d1",
		},
	}
	impl, _ := newImplWithCap(t, fc, nil)
	sum, err := impl.Sites_Status(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Sites_Status: %v", err)
	}
	if sum.Status != "live" {
		t.Errorf("Status = %q, want %q", sum.Status, "live")
	}
	if sum.URL != "https://alpha.example.com" {
		t.Errorf("URL = %q", sum.URL)
	}
}

func TestSitesStatus_Failed(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{
		statusErr: fmt.Errorf("%w: site not found", corefleet.ErrSiteNotFound),
	}
	impl, _ := newImplWithCap(t, fc, nil)
	_, err := impl.Sites_Status(t.Context(), "missing-site")
	if err == nil {
		t.Fatal("want error for missing site, got nil")
	}
}

// ----- Sites_Delete -----

func TestSitesDelete_OK(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{}
	impl, _ := newImplWithCap(t, fc, nil)
	if err := impl.Sites_Delete(t.Context(), "my-site"); err != nil {
		t.Fatalf("Sites_Delete: %v", err)
	}
}

func TestSitesDelete_NotFound(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{deleteErr: fmt.Errorf("%w", corefleet.ErrSiteNotFound)}
	impl, _ := newImplWithCap(t, fc, nil)
	err := impl.Sites_Delete(t.Context(), "ghost")
	if err == nil {
		t.Fatal("want error for not-found delete, got nil")
	}
	if !errors.Is(err, corefleet.ErrSiteNotFound) {
		t.Errorf("error = %v, want to wrap ErrSiteNotFound", err)
	}
}

// ----- Sites_Deploy — invalid manifest (WP03) -----

func TestSitesDeploy_InvalidManifest(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	em := &fakeEmitter{}
	impl, _ := newImplWithCap(t, &fakeFleetClient{}, em)

	// rootDir with no manifest file → Parse returns error.
	rootDir := t.TempDir()
	_, err := impl.Sites_Deploy(t.Context(), rootDir)
	if err == nil {
		t.Fatal("expected error for missing manifest, got nil")
	}
	// Progress events should include "validating" then "error".
	if len(em.events) < 2 {
		t.Fatalf("expected at least 2 progress events, got %d: %v", len(em.events), em.events)
	}
	first := em.events[0]
	if first.Stage != "validating" {
		t.Errorf("first event stage = %q, want %q", first.Stage, "validating")
	}
	last := em.events[len(em.events)-1]
	if last.Stage != "error" {
		t.Errorf("last event stage = %q, want %q", last.Stage, "error")
	}
}

// ----- Sites_Deploy — upload error surfaced verbatim (WP03) -----

func TestSitesDeploy_UploadError422(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	// SitesList: site not found → SiteCreate will be called.
	// SiteCreate: OK.
	// SiteDeploy: returns ErrInvalidSlug (simulates 422 invalid_slug).
	fc := &fakeFleetClient{
		listResult: []corefleet.SiteRecord{}, // empty → will trigger create
		deployErr:  fmt.Errorf("%w: site slug is invalid", corefleet.ErrInvalidSlug),
	}

	em := &fakeEmitter{}
	impl, _ := newImplWithCap(t, fc, em)

	// Write a minimal valid manifest so Pack succeeds.
	rootDir := t.TempDir()
	writeTestManifest(t, rootDir, "test-site", "static")

	_, err := impl.Sites_Deploy(t.Context(), rootDir)
	if err == nil {
		t.Fatal("expected error from upload, got nil")
	}
	if !errors.Is(err, corefleet.ErrInvalidSlug) {
		t.Errorf("error = %v, want to wrap ErrInvalidSlug", err)
	}
	// Last progress event should be "error".
	if len(em.events) == 0 {
		t.Fatal("expected progress events, got none")
	}
	last := em.events[len(em.events)-1]
	if last.Stage != "error" {
		t.Errorf("last event stage = %q, want %q", last.Stage, "error")
	}
}

// ----- Sites_Deploy — happy path progress events (WP03) -----

func TestSitesDeploy_HappyPath_ProgressEventOrder(t *testing.T) {
	ts := corefleet.TokenSet{AccessToken: "tok-access", RefreshToken: "tok-refresh"}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	fc := &fakeFleetClient{
		listResult: []corefleet.SiteRecord{
			{ID: "site-abc", Slug: "test-site", Kind: "static", URL: "https://test-site.example.com"},
		},
		deployResult: corefleet.DeploymentRecord{
			ID:     "dep-001",
			Status: "live",
			URL:    "https://test-site.example.com",
		},
		// On first SiteStatus poll: return active deployment → breaks loop.
		statusResult: corefleet.SiteRecord{
			ID:             "site-abc",
			Slug:           "test-site",
			ActiveDeployID: "dep-001",
			URL:            "https://test-site.example.com",
		},
	}

	em := &fakeEmitter{}
	impl, _ := newImplWithCap(t, fc, em)

	rootDir := t.TempDir()
	writeTestManifest(t, rootDir, "test-site", "static")

	sum, err := impl.Sites_Deploy(t.Context(), rootDir)
	if err != nil {
		t.Fatalf("Sites_Deploy: %v", err)
	}
	if sum.URL != "https://test-site.example.com" {
		t.Errorf("URL = %q", sum.URL)
	}

	// Verify stage sequence.
	wantStages := []string{"validating", "packing", "uploading", "waiting", "done"}
	if len(em.events) < len(wantStages) {
		t.Fatalf("progress events = %d, want at least %d", len(em.events), len(wantStages))
	}
	for i, want := range wantStages {
		if em.events[i].Stage != want {
			t.Errorf("events[%d].Stage = %q, want %q", i, em.events[i].Stage, want)
		}
	}
	// "done" event must carry the URL.
	lastEvent := em.events[len(em.events)-1]
	if lastEvent.URL == "" {
		t.Error("done event URL is empty")
	}
}

// ----- helpers -----

// writeTestManifest creates a minimal kameas-site.json for static sites.
func writeTestManifest(t *testing.T, rootDir, name, kind string) {
	t.Helper()
	// Create a placeholder index.html so the packer has at least one file.
	if err := os.WriteFile(filepath.Join(rootDir, "index.html"), []byte("<h1>hello</h1>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	manifest := `{
  "version": 1,
  "name": "` + name + `",
  "type": "` + kind + `",
  "static": { "dir": "." }
}`
	if err := os.WriteFile(filepath.Join(rootDir, "kameas-site.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
