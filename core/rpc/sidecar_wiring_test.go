package rpc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	sidecarview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sidecar"
	coretrust "github.com/kameas-ai/kenaz-harness/core/trust"
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

// TestSidecarRoot pins the ratified root (Amendment A5(2)): the STANDARD
// profile resolves ~/.kenaz/ml/<env> with <env> mapped from
// KENAZ_HARNESS_ENV; any custom data dir stays isolated under <dataDir>/ml.
func TestSidecarRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for raw, env := range map[string]string{"": "prod", "prod": "prod", "stage": "prod", "dev": "dev", "local": "dev", "test": "test"} {
		t.Setenv("KENAZ_HARNESS_ENV", raw)
		std, err := paths.DataDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".kenaz", "ml", env)
		if got := sidecarRoot(std); got != want {
			t.Errorf("KENAZ_HARNESS_ENV=%q: sidecarRoot(%s) = %q, want %q", raw, std, got, want)
		}
		if !ownsWholeRootForTest(want) {
			t.Errorf("the ratified root %q must be one Manager.Uninstall owns wholesale", want)
		}
	}
	// A custom data dir never reaches ~/.kenaz/ml.
	if got, want := sidecarRoot("/custom/data"), filepath.Join("/custom/data", "ml"); got != want {
		t.Errorf("custom data dir root = %q, want %q", got, want)
	}
}

// ownsWholeRootForTest asserts (via the public behavior) that Uninstall
// removes a root wholesale: a throwaway Manager on a copy of the path
// shape, with an engine-written file that must disappear.
func ownsWholeRootForTest(shape string) bool {
	tmp, err := os.MkdirTemp("", "sidecar-own-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, filepath.Base(filepath.Dir(shape)), filepath.Base(shape))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(root, "engine-state.bin"), []byte("x"), 0o600); err != nil {
		return false
	}
	// WP13-review hardening: whole-root removal requires a KNOWN install
	// record at entry, not just the ratified path shape — so the helper
	// models the realistic case (something was actually installed here).
	l := mlsidecar.NewLayout(root)
	exeDir := filepath.Join(l.VersionDir("1.0.0"), "kameas-ml")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(exeDir, "kameas-ml"), []byte("launcher"), 0o755); err != nil {
		return false
	}
	if err := l.SetCurrent("1.0.0"); err != nil {
		return false
	}
	tree, err := mlsidecar.TreeDigest(l.OnedirPath("1.0.0"))
	if err != nil {
		return false
	}
	if err := mlsidecar.WriteInstallJSON(l, mlsidecar.InstallRecord{Version: "1.0.0", Verified: true, Source: "test",
		TreeSHA256: tree, Provenance: mlsidecar.ProvenanceChannelManifest, InstalledBy: "harness"}); err != nil {
		return false
	}
	m := mlsidecar.NewManager(l, nil, nil, "harness", "v")
	if err := m.Uninstall(context.Background()); err != nil {
		return false
	}
	_, statErr := os.Stat(root)
	return os.IsNotExist(statErr)
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
	// The honest-unavailable assertions below are about a build with NO
	// engine pin; force that, independent of the checked-in pin.
	defer mlsidecar.SetPinnedReleaseForTesting(mlsidecar.EngineRelease{})()
	api, dataDir := newSidecarTestAPI(t)
	if api.sidecarMgr == nil {
		t.Fatal("New(c) with a data dir left the sidecar Manager nil")
	}
	if got, want := api.sidecarMgr.Layout.Root, filepath.Join(dataDir, "ml"); got != want {
		t.Fatalf("install root = %q, want %q (custom data dir stays isolated; the standard profile resolves ~/.kenaz/ml/<env>, see TestSidecarRoot)", got, want)
	}
	env := mlsidecar.EngineEnv()
	if got, want := api.sidecarMgr.Client.URL(), mlsidecar.BaseURLForEnv(env); got != want {
		t.Fatalf("manager client base = %q, want the env-mapped base %q (Amendment A5(1))", got, want)
	}
	if got, want := api.sidecarMgr.BasePort, mlsidecar.EnginePort(env); got != want {
		t.Fatalf("Manager.BasePort = %d, want the env's lane base %d (lane mode, owner ruling A5.2)", got, want)
	}
	// The advice/label dial client routes to the Manager's VERIFIED port
	// only — a recorded engine.port never redirects it (review F2).
	lane := mlsidecar.CandidatePorts(mlsidecar.EnginePort(env))[2]
	if err := mlsidecar.WriteEnginePort(api.sidecarMgr.Layout, lane); err != nil {
		t.Fatal(err)
	}
	if got := sidecarDialClient(api.sidecarMgr).URL(); got == mlsidecar.LoopbackURL(lane) || got == mlsidecar.BaseURLForEnv(env) {
		t.Fatalf("unverified dial client dials %q; it must fail closed, never follow engine.port or the base", got)
	}
	if err := os.RemoveAll(api.sidecarMgr.Layout.Root); err != nil {
		t.Fatal(err)
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
	// test never dials a real engine port (fixed-endpoint mode: no lane scan).
	eng := newCountingEngineRPC(t)
	api.sidecarMgr.Client = mlsidecar.NewClient(eng.srv.URL, nil)
	api.sidecarMgr.BasePort = 0
	eng.health.Store(mlsidecar.HealthPayload{}) // bare {} health -> never adopted
	// The platform gate is device-dependent (CI runs linux/arm64, where
	// compose's !Supported branch correctly wins) — force supported so
	// the assertions below reach the release-pin reason it pins.
	api.sidecarAPI.(*sidecarview.Impl).Platform = func() (bool, string) { return true, "" }
	v, err := api.Sidecar().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The checked-in engine pin (mlsidecar pinned_release_gen.go) is zero —
	// a real pin is written only at release-build time by release.yml's
	// engine-pin step — so a test build honestly reports it unavailable.
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
	tree, err := mlsidecar.TreeDigest(layout.OnedirPath("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := mlsidecar.WriteInstallJSON(layout, mlsidecar.InstallRecord{Version: "1.0.0", EngineSHA256: sha, Verified: true, Source: "test",
		TreeSHA256: tree, Provenance: mlsidecar.ProvenanceChannelManifest, InstalledBy: "harness"}); err != nil {
		t.Fatal(err)
	}
	eng := newCountingEngineRPC(t)
	eng.health.Store(mlsidecar.HealthPayload{
		Product: "kameas-ml", SidecarVersion: "1.0.0", ExePath: exe, EngineSHA256: sha,
		LifecycleProtocol: 1,
	})
	mgr.Client = mlsidecar.NewClient(eng.srv.URL, nil)
	mgr.BasePort = 0 // fixed-endpoint mode: dial the double, never scan real lane ports

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
	var model string
	var rung advice.ModelRung
	var ok bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, model, rung, _, ok = advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, probe)
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok || rung != advice.RungLocalLaya || model != "kenaz-ml-sidecar@1.0.0" {
		t.Fatalf("ladder = (%q, %q, %v), want rung 2 via the production probe", model, rung, ok)
	}
}

// TestSidecarWiring_BakedReleaseKeySeededAtBoot pins WP-H2's boot wiring
// (engine-publication-01ENPUB01): a build carrying a real baked release
// key boots with that key as a trust anchor, and the explicit install
// path's VerifierFunc hands it to the engine verifier — so an un-enrolled
// install verifies the engine download with no operator step. A
// placeholder build seeds nothing. (Revocation-across-boots and
// operator-anchor precedence are pinned against real sqlite in
// core/trust/seed_test.go.)
func TestSidecarWiring_BakedReleaseKeySeededAtBoot(t *testing.T) {
	ctx := context.Background()

	t.Run("placeholder seeds nothing", func(t *testing.T) {
		restore := mlsidecar.SetBakedReleaseKeyForTesting("")
		defer restore()
		api, _ := newSidecarTestAPI(t)
		anchors, err := api.TrustAnchors().ListAnchors(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range anchors {
			if strings.HasPrefix(a.AnchorID, "kameas-ml-release-") {
				t.Fatalf("placeholder build seeded %q", a.AnchorID)
			}
		}
	})

	t.Run("real key is seeded and reaches the install verifier", func(t *testing.T) {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		restore := mlsidecar.SetBakedReleaseKeyForTesting(hex.EncodeToString(pub))
		defer restore()
		api, _ := newSidecarTestAPI(t)
		want, ok, err := mlsidecar.BakedReleaseAnchor()
		if err != nil || !ok {
			t.Fatalf("BakedReleaseAnchor: ok=%v err=%v", ok, err)
		}
		anchors, err := api.TrustAnchors().ListAnchors(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range anchors {
			if a.AnchorID == want.AnchorID && a.PublicKey.Fingerprint == want.PublicKey.Fingerprint {
				found = true
			}
		}
		if !found {
			t.Fatalf("baked anchor %s not in the boot trust store: %+v", want.AnchorID, anchors)
		}
		if api.sidecarMgr == nil || api.sidecarMgr.VerifierFunc == nil {
			t.Fatal("sidecar install deps not attached")
		}
		v, err := api.sidecarMgr.VerifierFunc(ctx)
		if err != nil {
			t.Fatal(err)
		}
		inVerifier := false
		for _, a := range v.Anchors {
			if a.PublicKey.Fingerprint == want.PublicKey.Fingerprint {
				inVerifier = true
			}
		}
		if !inVerifier {
			t.Fatal("the install VerifierFunc does not carry the baked anchor")
		}
		// Re-running the boot seed is a no-op, never a rewrite.
		if got := seedBakedReleaseAnchor(ctx, api.trustEngine); got != coretrust.SeedAlreadyPresent {
			t.Fatalf("second seed outcome = %q, want already_present", got)
		}
	})
}
