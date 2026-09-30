package rpc

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	coretrust "github.com/kameas-ai/kenaz-harness/core/trust"
)

// sidecarRootDirName is the install root's directory name under the
// harness's per-environment data dir (<DataDir>/ml — the ~/.kenaz family,
// owner ruling 2026-09-30 / design Amendment A2). mlsidecar.Manager.
// Uninstall keys its "remove everything under the root" behavior on this
// exact base name, so the two must not drift: see
// TestSidecarRoot_IsDataDirMl.
const sidecarRootDirName = "ml"

// sidecarRoot returns the engine install root for a data dir.
func sidecarRoot(dataDir string) string { return filepath.Join(dataDir, sidecarRootDirName) }

// newSidecarStack constructs the process's single *mlsidecar.Manager and
// its advisor-facing probe (laya-advisors-01LAYA001 WP13) — replacing the
// dated nil sidecarProbe WP12 left behind. dataDir == "" (nil-core test
// chassis) returns (nil, nil): no manager, and — crucially — a NIL
// interface probe, never a typed-nil one (advice.ResolveAdvisorModel and
// the WP14/15 consumers compare against nil).
//
// Nothing here starts, dials or spawns anything. The Manager is demand-
// driven (lazy start on first advisor demand, design §2c): the returned
// probe schedules a throttled background Ensure only when an advisor
// actually asks AND an engine is installed; the Settings surface drives
// the explicit install/uninstall through the same Manager.
//
// Registry / Creds / VerifierFunc are attached later by New()
// (attachSidecarInstallDeps) once the bundle channel registry and trust
// engine exist — they are only read by the explicit install path.
func newSidecarStack(dataDir, buildVersion string) (*mlsidecar.Manager, advice.SidecarProbe) {
	if dataDir == "" {
		return nil, nil
	}
	layout := mlsidecar.NewLayout(sidecarRoot(dataDir))
	m := mlsidecar.NewManager(layout,
		mlsidecar.NewClient(mlsidecar.DefaultBaseURL, nil),
		mlsidecar.ProcessSpawner{Layout: layout},
		"harness", buildVersion)
	// A real PyInstaller engine needs seconds to bind its port.
	m.StartupWait = 30 * time.Second
	return m, &mlsidecar.DemandProbe{M: m}
}

// attachSidecarInstallDeps wires the explicit install path's collaborators:
// the same channel registry the bundle installer uses (http_mirror on the
// env-specific download channels) and an engine Verifier built FRESH per
// install from the trust engine's current anchors with SigningRequired —
// "no second verifier" (design §3.2). An unavailable trust engine makes
// the install refuse (Manager.InstallAndActivate maps a VerifierFunc error
// to a verification failure); it never degrades to an unverified install.
func attachSidecarInstallDeps(m *mlsidecar.Manager, registry channels.Registry, engine coretrust.TrustEngine) {
	if m == nil {
		return
	}
	m.Registry = registry
	m.Creds = secrets.NoopResolver{}
	m.VerifierFunc = func(ctx context.Context) (mlsidecar.Verifier, error) {
		if engine == nil {
			return mlsidecar.Verifier{}, errors.New("trust engine unavailable: cannot verify the ML engine artifact")
		}
		v, err := coretrust.NewEngineVerifier(engine)
		if err != nil {
			return mlsidecar.Verifier{}, err
		}
		anchors, err := engine.ListAnchors(ctx)
		if err != nil {
			return mlsidecar.Verifier{}, err
		}
		return mlsidecar.DefaultEngineVerifier(v, anchors), nil
	}
}
