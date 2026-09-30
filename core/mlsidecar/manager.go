package mlsidecar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Spawner launches the verified engine executable at exePath, detached
// in its own process group (design §3.5: "spawn detached (own process
// group) from `current`; supervisor sets LAYA_THREADS <= physical
// cores"), and returns its pid. Production wiring (a later WP, once a
// real kenaz-ml artifact exists — design §9 Phase 1) implements this
// with a real process launch; WP12's own tests inject a fake that never
// execs anything — the brief's hard constraint is "NO real sidecar
// spawn in tests (no Python anywhere)".
type Spawner interface {
	Spawn(ctx context.Context, exePath string) (pid int, err error)
}

// SpawnerFunc adapts a function to Spawner.
type SpawnerFunc func(ctx context.Context, exePath string) (int, error)

// Spawn implements Spawner.
func (f SpawnerFunc) Spawn(ctx context.Context, exePath string) (int, error) { return f(ctx, exePath) }

// Manager is the harness-side lifecycle orchestrator: adopt-or-spawn,
// the health/state machine, lease heartbeats, and uninstall. One Manager
// per (Layout, Client) pair — production wiring constructs exactly one
// per process, matching the one-instance strategy design §3.6 describes
// at the sidecar level.
type Manager struct {
	Layout   Layout
	Client   *Client
	ClientID string // stable client id for the lease file name, e.g. "harness"
	Version  string // this client build's own version, sent on lease acquisition
	Spawner  Spawner

	// Registry/Creds/Verifier are consulted only by InstallAndActivate /
	// UpdateAndActivate (the explicit, user-initiated install/update
	// path — design §6.2). Reconcile (lazy start / health poll) never
	// touches them: it only adopts-or-spawns an ALREADY-installed engine.
	Registry channels.Registry
	Creds    secrets.ResolverAPI
	Verifier Verifier

	mu     sync.Mutex
	status Status
}

// NewManager constructs a Manager with an initial not_installed status.
// clientID and version are recorded on every lease file this Manager
// writes.
func NewManager(layout Layout, client *Client, spawner Spawner, clientID, version string) *Manager {
	return &Manager{
		Layout:   layout,
		Client:   client,
		ClientID: clientID,
		Version:  version,
		Spawner:  spawner,
		status:   Status{State: StateNotInstalled, UpdatedAt: time.Now()},
	}
}

// Status returns a snapshot of the Manager's current, honest state.
// Never triggers a network call or a spawn — that is Reconcile's job.
// Constructing a Manager and calling Status() immediately (with no
// Reconcile ever run) is the "app boot" case design §2c/§6.2 requires to
// cost nothing: StateNotInstalled, no probe, no idle RAM.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Reconcile is the single entry point for both "lazy start on first
// advisor demand" (design §2c: "start on demand (first advisor call, not
// at app boot)") and the periodic 30s health-poll cadence a later
// production wiring drives on a timer (design §3.5). Both call sites are
// the SAME function so there is exactly one place that decides adopt vs.
// spawn vs. surface-a-failure — Ensure is provided as an alias for
// callers that want to express intent ("I am asking because I need
// advice right now").
func (m *Manager) Reconcile(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileLocked(ctx)
}

// Ensure is Reconcile under the name the lazy-start call site reads
// correctly at (design §2c/§9: "Lazy start on first advisor demand").
func (m *Manager) Ensure(ctx context.Context) Status { return m.Reconcile(ctx) }

func (m *Manager) reconcileLocked(ctx context.Context) Status {
	if m.Client == nil {
		m.status = Status{State: StateNotInstalled, Detail: "no client configured", UpdatedAt: time.Now()}
		return m.status
	}

	health, err := m.Client.Health(ctx)
	if err == nil {
		m.status = m.evaluateRunning(health)
		return m.status
	}
	if errors.Is(err, ErrUnusableResponse) {
		// Something IS listening — it just did not answer /health with a
		// payload this client can read (a foreign process, or an engine
		// whose /health shape drifted from this build's). The port is
		// taken: spawning here would only launch a second engine that
		// cannot bind. Never adopted, never spawned over, never killed.
		m.status = Status{State: StateInstalledUnhealthy, Reason: ReasonPortConflict,
			Detail: "a process answered /health but not with a usable health payload: " + err.Error(), UpdatedAt: time.Now()}
		return m.status
	}

	// Nothing answered /health: either nothing is installed, or an
	// installed engine is not currently running and this client should
	// spawn it (adopt-or-spawn, design §3.5).
	m.status = m.spawnLocked(ctx)
	return m.status
}

// evaluateRunning handles the "something answered /health" branch via
// EvaluateAdoption (design F2), mapping its verdict onto the state
// machine and renewing this client's lease whenever the result is a
// verified, usable install (design §3.7 R4: "every /health poll counts
// as an implicit 90s lease").
//
// AMENDED (2026-09-29 security-review ruling): AdoptLegacyUnverified
// maps to StateLegacyUnverified, NOT StateHealthy, and does NOT renew
// this client's lease — a legacy engine is never usable for
// recommendations (see StateLegacyUnverified's doc comment), so this
// client has no relationship with it worth advertising via a lease.
func (m *Manager) evaluateRunning(health HealthPayload) Status {
	decision, err := EvaluateAdoption(m.Layout, health)
	if err != nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: err.Error(), UpdatedAt: time.Now()}
	}
	now := time.Now()
	switch decision.Action {
	case AdoptAccept:
		m.renewLease()
		return Status{State: StateHealthy, EngineVersion: health.SidecarVersion, ContractVersion: health.LifecycleProtocol, Detail: decision.Detail, UpdatedAt: now}
	case AdoptLegacyUnverified:
		// Never healthy, never leased: adopt-only in the narrow sense of
		// "never terminate, never double-spawn" — the advisor ladder must
		// fall through to RungNone for this sidecar (SidecarProbe.Healthy()
		// reads State != StateHealthy).
		return Status{State: StateLegacyUnverified, Reason: ReasonLegacyEngine, EngineVersion: health.SidecarVersion, Detail: decision.Detail, UpdatedAt: now}
	case AdoptContractUnsupported:
		return Status{State: StateContractUnsupported, EngineVersion: health.SidecarVersion, Detail: decision.Detail, UpdatedAt: now}
	case AdoptPortConflict:
		return Status{State: StateInstalledUnhealthy, Reason: ReasonPortConflict, Detail: decision.Detail, UpdatedAt: now}
	case AdoptRefuseUnverified:
		return Status{State: StateUnverified, Reason: decision.Reason, Detail: decision.Detail, UpdatedAt: now}
	default:
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "unrecognized adoption verdict", UpdatedAt: now}
	}
}

// spawnLocked handles the "/health is unreachable" branch: nothing is
// currently listening on :7774. If nothing is installed at all, that is
// simply StateNotInstalled (installation is a distinct, explicit user
// action — design §6.2 — never triggered implicitly by a failed health
// probe). If an engine IS installed, this client spawns it under the
// O_EXCL spawn lock (design §3.5), then health-checks once.
func (m *Manager) spawnLocked(ctx context.Context) Status {
	now := time.Now()
	rec, ok, err := ReadInstallJSON(m.Layout)
	if err != nil || !ok || !rec.Verified {
		return Status{State: StateNotInstalled, UpdatedAt: now}
	}
	currentDir, err := m.Layout.CurrentVersionDir()
	if err != nil {
		return Status{State: StateNotInstalled, Detail: err.Error(), UpdatedAt: now}
	}
	exePath := pathUnderVersionsDir(m.Layout, filepath.Base(currentDir))

	lock, lerr := AcquireSpawnLock(m.Layout, os.Getpid(), processAlive)
	if lerr != nil {
		// Another live process is already spawning — report "installing"
		// rather than racing it (design §3.5: spawn is serialized).
		return Status{State: StateInstalling, Detail: "another process is spawning the engine", UpdatedAt: now}
	}
	defer lock.Release()

	if m.Spawner == nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "no spawner configured", UpdatedAt: now}
	}
	if _, serr := m.Spawner.Spawn(ctx, exePath); serr != nil {
		logging.L().Warn("mlsidecar.spawn_failed", "exe", exePath, "err", serr.Error())
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: serr.Error(), UpdatedAt: now}
	}

	health, herr := m.Client.Health(ctx)
	if herr != nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "spawned but did not become healthy: " + herr.Error(), UpdatedAt: now}
	}
	m.renewLease()
	return Status{State: StateHealthy, EngineVersion: health.SidecarVersion, ContractVersion: health.LifecycleProtocol, Detail: "spawned", UpdatedAt: time.Now()}
}

// renewLease piggybacks the harness's own file-lease heartbeat onto a
// successful health check (design §3.7 R4).
func (m *Manager) renewLease() {
	if m.ClientID == "" {
		return
	}
	if err := RenewLease(m.Layout, m.ClientID, os.Getpid(), m.Version); err != nil {
		logging.L().Warn("mlsidecar.lease.renew_failed", "client", m.ClientID, "err", err.Error())
	}
}

// InstallAndActivate implements the explicit, user-initiated install
// flow (design §6.2: the "Enable local recommendations" button) —
// distinct from Reconcile's implicit adopt-or-spawn, which never
// downloads anything. While the download+verify+unpack is in flight the
// Manager honestly reports StateInstalling; on success it immediately
// reconciles (spawns/health-checks the newly-activated version) so the
// caller gets back a real StateHealthy/StateInstalledUnhealthy rather
// than having to poll separately.
func (m *Manager) InstallAndActivate(ctx context.Context, req InstallRequest) Status {
	m.mu.Lock()
	m.status = Status{State: StateInstalling, Detail: "downloading " + req.Version, UpdatedAt: time.Now()}
	m.mu.Unlock()

	if _, err := Install(ctx, m.Layout, m.Registry, m.Creds, m.Verifier, req); err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.status = Status{State: StateInstalledUnhealthy, Reason: ReasonDigestMismatch, Detail: err.Error(), UpdatedAt: time.Now()}
		return m.status
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconcileLocked(ctx)
}

// UpdateAndActivate implements design §3.5/§3.7 R6's flip-and-respawn,
// through the Manager so the resulting state is observable the same way
// InstallAndActivate's is.
func (m *Manager) UpdateAndActivate(ctx context.Context, req InstallRequest) (UpdateResult, Status) {
	m.mu.Lock()
	m.status = Status{State: StateInstalling, Reason: ReasonUpdatePending, Detail: "updating to " + req.Version, UpdatedAt: time.Now()}
	m.mu.Unlock()

	res := Update(ctx, m.Layout, m.Registry, m.Creds, m.Verifier, m.Client, req)

	m.mu.Lock()
	defer m.mu.Unlock()
	if res.Install.Record.Version == "" {
		m.status = Status{State: StateInstalledUnhealthy, Reason: ReasonUpdatePending, Detail: "update failed, old version still current", UpdatedAt: time.Now()}
		return res, m.status
	}
	m.status = m.reconcileLocked(ctx)
	return res, m.status
}

// Shutdown releases this client's own lease WITHOUT touching the running
// process — a "clean stop on app exit" (spec §2c) means "stop pinning
// the sidecar alive", not "kill it": other clients (or this one's own
// next launch) may still be using it, and the sidecar's own 120s
// zero-lease self-termination (design §3.5) is its job, not this
// client's.
func (m *Manager) Shutdown(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClientID == "" {
		return nil
	}
	return ReleaseLease(m.Layout, m.ClientID)
}

// Uninstall implements the WP12 checklist item (design §6.2 step 5):
// "release lease + remove version dirs + (if sole leaseholder)
// token-authorized drained stop". Leaves no process, weights, or config
// behind under this client's install root.
func (m *Manager) Uninstall(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ClientID != "" {
		_ = ReleaseLease(m.Layout, m.ClientID)
	}

	sole := true
	if entries, err := os.ReadDir(m.Layout.LeaseDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() && isLeaseFileName(e.Name()) {
				sole = false
				break
			}
		}
	}
	if sole && m.Client != nil {
		if token, ok, _ := ReadLocalToken(m.Layout); ok {
			_ = m.Client.Shutdown(ctx, token)
		}
	}

	for _, d := range []string{m.Layout.VersionsDir(), m.Layout.CheckpointsDir(), m.Layout.LeaseDir()} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	if err := os.Remove(m.Layout.CurrentLink()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(m.Layout.InstallJSONPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	m.status = Status{State: StateNotInstalled, UpdatedAt: time.Now()}
	return nil
}
