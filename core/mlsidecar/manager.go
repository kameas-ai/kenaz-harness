package mlsidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Spawner launches the verified engine executable at exePath, telling it
// to bind loopback port (`serve --port <port>`; 0 = the spawner's/engine's
// own default — only the fixed-endpoint test mode passes 0), detached
// in its own process group (design §3.5: "spawn detached (own process
// group) from `current`; supervisor sets LAYA_THREADS <= physical
// cores"), and returns its pid. Production wiring (a later WP, once a
// real kenaz-ml artifact exists — design §9 Phase 1) implements this
// with a real process launch; WP12's own tests inject a fake that never
// execs anything — the brief's hard constraint is "NO real sidecar
// spawn in tests (no Python anywhere)".
type Spawner interface {
	Spawn(ctx context.Context, exePath string, port int) (pid int, err error)
}

// SpawnerFunc adapts a function to Spawner.
type SpawnerFunc func(ctx context.Context, exePath string, port int) (int, error)

// Spawn implements Spawner.
func (f SpawnerFunc) Spawn(ctx context.Context, exePath string, port int) (int, error) {
	return f(ctx, exePath, port)
}

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

	// VerifierFunc, when non-nil, resolves the Verifier at install/update
	// time instead of the static Verifier field — production wiring uses
	// it because the trust anchors live in a store that is read fresh per
	// install (WP13). A non-nil error aborts the install with a
	// verification failure; it never falls back to an unverified install.
	VerifierFunc func(ctx context.Context) (Verifier, error)

	// Mounter is handed to Install for .dmg artifacts (nil = hdiutil).
	Mounter DMGMounter

	// StartupWait bounds how long spawnLocked polls /health after
	// launching the engine. A real PyInstaller engine needs seconds to
	// come up; the zero value (one probe, no wait) is what the stub-based
	// tests use. StartupPoll is the poll interval (default 250ms).
	StartupWait time.Duration
	StartupPoll time.Duration

	// BasePort switches the Manager into LANE mode (owner ruling A5.2,
	// engineport.go): the engine may live on any of CandidatePorts(
	// BasePort), the identity check arbitrates each candidate, and the
	// port settled on is recorded in engine.port (cross-client discovery).
	// Production always sets it (EnginePort(env)); its dial paths use
	// DialClient, which routes to the in-memory VERIFIED port only.
	//
	// 0 is FIXED-ENDPOINT mode: Client is dialed exactly as configured, no
	// scan, no engine.port, and the spawner is passed port 0. Package
	// tests that point a Client at an httptest server use it; production
	// never does.
	BasePort int

	// tv caches whole-tree verifications for this process (design A5(3)):
	// the first adoption or spawn per boot is a full re-hash.
	tv *TreeVerifier

	// mu serializes the operations that change the world (Reconcile,
	// install, update, uninstall, shutdown). smu guards ONLY the status
	// snapshot, so Status() — which the Settings panel and the advisor
	// probe call on every poll — never blocks behind a slow spawn-wait or
	// a 200 MB download.
	mu     sync.Mutex
	smu    sync.RWMutex
	status Status

	// verifiedPort (lane mode) is the port whose engine last passed the
	// identity check as a USABLE engine — AdoptAccept, or a spawn that
	// passed it — and 0 otherwise (review F2). It is cleared whenever the
	// status leaves healthy and is never set for an update-pending
	// adoption (F4). DialClient routes every advice / label request to it;
	// engine.port is never read for routing.
	verifiedPort atomic.Int32
}

func (m *Manager) setStatus(s Status) Status {
	m.smu.Lock()
	if s.State != StateHealthy {
		m.verifiedPort.Store(0)
	}
	m.status = s
	m.smu.Unlock()
	return s
}

// pinVerified records port as the verified engine port. Call it only
// immediately before setting a healthy status for that port.
func (m *Manager) pinVerified(port int) { m.verifiedPort.Store(int32(port)) }

// unpinnedURL is what a lane-mode DialClient dials when no engine is
// verified: port 0 never connects, so nothing (advice, labels) can leak
// to whatever squats the base port. Callers are Healthy()-gated anyway.
const unpinnedURL = "http://127.0.0.1:0"

// DialClient is the client the advice engine and the label pusher dial
// (review F2). Lane mode: it routes every request to the Manager's
// in-memory verified port, re-read per request, and fails closed while
// none is verified — engine.port is never consulted. Fixed-endpoint mode:
// a client on Client.BaseURL. Each call returns a fresh *Client with its
// own default http.Client.
func (m *Manager) DialClient() *Client {
	if m.BasePort == 0 {
		base := ""
		if m.Client != nil {
			base = m.Client.BaseURL
		}
		return NewClient(base, nil)
	}
	c := NewClient(unpinnedURL, nil)
	c.Endpoint = func() string {
		if p := m.verifiedPort.Load(); p > 0 {
			return LoopbackURL(int(p))
		}
		return ""
	}
	return c
}

// VerifiedPort is the lane port DialClient currently routes to (0: none).
func (m *Manager) VerifiedPort() int { return int(m.verifiedPort.Load()) }

// shutdownClient is the client a token-authorized shutdown goes to (review
// F2): in lane mode, the port where a scan RIGHT NOW verifies our usable
// engine (AdoptAccept) — never the ambient engine.port, never an
// update-pending or foreign listener, so the shutdown token is never sent
// to a process that merely claims our exe path. nil: nothing verified to
// stop. Call it while `current` still names the running engine (before an
// update flips it).
func (m *Manager) shutdownClient(ctx context.Context) *Client {
	if m.Client == nil {
		return nil
	}
	if m.BasePort == 0 {
		return m.Client
	}
	if sc := m.scanLanes(ctx); sc.found != nil && sc.found.healthy {
		return m.Client.at(sc.found.port)
	}
	return nil
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
		tv:       NewTreeVerifier(),
		status:   Status{State: StateNotInstalled, UpdatedAt: time.Now()},
	}
}

// Status returns a snapshot of the Manager's current, honest state.
// Never triggers a network call or a spawn — that is Reconcile's job.
// Constructing a Manager and calling Status() immediately (with no
// Reconcile ever run) is the "app boot" case design §2c/§6.2 requires to
// cost nothing: StateNotInstalled, no probe, no idle RAM.
func (m *Manager) Status() Status {
	m.smu.RLock()
	defer m.smu.RUnlock()
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
		return m.setStatus(Status{State: StateNotInstalled, Detail: "no client configured", UpdatedAt: time.Now()})
	}

	if m.BasePort > 0 {
		return m.setStatus(m.reconcileLanes(ctx))
	}

	health, err := m.Client.Health(ctx)
	if err == nil {
		return m.setStatus(m.evaluateRunning(health))
	}
	if errors.Is(err, ErrUnusableResponse) {
		// Something IS listening — it just did not answer /health with a
		// payload this client can read (a foreign process, or an engine
		// whose /health shape drifted from this build's). The port is
		// taken: spawning here would only launch a second engine that
		// cannot bind. Never adopted, never spawned over, never killed.
		return m.setStatus(occupiedStatus(err))
	}

	// Nothing answered /health: either nothing is installed, or an
	// installed engine is not currently running and this client should
	// spawn it (adopt-or-spawn, design §3.5).
	return m.setStatus(m.spawnLocked(ctx, 0))
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
func (m *Manager) evaluateRunning(health HealthPayload) Status { return m.evaluate(health, true) }

// evaluate is evaluateRunning with the lease renewal switchable: Observe
// (a settings-panel read) must report the verdict without pinning the
// engine alive.
func (m *Manager) evaluate(health HealthPayload, renew bool) Status {
	decision, err := EvaluateAdoption(m.Layout, health, m.tv)
	if err != nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: err.Error(), UpdatedAt: time.Now()}
	}
	if decision.Action == AdoptAccept && renew {
		m.renewLease()
	}
	return statusForDecision(decision, health)
}

// statusForDecision maps an adoption verdict onto the state machine.
func statusForDecision(decision AdoptDecision, health HealthPayload) Status {
	now := time.Now()
	switch decision.Action {
	case AdoptAccept:
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
// currently listening on the engine port (in lane mode: on the free lane
// candidate `port` the scan chose). If nothing is installed at all, that is
// simply StateNotInstalled (installation is a distinct, explicit user
// action — design §6.2 — never triggered implicitly by a failed health
// probe). If an engine IS installed, this client spawns it under the
// O_EXCL spawn lock (design §3.5), then health-checks (polling up to
// StartupWait — a real engine needs seconds to bind its port).
//
// Lane mode (port > 0): the spawner is told to bind `port`, health is
// polled THERE, and the spawned engine must pass the same identity check
// adoption applies before engine.port records it — a process that
// answers on the port but fails identity is reported by its verdict,
// never recorded, never called healthy. port == 0 is fixed-endpoint mode.
func (m *Manager) spawnLocked(ctx context.Context, port int) Status {
	now := time.Now()
	if _, ok := m.Installed(); !ok {
		return Status{State: StateNotInstalled, UpdatedAt: now}
	}
	currentDir, err := m.Layout.CurrentVersionDir()
	if err != nil {
		return Status{State: StateNotInstalled, Detail: err.Error(), UpdatedAt: now}
	}
	label := filepath.Base(currentDir)
	// Never exec bytes this client has not just verified (design R2 +
	// Amendment A5(3)/(4)): the same launcher + whole-tree + provenance
	// check adoption applies. Before A5 this path only consulted the
	// record's Verified bit and then exec'd whatever was on disk.
	if _, verr := VerifyInstalled(m.Layout, label, m.tv); verr != nil {
		return Status{State: StateUnverified, Reason: ReasonDigestMismatch, Detail: "refusing to spawn an unverifiable install: " + verr.Error(), UpdatedAt: now}
	}
	exePath := pathUnderVersionsDir(m.Layout, label)

	// The real engine self-terminates through lease/ under
	// KENAZ_ML_INSTALL_ROOT, so the CLIENT must have created that
	// directory before the process exists (kenaz-ml interop invariant —
	// an engine that finds no lease/ dir could read "no leases" as "no
	// clients" and exit). AcquireSpawnLock also creates it, but the
	// invariant is stated and tested here, independent of that detail.
	if err := os.MkdirAll(m.Layout.LeaseDir(), 0o755); err != nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "create lease dir: " + err.Error(), UpdatedAt: now}
	}

	lock, lerr := AcquireSpawnLock(m.Layout, os.Getpid(), processAlive)
	if lerr != nil {
		// Another live process is already spawning — report "installing"
		// rather than racing it (design §3.5: spawn is serialized).
		return Status{State: StateInstalling, Detail: "another process is spawning the engine", UpdatedAt: now}
	}
	defer lock.Release()

	// Lane mode: another client (Kenaz shares this root and this scheme)
	// may have spawned and recorded an engine between our scan and our
	// lock. Adopt it rather than starting a second engine on another lane.
	if port > 0 {
		if rec, ok, _ := RecordedEnginePort(m.Layout, m.BasePort); ok {
			switch lp := m.probeLane(ctx, rec); lp.verdict {
			case laneOurs:
				m.adoptLane(lp)
				return lp.status
			case laneBusy, laneTerminal:
				// Recorded port connected but did not answer, or is a
				// refusal: never step around it into a second spawn.
				return lp.status
			}
		}
	}

	if m.Spawner == nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "no spawner configured", UpdatedAt: now}
	}
	// The spawning client owns lease/shutdown.token (kenaz-ml reads it on
	// every /v1/admin/shutdown and never creates it). Written — rotated —
	// here, under the spawn lock, before the process exists, so every
	// engine this client starts can be stopped by Update / Uninstall.
	// Until the v0.86.0 unwired sweep nothing in production wrote it.
	// Not fatal: Update/Uninstall fall back to ensureLocalToken.
	if _, terr := WriteLocalToken(m.Layout); terr != nil {
		logging.L().Warn("mlsidecar.spawn.token_write_failed", "err", terr.Error())
	}
	if _, serr := m.Spawner.Spawn(ctx, exePath, port); serr != nil {
		// Deliberate (review F5): a spawn that keeps failing — e.g. the
		// engine cannot bind the chosen lane port — stays honestly
		// installed_unhealthy/crash until the next Ensure (DemandProbe's
		// 30s cadence) retries; there is no tight respawn loop here.
		logging.L().Warn("mlsidecar.spawn_failed", "exe", exePath, "port", port, "err", serr.Error())
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: serr.Error(), UpdatedAt: now}
	}

	client := m.Client
	if port > 0 {
		client = m.Client.at(port)
	}
	health, herr := m.awaitHealth(ctx, client)
	if herr != nil {
		return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: "spawned but did not become healthy: " + herr.Error(), UpdatedAt: now}
	}
	if port > 0 {
		decision, derr := EvaluateAdoption(m.Layout, health, m.tv)
		if derr != nil {
			return Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: derr.Error(), UpdatedAt: time.Now()}
		}
		if decision.Action != AdoptAccept {
			st := statusForDecision(decision, health)
			st.Detail = fmt.Sprintf("spawned engine on port %d failed the identity check: %s", port, st.Detail)
			return st
		}
		if werr := WriteEnginePort(m.Layout, port); werr != nil {
			logging.L().Warn("mlsidecar.engine_port.write_failed", "port", port, "err", werr.Error())
		}
		m.pinVerified(port)
		m.renewLease()
		return Status{State: StateHealthy, EngineVersion: health.SidecarVersion, ContractVersion: health.LifecycleProtocol, Detail: fmt.Sprintf("spawned on port %d", port), UpdatedAt: time.Now()}
	}
	m.renewLease()
	return Status{State: StateHealthy, EngineVersion: health.SidecarVersion, ContractVersion: health.LifecycleProtocol, Detail: "spawned", UpdatedAt: time.Now()}
}

// awaitHealth probes /health once, then keeps polling every StartupPoll
// (default 250ms) until StartupWait elapses or ctx ends.
func (m *Manager) awaitHealth(ctx context.Context, client *Client) (HealthPayload, error) {
	health, err := client.Health(ctx)
	if err == nil || m.StartupWait <= 0 {
		return health, err
	}
	poll := m.StartupPoll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.NewTimer(m.StartupWait)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return HealthPayload{}, ctx.Err()
		case <-deadline.C:
			return HealthPayload{}, err
		case <-tick.C:
			if h, herr := client.Health(ctx); herr == nil {
				return h, nil
			} else {
				err = herr
			}
		}
	}
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

// resolveVerifier returns the Verifier an install/update should use.
func (m *Manager) resolveVerifier(ctx context.Context) (Verifier, error) {
	if m.VerifierFunc != nil {
		return m.VerifierFunc(ctx)
	}
	return m.Verifier, nil
}

// installFailureStatus maps a failed Install into an honest Status: a
// verification failure is ReasonDigestMismatch (the tampered-artifact
// surface); anything else (network, disk, mount) is reported as what it
// was. If an engine was already installed it stays installed — a failed
// re-install never pretends to have uninstalled anything.
func (m *Manager) installFailureStatus(err error) Status {
	reason := ReasonNone
	if errors.Is(err, ErrVerificationFailed) || errors.Is(err, ErrDigestMismatch) {
		reason = ReasonDigestMismatch
	}
	state := StateNotInstalled
	if _, ok := m.Installed(); ok {
		state = StateInstalledUnhealthy
	}
	return Status{State: state, Reason: reason, Detail: "install failed: " + err.Error(), UpdatedAt: time.Now()}
}

// InstallAndActivate implements the explicit, user-initiated install
// flow (design §6.2: the "Enable local recommendations" button) —
// distinct from Reconcile's implicit adopt-or-spawn, which never
// downloads anything. While the download+verify+unpack is in flight the
// Manager honestly reports StateInstalling (Detail names the sub-phase:
// downloading / verifying / unpacking / starting); on success it
// immediately reconciles (spawns/health-checks the newly-activated
// version) so the caller gets back a real StateHealthy/
// StateInstalledUnhealthy rather than having to poll separately. A
// second call while one is in flight returns the in-flight status
// instead of racing it.
func (m *Manager) InstallAndActivate(ctx context.Context, req InstallRequest) Status {
	if !m.mu.TryLock() {
		return m.Status()
	}
	defer m.mu.Unlock()

	m.setStatus(Status{State: StateInstalling, Detail: "downloading " + req.Version, UpdatedAt: time.Now()})
	req.Mounter = firstMounter(req.Mounter, m.Mounter)
	userPhase := req.Phase
	req.Phase = func(p string) {
		m.setStatus(Status{State: StateInstalling, Detail: p + " " + req.Version, UpdatedAt: time.Now()})
		if userPhase != nil {
			userPhase(p)
		}
	}

	verifier, verr := m.resolveVerifier(ctx)
	if verr != nil {
		return m.setStatus(m.installFailureStatus(fmt.Errorf("%w: %w", ErrVerificationFailed, verr)))
	}
	if _, err := Install(ctx, m.Layout, m.Registry, m.Creds, verifier, req); err != nil {
		return m.setStatus(m.installFailureStatus(err))
	}

	m.setStatus(Status{State: StateInstalling, Detail: "starting " + req.Version, UpdatedAt: time.Now()})
	return m.reconcileLocked(ctx)
}

func firstMounter(a, b DMGMounter) DMGMounter {
	if a != nil {
		return a
	}
	return b
}

// UpdateAndActivate implements design §3.5/§3.7 R6's flip-and-respawn,
// through the Manager so the resulting state is observable the same way
// InstallAndActivate's is.
func (m *Manager) UpdateAndActivate(ctx context.Context, req InstallRequest) (UpdateResult, Status) {
	if !m.mu.TryLock() {
		return UpdateResult{}, m.Status()
	}
	defer m.mu.Unlock()

	m.setStatus(Status{State: StateInstalling, Reason: ReasonUpdatePending, Detail: "updating to " + req.Version, UpdatedAt: time.Now()})
	req.Mounter = firstMounter(req.Mounter, m.Mounter)

	verifier, verr := m.resolveVerifier(ctx)
	if verr != nil {
		st := m.setStatus(Status{State: StateInstalledUnhealthy, Reason: ReasonUpdatePending, Detail: "update failed, old version still current: " + verr.Error(), UpdatedAt: time.Now()})
		return UpdateResult{}, st
	}
	// Resolve the shutdown target BEFORE Update's Install flips `current`
	// (after the flip the running engine no longer verifies as ours).
	res := Update(ctx, m.Layout, m.Registry, m.Creds, verifier, m.shutdownClient(ctx), req)

	if res.Install.Record.Version == "" {
		detail := "update failed, old version still current"
		if res.ShutdownErr != nil {
			detail += ": " + res.ShutdownErr.Error()
		}
		st := m.setStatus(Status{State: StateInstalledUnhealthy, Reason: ReasonUpdatePending, Detail: detail, UpdatedAt: time.Now()})
		return res, st
	}
	return res, m.reconcileLocked(ctx)
}

// Shutdown releases this client's own lease WITHOUT touching the running
// process — a "clean stop on app exit" (spec §2c) means "stop pinning
// the sidecar alive", not "kill it": other clients (or this one's own
// next launch) may still be using it, and the sidecar's own 120s
// zero-lease self-termination (design §3.5) is its job, not this
// client's.
// KNOWN HAZARD (dated 2026-09-30, WP13 review; becomes reachable the
// moment PinnedEngineRelease returns a real release): Shutdown takes
// m.mu, and an in-flight InstallAndActivate holds it for the whole
// download+verify+mount using the Wails appCtx, which Shutdown does not
// cancel — quitting mid-install would hang app exit until the install
// finishes. Unreachable today (Enable returns ErrUnavailable with no
// published release). Fix alongside the release-channel wiring: an
// install ctx Shutdown can cancel, or a bounded TryLock here.
func (m *Manager) Shutdown(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ClientID == "" {
		return nil
	}
	return ReleaseLease(m.Layout, m.ClientID)
}

// Installed reports whether an ADOPTABLE engine install record exists
// under this Manager's root: a known provenance (this client's A-1
// channel-manifest install OR a Kenaz bundle-digest seed — design
// Amendment A5(4)) carrying a whole-tree digest (A5(3)). It is keyed on
// provenance + tree digest, NOT on the Verified bit: a Kenaz seed never
// sets Verified, and gating on it would call a shared, adoptable engine
// "not installed" (no lazy start, no uninstall, and an Enable offered
// that would re-install over the other client's engine).
//
// Cheap (one small file read, no hashing) and never touches the network
// — the Settings panel and DemandProbe use it to decide whether any
// engine-related work is warranted at all, so a user who never enabled
// recommendations costs nothing. The full on-disk re-verification
// (VerifyInstalled) still gates every spawn and adoption.
func (m *Manager) Installed() (InstallRecord, bool) {
	rec, ok, err := ReadInstallJSON(m.Layout)
	if err != nil || !ok || !recordAdoptable(m.Layout, rec) {
		return InstallRecord{}, false
	}
	return rec, true
}

// recordAdoptable is the record-only half of VerifyInstalled's rule: a
// known installer provenance and a tree digest to re-verify against.
func recordAdoptable(l Layout, rec InstallRecord) bool {
	return l.acceptsProvenance(rec.Provenance) && rec.TreeSHA256 != ""
}

// occupiedStatus is the "something answered on the port, but not with a
// usable /health payload" status (ErrUnusableResponse): the port is taken.
func occupiedStatus(err error) Status {
	return Status{State: StateInstalledUnhealthy, Reason: ReasonPortConflict,
		Detail: "a process answered /health but not with a usable health payload: " + err.Error(), UpdatedAt: time.Now()}
}

// ErrEngineInUse is returned by Uninstall when another client (Kenaz)
// still holds a live lease on the shared engine: the install root is
// shared (design Amendment A5(2): ~/.kenaz/ml/<env>), so removing it
// would pull the engine out from under that app. This client's own lease
// is released and nothing else is touched.
var ErrEngineInUse = errors.New("mlsidecar: another app is still using the shared ML engine")

// ownsWholeRoot reports whether root is a directory this harness may
// remove wholesale on Uninstall: the isolated "<dataDir>/ml" fallback, or
// the ratified shared "~/.kenaz/ml/<env>" (env in prod|dev|test). Anything
// else (a Manager mis-pointed at a directory it does not own) gets only
// its known layout entries removed.
func ownsWholeRoot(root string) bool {
	base := filepath.Base(root)
	if base == "ml" {
		return true
	}
	if filepath.Base(filepath.Dir(root)) != "ml" {
		return false
	}
	switch base {
	case EngineEnvProd, EngineEnvDev, EngineEnvTest:
		return true
	}
	return false
}

// otherLiveClients returns the ids of OTHER clients holding a fresh lease
// (stale-by-mtime and dead-by-pid leases are swept first, exactly as the
// engine's own sweep would).
func (m *Manager) otherLiveClients() []string {
	_, _ = SweepStaleLeases(m.Layout, time.Now(), processAlive)
	entries, err := os.ReadDir(m.Layout.LeaseDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !isLeaseFileName(e.Name()) {
			continue
		}
		client := e.Name()[:len(e.Name())-len(".lease")]
		if client != m.ClientID {
			out = append(out, client)
		}
	}
	return out
}

// Uninstall implements the WP12 checklist item (design §6.2 step 5):
// "release lease + remove version dirs + (if sole leaseholder)
// token-authorized drained stop". Leaves no process, weights, or config
// behind under this client's install root.
//
// WP13 widening: when the root is the ratified "~/.kenaz/ml/<env>" (or
// the isolated "<dataDir>/ml" fallback — see ownsWholeRoot), EVERYTHING
// under it is removed, because the engine writes its own state
// (downloaded models, calibration, retained examples) under
// KENAZ_ML_INSTALL_ROOT and "weights + config gone" must be literally
// true. For any other root only the known layout entries are removed — a
// Manager mis-pointed at a directory it does not own must never
// RemoveAll it. The root is SHARED with Kenaz (Amendment A5): if another
// client still holds a live lease, Uninstall releases ours and returns
// ErrEngineInUse without removing anything.
func (m *Manager) Uninstall(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// WP13-review hardening: whole-root removal additionally requires
	// that a known-provenance install record existed here at entry. A
	// name-based path check alone ("…/ml/<env>") must never be the only
	// thing between a mis-pointed Layout and RemoveAll of a directory we
	// do not own. Captured before the targeted removals delete the
	// record itself.
	_, hadKnownInstall := m.Installed()

	if m.ClientID != "" {
		_ = ReleaseLease(m.Layout, m.ClientID)
	}

	// Shared root: never pull the engine out from under another live
	// client. Our lease is already released; everything else stays.
	if others := m.otherLiveClients(); len(others) > 0 {
		return fmt.Errorf("%w (%s) — quit it, then uninstall again", ErrEngineInUse, strings.Join(others, ", "))
	}

	// Sole leaseholder: ask the engine to stop (token-authorized, drained).
	// ensureLocalToken (v0.86.0 unwired sweep): no production code
	// wrote the token before, so this used to skip the stop entirely and
	// remove the root out from under a still-running engine.
	if target := m.shutdownClient(ctx); target != nil {
		if token, terr := ensureLocalToken(m.Layout); terr == nil {
			_ = target.Shutdown(ctx, token)
		}
	}

	for _, d := range []string{m.Layout.VersionsDir(), m.Layout.CheckpointsDir(), m.Layout.LeaseDir(), filepath.Join(m.Layout.Root, ".staging")} {
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
	if err := RemoveEnginePort(m.Layout); err != nil {
		return err
	}
	if hadKnownInstall && ownsWholeRoot(m.Layout.Root) {
		if err := os.RemoveAll(m.Layout.Root); err != nil {
			return err
		}
	}
	m.setStatus(Status{State: StateNotInstalled, UpdatedAt: time.Now()})
	return nil
}
