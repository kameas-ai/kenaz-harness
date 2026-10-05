package sidecar

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

type okTrust struct{}

func (okTrust) Verify(context.Context, trust.VerifyRequest) (trust.VerifyResult, error) {
	return trust.VerifyResult{OK: true}, nil
}

// engineDouble is the far end of the loopback contract: an httptest server
// whose /health can be scripted, with a recorded shutdown count.
type engineDouble struct {
	mu        sync.Mutex
	health    mlsidecar.HealthPayload
	down      bool
	shutdowns int
	srv       *httptest.Server
}

func newEngineDouble(t *testing.T) *engineDouble {
	t.Helper()
	e := &engineDouble{down: true} // nothing answers until the "process" starts
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		h, down := e.health, e.down
		e.mu.Unlock()
		if down {
			// Drop the connection: "nothing listening" must be a transport
			// error. An HTTP 503 is a LIVE process with an unusable answer
			// and now classifies as port-occupied (WP13-merge semantics).
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
				}
				return
			}
			panic("engineDouble: not a Hijacker")
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	mux.HandleFunc("/v1/admin/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		e.shutdowns++
		e.down = true
		e.mu.Unlock()
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *engineDouble) set(f func(h *mlsidecar.HealthPayload, down *bool)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f(&e.health, &e.down)
}

func zipFixture(t *testing.T, channelRoot, name string, launcher []byte) string {
	t.Helper()
	f, err := os.Create(filepath.Join(channelRoot, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("kameas-ml/kameas-ml")
	_, _ = w.Write(launcher)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	rf, _ := os.Open(filepath.Join(channelRoot, name))
	defer rf.Close()
	sha, _, err := integrity.HashSHA256(rf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(channelRoot, name+".sig"), []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	return sha
}

type fixture struct {
	impl        *Impl
	mgr         *mlsidecar.Manager
	eng         *engineDouble
	channelRoot string
	root        string
	release     func(version string) mlsidecar.EngineRelease
	pin         *mlsidecar.EngineRelease
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	channelRoot := t.TempDir()
	root := filepath.Join(t.TempDir(), "ml")
	eng := newEngineDouble(t)
	reg := channels.NewRegistry()
	_ = reg.Register(localpath.Kind, localpath.Factory)

	mgr := mlsidecar.NewManager(mlsidecar.NewLayout(root), mlsidecar.NewClient(eng.srv.URL, nil), nil, "harness", "0.85.0")
	mgr.Registry = reg
	mgr.Creds = secrets.NoopResolver{}
	mgr.Verifier = mlsidecar.DefaultEngineVerifier(okTrust{}, nil)
	// The "process": starting it makes the double answer health with the
	// identity of whatever is installed at `current`.
	mgr.Spawner = mlsidecar.SpawnerFunc(func(_ context.Context, exe string) (int, error) {
		rec, _, _ := mlsidecar.ReadInstallJSON(mgr.Layout)
		eng.set(func(h *mlsidecar.HealthPayload, down *bool) {
			*down = false
			*h = mlsidecar.HealthPayload{
				Product: "kameas-ml", SidecarVersion: rec.Version, ExePath: exe, EngineSHA256: rec.EngineSHA256,
				LifecycleProtocol: 1,
			}
		})
		return 4242, nil
	})

	f := &fixture{mgr: mgr, eng: eng, channelRoot: channelRoot, root: root}
	f.release = func(version string) mlsidecar.EngineRelease {
		name := "engine-" + version + ".zip"
		sha := zipFixture(t, channelRoot, name, []byte("launcher-"+version))
		return mlsidecar.EngineRelease{
			Version: version, ChannelKind: localpath.Kind, ChannelPath: channelRoot, ArtifactPath: name,
			ExpectedSHA256: sha, SizeBytes: 207 << 20, Source: "local_path:" + channelRoot,
			Signature: &manifest.SignatureRef{Kind: "ed25519_detached", Locator: name + ".sig", Algorithm: "ed25519", KeyID: "k"},
		}
	}
	rel := f.release("1.0.0")
	f.pin = &rel
	f.impl = &Impl{
		Manager:  mgr,
		Release:  func(context.Context) (mlsidecar.EngineRelease, error) { return *f.pin, nil },
		Platform: func() (bool, string) { return true, "" },
	}
	return f
}

func TestStatus_NilManager_IsHonestlyUnavailable(t *testing.T) {
	impl := &Impl{}
	v, err := impl.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Available || v.State != "not_installed" || v.UnavailableReason == "" {
		t.Fatalf("view = %+v", v)
	}
	if _, err := impl.Enable(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Enable err = %v", err)
	}
}

func TestStatus_UnsupportedPlatform_NoEnable(t *testing.T) {
	f := newFixture(t)
	f.impl.Platform = func() (bool, string) { return false, "macOS with Apple silicon only" }
	v, _ := f.impl.Status(context.Background())
	if v.Supported || v.Available || v.UnavailableReason != "macOS with Apple silicon only" {
		t.Fatalf("view = %+v", v)
	}
	if _, err := f.impl.Enable(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Enable err = %v", err)
	}
	if _, ok := f.mgr.Installed(); ok {
		t.Fatal("an unsupported-platform Enable must install nothing")
	}
}

func TestStatus_NoPublishedRelease_ExplainsAndRefuses(t *testing.T) {
	f := newFixture(t)
	f.impl.Release = mlsidecar.PinnedEngineRelease
	v, _ := f.impl.Status(context.Background())
	if v.Available || v.UnavailableReason == "" || v.Release.Version != "" {
		t.Fatalf("view = %+v", v)
	}
	if _, err := f.impl.Enable(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Enable err = %v", err)
	}
}

func TestStatus_DisclosesSizeVersionAndLocationBeforeDownload(t *testing.T) {
	f := newFixture(t)
	v, _ := f.impl.Status(context.Background())
	if !v.Available || v.Release.SizeMB != 207 || v.Release.Version != "1.0.0" || v.InstallLocation != f.root {
		t.Fatalf("pre-download disclosure = %+v", v)
	}
	if v.State != "not_installed" || v.Installed {
		t.Fatalf("nothing installed yet: %+v", v)
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Fatal("reading Status must not create anything on disk")
	}
}

func TestEnable_InstallsVerifiesStartsAndReportsHealthy(t *testing.T) {
	f := newFixture(t)
	v, err := f.impl.Enable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "healthy" || !v.Installed || v.InstalledVersion != "1.0.0" {
		t.Fatalf("after Enable: %+v", v)
	}
	// And Status reads it back the same way.
	v2, _ := f.impl.Status(context.Background())
	if v2.State != "healthy" {
		t.Fatalf("Status after Enable: %+v", v2)
	}
}

func TestStatus_InstalledButEngineStopped_IsIdleNotUnhealthy(t *testing.T) {
	f := newFixture(t)
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.eng.set(func(_ *mlsidecar.HealthPayload, down *bool) { *down = true }) // engine self-terminated
	v, _ := f.impl.Status(context.Background())
	if v.State != StateInstalledIdle {
		t.Fatalf("state = %q, want the view-only installed_idle (not installed_unhealthy): %+v", v.State, v)
	}
	if f.mgr.Healthy() {
		t.Fatal("the cached Manager status must no longer claim healthy for a stopped engine")
	}
}

func TestStatus_FailedSpawn_IsInstalledUnhealthyNotIdle(t *testing.T) {
	f := newFixture(t)
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.eng.set(func(_ *mlsidecar.HealthPayload, down *bool) { *down = true })
	f.mgr.Spawner = mlsidecar.SpawnerFunc(func(context.Context, string) (int, error) { return 0, errors.New("exec format error") })
	v, _ := f.impl.Repair(context.Background())
	if v.State != "installed_unhealthy" || v.Reason != "crash" || v.Detail == "" {
		t.Fatalf("view = %+v", v)
	}
	v2, _ := f.impl.Status(context.Background())
	if v2.State != "installed_unhealthy" {
		t.Fatalf("a recorded crash must not be rendered as idle: %+v", v2)
	}
}

func TestStatus_LegacyEngineOnPort_SurfacesLegacyUnverified(t *testing.T) {
	f := newFixture(t)
	f.eng.set(func(h *mlsidecar.HealthPayload, down *bool) {
		*down = false
		*h = mlsidecar.HealthPayload{Product: "kameas-ml", SidecarVersion: "0.9.0"}
	}) // pre-lease
	v, _ := f.impl.Status(context.Background())
	if v.State != "legacy_unverified" || v.Reason != "legacy_engine" {
		t.Fatalf("view = %+v", v)
	}
	if f.mgr.Healthy() {
		t.Fatal("a legacy engine must never read healthy")
	}
}

func TestStatus_PortConflictAndContractUnsupportedAndUnverified(t *testing.T) {
	f := newFixture(t)
	// Foreign lease-aware process, nothing installed: port conflict.
	f.eng.set(func(h *mlsidecar.HealthPayload, down *bool) {
		*down = false
		*h = mlsidecar.HealthPayload{SidecarVersion: "9", ExePath: "/foreign/kameas-ml", LifecycleProtocol: 1}
	})
	if v, _ := f.impl.Status(context.Background()); v.State != "installed_unhealthy" || v.Reason != "port_conflict" {
		t.Fatalf("port conflict view = %+v", v)
	}
	// Engine speaking a contract major this build cannot.
	f.eng.set(func(h *mlsidecar.HealthPayload, down *bool) {
		*down = false
		*h = mlsidecar.HealthPayload{SidecarVersion: "9", LifecycleProtocol: 6}
	})
	if v, _ := f.impl.Status(context.Background()); v.State != "contract_unsupported" {
		t.Fatalf("contract view = %+v", v)
	}
}

func TestUpdate_NeverSilent_OnlyOnExplicitCall(t *testing.T) {
	f := newFixture(t)
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Harness build now pins a newer engine.
	rel := f.release("1.1.0")
	f.pin = &rel
	v, _ := f.impl.Status(context.Background())
	if !v.UpdateAvailable || v.InstalledVersion != "1.0.0" || v.Release.Version != "1.1.0" {
		t.Fatalf("view = %+v, want update_available with the installed version untouched", v)
	}
	if rec, _ := f.mgr.Installed(); rec.Version != "1.0.0" {
		t.Fatal("Status/observation must never update the engine")
	}
	v2, err := f.impl.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v2.InstalledVersion != "1.1.0" || v2.UpdateAvailable {
		t.Fatalf("after Update: %+v", v2)
	}
}

func TestUpdate_RefusedWhenNotNewerOrNotInstalled(t *testing.T) {
	f := newFixture(t)
	if _, err := f.impl.Update(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Update before install: %v", err)
	}
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.impl.Update(context.Background()); err == nil {
		t.Fatal("Update to the same pinned version must be refused (no downgrade/no-op install)")
	}
}

func TestUninstall_RemovesEverything(t *testing.T) {
	f := newFixture(t)
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Engine-written state lives under the install root too.
	if err := os.MkdirAll(filepath.Join(f.root, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "models", "w.bin"), []byte("w"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "lease", "shutdown.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := f.impl.Uninstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "not_installed" || v.Installed {
		t.Fatalf("after Uninstall: %+v", v)
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Fatalf("install root (process+weights+config) must be gone: %v", err)
	}
	f.eng.mu.Lock()
	defer f.eng.mu.Unlock()
	if f.eng.shutdowns != 1 {
		t.Fatalf("engine shutdown requests = %d, want 1", f.eng.shutdowns)
	}
}

func TestNewerThan(t *testing.T) {
	cases := []struct {
		cand, cur string
		want      bool
	}{
		{"1.1.0", "1.0.0", true}, {"1.0.0", "1.0.0", false}, {"1.0.0", "1.1.0", false},
		{"2.0.0", "1.9.9", true}, {"v1.2.0", "1.1.9", true}, {"1.0.0-rc1", "1.0.0", false},
		{"", "1.0.0", false}, {"abc", "1.0.0", true},
	}
	for _, c := range cases {
		if got := newerThan(c.cand, c.cur); got != c.want {
			t.Errorf("newerThan(%q,%q) = %v, want %v", c.cand, c.cur, got, c.want)
		}
	}
}

// TestStatus_SurfacesPausedLabelLanes: the WP14 pusher's "label lane
// paused" state reaches the panel (design A4/A3.3(5)) — sorted, typed,
// with the engine's code — instead of living only in a WARN log line.
func TestStatus_SurfacesPausedLabelLanes(t *testing.T) {
	f := newFixture(t)
	until := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	f.impl.Lanes = func() map[string]mlsidecar.LanePause {
		return map[string]mlsidecar.LanePause{
			"escalate_model": {Reason: "contract_mismatch", Code: "names_mismatch", Detail: "409", Until: until},
			"branch_now":     {Reason: "rows_refused", Code: "features_invalid", Until: until},
		}
	}
	v, err := f.impl.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(v.LabelLanes) != 2 {
		t.Fatalf("LabelLanes = %+v, want 2 entries", v.LabelLanes)
	}
	if v.LabelLanes[0].Kind != "branch_now" || v.LabelLanes[1].Kind != "escalate_model" {
		t.Fatalf("lanes not sorted by kind: %+v", v.LabelLanes)
	}
	if v.LabelLanes[1].Code != "names_mismatch" || v.LabelLanes[1].Until != "2026-09-30T12:00:00Z" {
		t.Fatalf("lane fields: %+v", v.LabelLanes[1])
	}
}

// TestUninstall_RewindsLabelCursor (v0.86.0 unwired sweep): Uninstall
// deletes the engine's retained-label mirror, so it must rewind the push
// cursor — otherwise a re-Enable starts an engine with an empty mirror
// while the cursor claims every label was delivered.
// LabelPusher.ResetCursor had no production caller before this.
func TestUninstall_RewindsLabelCursor(t *testing.T) {
	f := newFixture(t)
	resets := 0
	f.impl.ResetLabelCursor = func(context.Context) error { resets++; return nil }
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.impl.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resets != 1 {
		t.Fatalf("label cursor resets = %d, want 1 after a successful uninstall", resets)
	}

	// A failing reset must not turn a completed uninstall into an error.
	f2 := newFixture(t)
	f2.impl.ResetLabelCursor = func(context.Context) error { return errors.New("db locked") }
	if _, err := f2.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, err := f2.impl.Uninstall(context.Background()); err != nil || v.Installed {
		t.Fatalf("Uninstall with a failing cursor reset = (%+v, %v), want a clean uninstall", v, err)
	}
}

// TestEnable_NudgesLabelLaneOnceHealthy (v0.86.0 unwired sweep): the push
// lane only drains on a nudge, so a freshly healthy engine must be nudged
// or the backlog (and an uninstall's queued re-push) waits for an
// unrelated future label write. A refused action must not nudge.
func TestEnable_NudgesLabelLaneOnceHealthy(t *testing.T) {
	f := newFixture(t)
	nudges := 0
	f.impl.NudgeLabels = func() { nudges++ }
	if _, err := f.impl.Repair(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Repair before install: err = %v, want ErrNotInstalled", err)
	}
	if nudges != 0 {
		t.Fatalf("nudges = %d after a refused Repair, want 0", nudges)
	}
	v, err := f.impl.Enable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.State != string(mlsidecar.StateHealthy) {
		t.Fatalf("Enable left state %q, want healthy", v.State)
	}
	if nudges != 1 {
		t.Fatalf("nudges = %d after a healthy Enable, want 1", nudges)
	}
}
