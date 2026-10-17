package rpc

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/activity"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
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

// activityDBPath is where the local ML activity store lives
// (ml-producer-01MLPRD01 WP11): <dataDir>/ml/activity.db (activity.Path),
// which for the standard profile (~/.kenaz/harness/<env>) is separate from
// the shared engine root ~/.kenaz/ml/<env>. A non-standard data dir puts
// the engine's isolated install root AT <dataDir>/ml, which
// mlsidecar.Manager.Uninstall removes wholesale (ownsWholeRoot) and which
// boot must not create; the store then moves to
// <dataDir>/ml-activity/activity.db so an engine uninstall never deletes
// the user's activity. The engine is told the path either way
// (KENAZ_ML_ACTIVITY_DB), so it never assumes one.
func activityDBPath(dataDir string) string {
	p := activity.Path(dataDir)
	if filepath.Clean(sidecarRoot(dataDir)) == filepath.Dir(p) {
		alt := filepath.Join(dataDir, "ml-activity", activity.FileName)
		if abs, err := filepath.Abs(alt); err == nil {
			return abs
		}
		return alt
	}
	return p
}

// activityRetentionEnv is the engine's KENAZ_ML_ACTIVITY_RETENTION_DAYS
// entry: the EFFECTIVE days (0 → 90, clamped to 1..3650), the same window
// the harness prunes events/tasks to. read is the shared fail-safe
// Retention (sidecarRetentionReader), so a read error after a good read
// still yields the last good value. With no value at all (or no reader)
// it is activity.RetentionHold and logs a WARN: the engine prunes nothing
// this run (kenaz-ml contract; see activity.RetentionEnvVar). The
// spawner's ScrubEnv drops any inherited value, so this entry is the only
// one the engine sees.
func activityRetentionEnv(read func() (int, error)) []string {
	hold := []string{activity.RetentionEnvVar + "=" + activity.RetentionHold}
	if read == nil {
		return hold
	}
	days, err := read()
	if err != nil {
		slog.Warn("mlsidecar.spawn.activity_retention_unreadable")
		return hold
	}
	return []string{activity.RetentionEnvVar + "=" + strconv.Itoa(activity.EffectiveRetentionDays(days))}
}

// sidecarRetentionReader is the engine's spawn-time retention read: the
// ML producer's shared fail-safe Retention when there is one (a failed
// read then carries the last good value, so the engine's own pruning is
// never widened or shortened by it), else the settings API directly.
// With neither a fresh nor a last-good value the read fails and the
// engine is told to hold (activity.RetentionHold) — see
// activityRetentionEnv.
func sidecarRetentionReader(w *mlProducerWiring, s *settings.API) func() (int, error) {
	if r := w.retentionReader(); r != nil {
		return r
	}
	return settingsRetentionReader(s)
}

// settingsRetentionReader adapts the settings API (nil-safe: nil = no
// reader, so the engine is told to hold).
func settingsRetentionReader(s *settings.API) func() (int, error) {
	if s == nil {
		return nil
	}
	return s.MLLocalActivityRetentionDays
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
//
// retentionDays reads the local activity retention setting
// (settings.API.MLLocalActivityRetentionDays; nil = unknown, the engine is
// told to hold). It is read on every spawn.
func newSidecarStack(dataDir, buildVersion string, retentionDays func() (int, error)) (*mlsidecar.Manager, advice.SidecarProbe) {
	if dataDir == "" {
		return nil, nil
	}
	layout := mlsidecar.NewLayout(sidecarRoot(dataDir))
	// One engine per env per machine (Amendment A5(1), lanes per owner
	// ruling A5.2): the Manager scans the env's lane (base prod 7774 / dev
	// 7785 / test 7786, + k*10), arbitrating each candidate with the
	// engine identity check, spawns on the first free one (the spawner is
	// passed that port per spawn) and records it in <root>/engine.port.
	// engine.port is cross-client discovery only; requests go to the
	// Manager's in-memory verified port (Manager.DialClient). A dev build
	// never talks to — or port-conflicts with — the prod engine, and a
	// foreign listener on a base port (sigild on 7775 was the case that
	// forced this) no longer strands the engine.
	env := mlsidecar.EngineEnv()
	// The dev root also adopts engines a developer seeded from a local
	// build (scripts/dev-ml.sh); prod and test roots never do.
	layout.DeveloperBuilds = env == mlsidecar.EngineEnvDev
	m := mlsidecar.NewManager(layout,
		mlsidecar.NewClient(mlsidecar.BaseURLForEnv(env), nil),
		// WP11 (spec §12 A-16): the engine reads the harness's local
		// activity store as a second source — and only that file, never
		// data.db (least privilege).
		// The retention rides along, read fresh on each spawn: a change to
		// the setting reaches the engine at its next start (the engine is
		// shared with Kenaz and lease-managed, so the harness does not
		// restart it for a settings change).
		mlsidecar.ProcessSpawner{
			Layout:   layout,
			ExtraEnv: []string{activity.EnvVar + "=" + activityDBPath(dataDir)},
			EnvFunc:  func() []string { return activityRetentionEnv(retentionDays) },
			// The harness's own entry (days or "hold") is the only one the
			// engine sees: never a value the harness merely inherited from
			// its launcher's environment.
			ScrubEnv: []string{activity.RetentionEnvVar},
		},
		"harness", buildVersion)
	m.BasePort = mlsidecar.EnginePort(env)
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
// The checked-in release_signing_key.pub is the real Kameas ML release
// key (#388), so every build seeds it; the engine a build installs is
// pinned at CI build time (release.yml's engine-pin step, which checks the
// pinned release's key_id against this key). A build whose key is absent
// or blank (BakedReleaseAnchor ok=false) seeds nothing. Failures are
// logged, never fatal: without the anchor an engine install fails closed
// with anchor_missing.
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

// sidecarDialClient is the client the advice engine and the label pusher
// dial: Manager.DialClient, routed per request to the port whose engine
// the Manager last verified (never to whatever engine.port says — review
// F2). A nil Manager (nil-core chassis) falls back to the env's base port;
// nothing is healthy then, so nothing is sent.
func sidecarDialClient(m *mlsidecar.Manager) *mlsidecar.Client {
	if m == nil {
		return mlsidecar.NewClient(mlsidecar.DefaultEngineBaseURL(), nil)
	}
	return m.DialClient()
}
