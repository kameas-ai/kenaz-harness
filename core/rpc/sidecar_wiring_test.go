package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	sidecarview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sidecar"
)

// countingEngine answers /health from a scripted payload and counts hits,
// so "nothing dials the port" is asserted as a number.
type countingEngineRPC struct {
	hits   atomic.Int64
	health atomic.Value // mlsidecar.HealthPayload
	srv    *httptest.Server
}

func newCountingEngineRPC(t *testing.T) *countingEngineRPC {
	t.Helper()
	e := &countingEngineRPC{}
	e.health.Store(mlsidecar.HealthPayload{})
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		e.hits.Add(1)
		_ = json.NewEncoder(w).Encode(e.health.Load().(mlsidecar.HealthPayload))
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func newSidecarTestAPI(t *testing.T) (*API, string) {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)
	return api, dataDir
}

func TestSidecarRoot_IsDataDirMl(t *testing.T) {
	// Manager.Uninstall's "remove everything" branch keys on base name
	// "ml"; the wiring's root must keep that exact base name.
	if got := sidecarRoot("/d/harness/prod"); got != filepath.Join("/d/harness/prod", "ml") || filepath.Base(got) != "ml" {
		t.Fatalf("sidecarRoot = %q", got)
	}
}

func TestSidecarWiring_NilCore_NoManagerAndNilInterfaceProbe(t *testing.T) {
	api := New(nil, WithSettingsStore(newTestStore(t)))
	if api.sidecarMgr != nil {
		t.Fatal("nil-core chassis constructed a sidecar manager")
	}
	if api.sidecarProbe != nil {
		t.Fatalf("nil-core sidecarProbe = %#v; must be a true nil interface (typed-nil trap)", api.sidecarProbe)
	}
	v, err := api.Sidecar().Status(context.Background())
	if err != nil || v.Available || v.State != "not_installed" {
		t.Fatalf("nil-core Status = %+v, %v", v, err)
	}
	if m, p := newSidecarStack("", "v"); m != nil || p != nil {
		t.Fatalf("newSidecarStack(\"\") = %v, %v; want (nil, nil-interface)", m, p)
	}
}

// TestSidecarWiring_RealManagerUnderDataDir is the replacement proof for
// the WP12 dated nil: New() constructs a REAL Manager rooted at
// <DataDir>/ml, exposes the demand-driven probe, attaches the install
// collaborators — and constructing all of it touches nothing on disk and
// dials nothing (no boot-time spawn).
func TestSidecarWiring_RealManagerUnderDataDir(t *testing.T) {
	api, dataDir := newSidecarTestAPI(t)
	if api.sidecarMgr == nil {
		t.Fatal("New(c) with a data dir left the sidecar Manager nil")
	}
	if got, want := api.sidecarMgr.Layout.Root, filepath.Join(dataDir, "ml"); got != want {
		t.Fatalf("install root = %q, want %q (the ~/.kenaz per-env data-dir family)", got, want)
	}
	if _, ok := api.sidecarProbe.(*mlsidecar.DemandProbe); !ok {
		t.Fatalf("sidecarProbe = %T, want the demand-driven *mlsidecar.DemandProbe", api.sidecarProbe)
	}
	if api.sidecarMgr.Registry == nil || api.sidecarMgr.VerifierFunc == nil || api.sidecarMgr.Creds == nil {
		t.Fatal("install collaborators (channel registry, trust verifier, creds) were not attached")
	}
	if _, ok := api.sidecarMgr.Spawner.(mlsidecar.ProcessSpawner); !ok {
		t.Fatalf("Spawner = %T, want the production mlsidecar.ProcessSpawner", api.sidecarMgr.Spawner)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "ml")); !os.IsNotExist(err) {
		t.Fatalf("boot created the install root (stat err=%v) — nothing may be written until the user enables", err)
	}
	if api.sidecarMgr.Status().State != mlsidecar.StateNotInstalled {
		t.Fatalf("boot status = %+v", api.sidecarMgr.Status())
	}

	// Point the client at a counting double for the read-only calls so this
	// test never dials the real :7774.
	eng := newCountingEngineRPC(t)
	api.sidecarMgr.Client = mlsidecar.NewClient(eng.srv.URL, nil)
	eng.health.Store(mlsidecar.HealthPayload{}) // bare {} health -> never adopted
	v, err := api.Sidecar().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Production pin is honestly unavailable until the release channel
	// publishes the engine (dated note in mlsidecar.PinnedEngineRelease).
	if v.Available || !strings.Contains(v.UnavailableReason, "not been published") {
		t.Fatalf("view = %+v, want the honest no-published-release reason", v)
	}
	if _, err := api.Sidecar().Enable(context.Background()); !errors.Is(err, sidecarview.ErrUnavailable) {
		t.Fatalf("Enable err = %v, want sidecar ErrUnavailable", err)
	}
	if v.State != "legacy_unverified" {
		t.Fatalf("a bare-{} engine on the port must read legacy_unverified, got %q", v.State)
	}
}

// TestSidecarWiring_VerifierFuncRefusesWithoutTrustEngine: the explicit
// install path must fail closed when the trust engine is missing — never
// degrade to an unverified install.
func TestSidecarWiring_VerifierFuncRefusesWithoutTrustEngine(t *testing.T) {
	m := mlsidecar.NewManager(mlsidecar.NewLayout(filepath.Join(t.TempDir(), "ml")), nil, nil, "harness", "v")
	attachSidecarInstallDeps(m, nil, nil)
	if _, err := m.VerifierFunc(context.Background()); err == nil {
		t.Fatal("VerifierFunc must error when the trust engine is unavailable")
	}
	attachSidecarInstallDeps(nil, nil, nil) // nil-safe
}

// TestSidecarWiring_LadderResolvesRung2ThroughProductionProbe is the
// end-to-end ladder proof through the PRODUCTION wiring: a verified install
// under <DataDir>/ml + a healthy engine double => the very probe
// newLLMStack hands the advisors resolves rung 2 after its first (lazy)
// demand; the boot-time resolve (cache-only Manager) did not.
func TestSidecarWiring_LadderResolvesRung2ThroughProductionProbe(t *testing.T) {
	api, _ := newSidecarTestAPI(t)
	mgr := api.sidecarMgr
	layout := mgr.Layout

	// A verified install, as the install flow leaves it.
	exe := filepath.Join(layout.VersionDir("1.0.0"), "kameas-ml", "kameas-ml")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := mlsidecar.HashFileSHA256(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.SetCurrent("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := mlsidecar.WriteInstallJSON(layout, mlsidecar.InstallRecord{Version: "1.0.0", EngineSHA256: sha, Verified: true, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	eng := newCountingEngineRPC(t)
	eng.health.Store(mlsidecar.HealthPayload{
		Product: "kameas-ml", SidecarVersion: "1.0.0", ExePath: exe, EngineSHA256: sha,
		ContractVersions: map[string]int{"api": mlsidecar.SupportedContractMajor}, LifecycleProtocol: 1,
	})
	mgr.Client = mlsidecar.NewClient(eng.srv.URL, nil)

	// Boot-style resolve (cache-only) never reaches rung 2, never dials.
	if _, _, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, mgr); ok || rung == advice.RungLocalLaya {
		t.Fatalf("cache-only boot resolve reached rung 2 (rung=%q ok=%v)", rung, ok)
	}
	if eng.hits.Load() != 0 {
		t.Fatalf("boot-style resolve dialed the engine %d time(s)", eng.hits.Load())
	}

	// First advisor demand: falls through (per-call fallback) but starts the
	// lazy background Ensure; once it lands the ladder resolves rung 2.
	probe := api.sidecarProbe.(*mlsidecar.DemandProbe)
	if _, _, _, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, probe); ok {
		t.Log("first demand already healthy (engine answered before the call returned) — acceptable")
	}
	probe.Wait()
	_, model, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, probe)
	if !ok || rung != advice.RungLocalLaya || model != "kenaz-ml-sidecar@1.0.0" {
		t.Fatalf("ladder = (%q, %q, %v), want rung 2 via the production probe", model, rung, ok)
	}
}
