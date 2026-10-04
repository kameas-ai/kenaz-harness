package rpc

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	coretrust "github.com/kameas-ai/kenaz-harness/core/trust"
)

// sidecarRootDirName is the isolated-fallback install root's directory
// name under a non-standard data dir (<DataDir>/ml). mlsidecar.Manager.
// Uninstall keys its "remove everything under the root" behavior on base
// name "ml" (and on ~/.kenaz/ml/<env>) — see mlsidecar.ownsWholeRoot and
// TestSidecarRoot.
const sidecarRootDirName = "ml"

// sidecarRoot returns the engine install root.
//
// STANDARD PROFILE (dataDir == paths.DataDir(), i.e. ~/.kenaz/harness/
// <env>): the RATIFIED shared root ~/.kenaz/ml/<env> (design Amendment
// A5(2)), <env> in {prod, dev, test} mapped from KENAZ_HARNESS_ENV by
// mlsidecar.EngineEnv — the same root Kenaz resolves, so the two clients
// share one verified engine and one lease directory.
//
// ANYTHING ELSE (a test chassis, an explicit custom data dir): the
// isolated <dataDir>/ml, so nothing outside that directory is ever read
// or written — tests never touch the real ~/.kenaz.
func sidecarRoot(dataDir string) string {
	if std, err := paths.DataDir(); err == nil && filepath.Clean(dataDir) == filepath.Clean(std) {
		if home, herr := os.UserHomeDir(); herr == nil {
			if root, rerr := mlsidecar.DefaultRootFor(home, mlsidecar.EngineEnv()); rerr == nil {
				return root
			}
		}
	}
	return filepath.Join(dataDir, sidecarRootDirName)
}

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
	// One engine per env per machine (Amendment A5(1)): the client dials,
	// and the spawner tells the engine to bind, the same env-mapped port
	// (prod 7774, dev 7775, test 7776), so a dev build never talks to —
	// or port-conflicts with — the prod engine.
	env := mlsidecar.EngineEnv()
	m := mlsidecar.NewManager(layout,
		mlsidecar.NewClient(mlsidecar.BaseURLForEnv(env), nil),
		mlsidecar.ProcessSpawner{Layout: layout, Port: mlsidecar.EnginePort(env)},
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

// seedBakedReleaseAnchor installs the compiled-in Kameas ML engine
// release public key (mlsidecar.BakedReleaseAnchor) into the trust store
// at boot so an un-enrolled, open-source/local-first install can verify
// the engine download (engine-publication-01ENPUB01 WP-H2, owner
// decision 2026-10-03 #4). trust.SeedAnchor yields to every existing
// row: an operator- or fleet-installed anchor for the same key or id is
// left alone, and a tombstoned (revoked) one stays revoked across boots.
// A placeholder build (the checked-in NOT-A-REAL-KEY file, no -ldflags
// override) seeds nothing. Failures are logged, never fatal: without the
// anchor an engine install fails closed with anchor_missing.
func seedBakedReleaseAnchor(ctx context.Context, engine coretrust.TrustEngine) coretrust.SeedOutcome {
	if engine == nil {
		return ""
	}
	anchor, ok, err := mlsidecar.BakedReleaseAnchor()
	if err != nil {
		slog.Warn("baked ML release signing key is malformed; not seeding a trust anchor", "err", err)
		return ""
	}
	if !ok {
		return ""
	}
	outcome, err := coretrust.SeedAnchor(ctx, engine, anchor)
	if err != nil {
		slog.Warn("seeding the baked ML release trust anchor failed", "anchor_id", anchor.AnchorID, "err", err)
		return ""
	}
	slog.Info("baked ML release trust anchor", "anchor_id", anchor.AnchorID, "key_id", anchor.PublicKey.Fingerprint, "outcome", string(outcome))
	return outcome
}
