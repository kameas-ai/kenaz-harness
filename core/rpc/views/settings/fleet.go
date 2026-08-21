package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fleetState holds the fleet client, dataDir, capability poller, config
// poller, and the emergency-lockdown watcher
// (fleet-emergency-lockdown-01NDFSEX12).
// It is attached to API after construction via SetFleetClient.
type fleetState struct {
	mu              sync.RWMutex
	client          *fleet.Client
	dataDir         string
	poller          *fleet.CapabilityPoller
	configPoller    *fleet.ConfigPoller
	lockdownWatcher *fleet.Watcher
	lockdownBroker  fleet.BrokerSink

	// cedarEngine is the Cedar policy engine wired at SetCedarEngine time.
	// Used by the composite ConfigApplier to apply team policy bundles.
	cedarEngine *cedarpolicy.Engine

	// skillStore + skillRegistry are wired at boot time via SetSkillRefs so the
	// compositeConfigApplier can call fleet.ApplyMandatedSkills when a bundle
	// carries the mandated_skills section (fleet-skills-sync-01NDFSEX18 WP05).
	// Both may be nil when fleet skill sync is not configured.
	skillStore    *slashcmd.SkillStore
	skillRegistry *slashcmd.Registry

	// otlpPipeline is the post-login OTLP export pipeline
	// (harness-fleet-otlp-export-01NTLMEX01). Set via SetFleetOTLPPipeline;
	// nil means OTLP export is not configured (OSS build / fleet disabled).
	otlpPipeline *fleet.FleetOTLPPipeline

	// telemetryRes is the startup OTel resource from telemetry.Init, held
	// so Activate can merge it with identity attrs.
	telemetryRes *resource.Resource

	// consent is the TelemetryConsent from the rpc API, used to gate
	// Activate: if consent == "none" the pipeline is not activated.
	consent *fleet.TelemetryConsent

	// tpFunc is a lazy accessor for the TracerProvider from telemetry.Init.
	// It is stored as a function rather than a pointer because telemetry.Init
	// runs inside c.Start() which fires AFTER rpc.New() constructs the fleet
	// state — a direct pointer captured at rpc.New time is always nil.
	// The function is invoked at Activate time (post-login, post-c.Start) so
	// it returns the real provider.
	// (harness-fleet-otlp-export-01NTLMEX01 tp-nil timing fix)
	tpFunc func() *sdktrace.TracerProvider

	// telemetryOptIns is the per-class telemetry opt-in set last fetched from
	// the fleet store (harness-fleet-sync-activation-01NSYNC01 gap #4). The
	// fleet store is authoritative; this is the harness-side cache populated at
	// enroll. nil before the first successful fetch.
	telemetryOptIns []fleet.TelemetryOptInItem
}

// SetFleetClient wires a fleet.Client into the API and starts the capability
// and config pollers. Called from rpc.New() during chassis boot. When not
// called, fleet methods return fleet.ErrFleetDisabled.
func (a *API) SetFleetClient(c *fleet.Client, dataDir string) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.client = c
	a.fleet.dataDir = dataDir
	// Wire the session-expired broker into the client if already set.
	if a.fleet.lockdownBroker != nil && c != nil {
		c.SetSessionBroker(a.fleet.lockdownBroker)
	}
	// Start the capability poller lazily. When c is a nop client the poller
	// will degrade gracefully on every Refresh call.
	if a.fleet.poller == nil {
		p := fleet.NewCapabilityPoller(c, dataDir)
		a.fleet.poller = p
		p.Start(context.Background())
	}
	// Background network workers do not run under `go test`.
	//
	// The ConfigPoller calls fleet.LoadTokens -> keyring.Get() on a ticker.
	// go-keyring's mock backend is a package-level global whose provider
	// pointer AND whose internal map are both unsynchronised, so a poller
	// alive during a test race any test touching the keyring. That surfaced
	// for weeks as "TestKeychainDelete/Set...: race detected", which reads
	// like a keychain flake and is not one.
	//
	// Three earlier attempts fixed real but insufficient things:
	//   - t.Cleanup(api.Shutdown) at all 13 construction sites (v0.66.0)
	//   - making fleet.Syncer.Stop idempotent (a genuine latent panic)
	//   - hoisting keyring.MockInit() into TestMain
	// None could work. Cleanup runs when a test ENDS, but tests run in
	// PARALLEL — one test's poller is alive exactly while another test
	// touches the keyring. And MockInit hoisting removed the pointer race
	// only to expose the map race beneath it.
	//
	// The poller has no business running in a unit test at all: it is a
	// network worker on a ticker. Not starting it removes the whole class
	// rather than another instance of it. Production is unaffected —
	// testing.Testing() is false in the shipped binary.
	if a.fleet.configPoller == nil && !testing.Testing() {
		applier := &compositeConfigApplier{state: a.fleet}
		cp := fleet.NewConfigPoller(c, dataDir, applier)
		a.fleet.configPoller = cp
		cp.Start(context.Background())
	}
	// Start the emergency-lockdown watcher. The watcher self-gates on
	// CapEmergencyLockdown so it exits immediately when the capability
	// is absent (fleet-emergency-lockdown-01NDFSEX12 WP02).
	if a.fleet.lockdownWatcher == nil && c != nil {
		w := fleet.NewWatcher(c, a.fleet.poller, a.fleet.lockdownBroker)
		a.fleet.lockdownWatcher = w
		w.Start(context.Background())
	}
}

// SetLockdownBroker wires the event broker into the fleet state so the
// lockdown Watcher can publish fleet:lockdown:changed events to the frontend.
// Also wires the same broker into the fleet Client for session-expired events
// (fleet-integrity-observability WP05 / FR-005).
// Must be called before SetFleetClient to take effect on first start; if called
// after, the watcher uses the broker on its next reconnect cycle.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) SetLockdownBroker(sink fleet.BrokerSink) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.lockdownBroker = sink
	// If the watcher is already running, update its broker reference.
	if a.fleet.lockdownWatcher != nil {
		a.fleet.lockdownWatcher.SetBroker(sink)
	}
	// Wire the broker into the fleet client for session-expired events (FR-005).
	if a.fleet.client != nil {
		a.fleet.client.SetSessionBroker(sink)
	}
}

// SetCedarEngine wires the Cedar policy engine into the fleet state so that
// the config poller can apply fleet-distributed policy bundles.
//
// Ordering does NOT matter, despite an earlier version of this comment
// claiming it must be called before SetFleetClient (fleet-enforcement-truth-
// 01PMZ505 WP03 re-verified this per spec §5.2 / §13 claim 2, and the claim
// was wrong): SetFleetClient's compositeConfigApplier holds a pointer to
// the shared fleetState, and ApplyBundle reads a.state.cedarEngine fresh
// (under a.state.mu) on every apply rather than capturing it at
// construction time. Call this whenever the engine becomes available; any
// bundle applied afterward — even the very next poll — sees it.
func (a *API) SetCedarEngine(engine *cedarpolicy.Engine) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.cedarEngine = engine
}

// SetFleetOTLPPipeline wires the post-login OTLP export pipeline into the
// fleet state. Call this after SetFleetClient, before FleetSignIn or
// FleetRefreshIdentity, so the pipeline is ready to Activate on the first
// enroll success.
//
//   - p:          the FleetOTLPPipeline constructed in core.New.
//   - startupRes: the OTel resource from telemetry.Telemetry.Resource; merged
//     with identity attrs at Activate time.
//   - tpFunc:     a lazy accessor for the TracerProvider from telemetry.Init.
//     Stored as a function rather than a direct pointer because telemetry.Init
//     runs inside c.Start() which fires AFTER rpc.New() — a pointer captured
//     at rpc.New time is always nil. The function is called at Activate time
//     (post-login, post-c.Start) so it returns the real provider.
//   - consent:    the TelemetryConsent instance (gates on EffectiveLevel).
//
// (harness-fleet-otlp-export-01NTLMEX01 wiring seam; tp-nil timing fix)
func (a *API) SetFleetOTLPPipeline(
	p *fleet.FleetOTLPPipeline,
	startupRes *resource.Resource,
	tpFunc func() *sdktrace.TracerProvider,
	consent *fleet.TelemetryConsent,
) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.otlpPipeline = p
	a.fleet.telemetryRes = startupRes
	a.fleet.tpFunc = tpFunc
	a.fleet.consent = consent
}

// SetSkillRefs wires the fleet-skill store and slash registry into the fleet
// state so that the compositeConfigApplier can call fleet.ApplyMandatedSkills
// when a config bundle carries a mandated_skills section.
// (fleet-skills-sync-01NDFSEX18 WP05)
//
// Called from rpc.New() after both the skillStore and slashRegistry are
// constructed. Safe to skip — when nil, ApplyBundle silently skips the
// mandated_skills section.
func (a *API) SetSkillRefs(store *slashcmd.SkillStore, registry *slashcmd.Registry) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.skillStore = store
	a.fleet.skillRegistry = registry
}

func (a *API) fleetClient() *fleet.Client {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.client
}

// FleetClientForBootstrap returns the fleet client for use in one-shot
// bootstrap operations (e.g. BootstrapLockdownStatus). Returns nil when
// fleet is not configured or the client is a nop.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) FleetClientForBootstrap() *fleet.Client {
	return a.fleetClient()
}

func (a *API) fleetDataDir() string {
	if a.fleet == nil {
		return ""
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.dataDir
}

func (a *API) fleetPoller() *fleet.CapabilityPoller {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.poller
}

// CapabilityPoller returns the fleet capability poller, or nil when fleet is
// not configured. Used by the rpc layer to resolve live capabilities at call
// time (fleet-skills-sync-01NDFSEX18 WP02).
func (a *API) CapabilityPoller() *fleet.CapabilityPoller {
	return a.fleetPoller()
}

// FleetSignIn kicks off the PKCE loopback OAuth flow. On success it
// calls FleetRefreshIdentity to populate the cached identity.
func (a *API) FleetSignIn(ctx context.Context) (FleetIdentity, error) {
	logging.L().Info("fleet.rpc.sign_in.start")
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		logging.L().Warn("fleet.rpc.sign_in.disabled", "client_nil", c == nil, "env_disabled", fleet.Disabled())
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	profile := fleet.ResolveProfile()
	if !profile.Configured() {
		logging.L().Warn("fleet.rpc.sign_in.profile_not_configured",
			"profile", profile.Name,
			"issuer", profile.ZitadelIssuer,
			"has_client_id", profile.NativeClientID != "",
			"fleet_base_url", profile.FleetBaseURL,
		)
		return FleetIdentity{}, fleet.ErrProfileNotConfigured
	}
	ts, err := fleet.DeviceCodeFlow(ctx, profile)
	if err != nil {
		logging.L().Error("fleet.rpc.sign_in.device_code_flow_failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	if err := fleet.SaveTokens(ts); err != nil {
		logging.L().Error("fleet.rpc.sign_in.save_tokens_failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	logging.L().Info("fleet.rpc.sign_in.tokens_saved")
	id, err := a.fleetEnroll(ctx)
	if err != nil {
		logging.L().Error("fleet.rpc.sign_in.enroll_failed", "err", err.Error())
		return id, err
	}
	logging.L().Info("fleet.rpc.sign_in.success",
		"org_id", id.OrgID,
		"team_id", id.TeamID,
		"email", id.Email,
		"tier", id.Tier,
		"roles", id.Roles,
	)
	return id, nil
}

// StopFleetBackground stops all fleet background goroutines (capability
// poller, config poller, lockdown watcher) and clears the in-memory
// lockdown flag + model-pref cache. It is idempotent and safe to call
// from sign-out or app shutdown.
//
// Callers that need to block until goroutines have exited should call
// CapabilityPoller.Stop(), ConfigPoller.Stop(), and Watcher.Stop() directly;
// this method calls them in series.
func (a *API) StopFleetBackground() {
	if a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	poller := a.fleet.poller
	configPoller := a.fleet.configPoller
	watcher := a.fleet.lockdownWatcher
	// Nil them out so future Start calls in SetFleetClient create fresh instances.
	a.fleet.poller = nil
	a.fleet.configPoller = nil
	a.fleet.lockdownWatcher = nil
	// Clear in-memory caches that are session-scoped. Fleet model prefs
	// (fleet-enforcement-truth-01PMZ505 WP04) live in
	// core/rpc/views/llm's package-level store now, not here — clear
	// them there too, so a signed-out device does not keep enforcing an
	// allow-list or default model pushed before sign-out.
	llmview.ClearFleetModelPrefs()
	a.fleet.telemetryOptIns = nil
	pipeline := a.fleet.otlpPipeline
	a.fleet.mu.Unlock()

	// Drop the OTLP log lane's narrowing snapshot too: an empty snapshot
	// admits nothing, so a signed-out harness cannot keep exporting against a
	// stale opt-in set.
	if pipeline != nil {
		pipeline.SetTelemetryOptIns(nil)
	}

	// Stop goroutines outside the lock.
	if poller != nil {
		poller.Stop()
	}
	if configPoller != nil {
		configPoller.Stop()
	}
	if watcher != nil {
		watcher.Stop()
	}
	// Clear the package-level lockdown flag so a re-login starts clean.
	fleet.ForceSetLockdownForTest(false) // production-safe: the symbol is exported for exactly this use
}

// FleetSignOut clears tokens and identity cache, and stops background
// goroutines. Returns an aggregated error when one or more keychain
// operations fail (sign-out is still treated as logically complete).
func (a *API) FleetSignOut(ctx context.Context) error {
	logging.L().Info("fleet.rpc.sign_out.start")
	if fleet.Disabled() {
		logging.L().Warn("fleet.rpc.sign_out.disabled_by_env")
		return fleet.ErrFleetDisabled
	}
	// Stop pollers + watcher + clear caches before removing tokens so
	// in-flight requests have a chance to complete.
	a.StopFleetBackground()

	// Aggregate keyring errors: a missing token is not an error on sign-out.
	var signOutErr error
	if err := fleet.ClearTokens(); err != nil {
		logging.L().Warn("fleet.rpc.sign_out.clear_tokens_partial", "err", err.Error())
		signOutErr = err // surface to caller; sign-out proceeds regardless
	}
	dataDir := a.fleetDataDir()
	if dataDir != "" {
		if err := os.Remove(fleet.IdentityFilePath(dataDir)); err != nil && !os.IsNotExist(err) {
			logging.L().Warn("fleet.rpc.sign_out.remove_identity_failed", "err", err.Error())
			signOutErr = errors.Join(signOutErr, fmt.Errorf("remove identity file: %w", err))
		}
	}
	if signOutErr != nil {
		logging.L().Warn("fleet.rpc.sign_out.partial_success", "err", signOutErr.Error())
	} else {
		logging.L().Info("fleet.rpc.sign_out.success")
	}
	return signOutErr
}

// FleetSignedIn reports whether a valid (non-expired) fleet session exists.
//
// FR-004: honors token expiry — a dead session (expired access + refresh)
// returns false so the UI can show a re-auth prompt rather than a fake
// "signed in" state. Uses Client.SignedIn for consistent expiry semantics.
func (a *API) FleetSignedIn(ctx context.Context) (bool, error) {
	if fleet.Disabled() {
		return false, nil
	}
	c := a.fleetClient()
	if c == nil {
		return false, nil
	}
	return c.SignedIn(ctx)
}

// FleetRefreshIdentity calls the fleet enroll endpoint.
func (a *API) FleetRefreshIdentity(ctx context.Context) (FleetIdentity, error) {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	return a.fleetEnroll(ctx)
}

// FleetProfile returns the active env profile info (no secrets).
func (a *API) FleetProfile(_ context.Context) (FleetProfileInfo, error) {
	if fleet.Disabled() {
		return FleetProfileInfo{}, fleet.ErrFleetDisabled
	}
	p := fleet.ResolveProfile()
	return FleetProfileInfo{
		Name:         p.Name,
		BadgeColor:   p.BadgeColor(),
		FleetBaseURL: p.FleetBaseURL,
		Configured:   p.Configured(),
	}, nil
}

// fleetEnroll is the shared enroll helper used by SignIn and RefreshIdentity.
func (a *API) fleetEnroll(ctx context.Context) (FleetIdentity, error) {
	c := a.fleetClient()
	if c == nil {
		logging.L().Warn("fleet.rpc.enroll.no_client")
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	dataDir := a.fleetDataDir()
	nodeID, nodeIDErr := fleet.NodeID(dataDir)
	if nodeIDErr != nil {
		logging.L().Warn("fleet.rpc.enroll.node_id_error", "err", nodeIDErr.Error())
	}
	logging.L().Info("fleet.rpc.enroll.start",
		"node_id", nodeID,
		"platform", runtime.GOOS,
		"fleet_base_url", c.Profile().FleetBaseURL,
	)
	id, err := c.RefreshIdentity(ctx, nodeID, runtime.GOOS, "0.18.0")
	if err != nil {
		logging.L().Error("fleet.rpc.enroll.failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	logging.L().Info("fleet.rpc.enroll.success",
		"org_id", id.OrgID,
		"team_id", id.TeamID,
		"email", id.Email,
		"tier", id.Tier,
	)

	// Activate the fleet OTLP export pipeline post-enroll.
	// This is the post-login trigger point (FR-003): identity attrs are now
	// known (user.id = JWT sub resolved via enroll response, org.id =
	// enroll org_id, machine.id = nodeID), so we can build the identity
	// resource and register the OTLP processors/exporters.
	a.activateOTLPPipeline(ctx, id, nodeID)

	// Reconcile per-class telemetry opt-ins from the fleet store post-enroll
	// (harness-fleet-sync-activation-01NSYNC01 gap #4). The fleet store is the
	// source of truth for the seven classes (replacing local-only JSON). This
	// is best-effort and consent-gated: it caches the fleet-resolved opt-ins
	// but never relaxes the TelemetryConsent.EffectiveLevel export gate.
	a.refreshTelemetryOptIns(ctx)

	return fleetIdentityToView(id), nil
}

// refreshTelemetryOptIns fetches the per-class telemetry opt-in set from the
// fleet store and caches it on the fleet state. Best-effort: errors are logged
// at debug/warn and never fail the enroll flow. The cached set is exposed via
// FleetTelemetryOptIns for the settings UI.
func (a *API) refreshTelemetryOptIns(ctx context.Context) {
	c := a.fleetClient()
	if c == nil {
		return
	}
	items, err := c.GetTelemetryOptIns(ctx)
	if err != nil {
		// Unentitled / offline / signed-out → keep whatever is cached. Not fatal.
		logging.L().Debug("fleet.telemetry_optins.refresh.skipped", "err", err.Error())
		return
	}
	if a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	a.fleet.telemetryOptIns = items
	pipeline := a.fleet.otlpPipeline
	a.fleet.mu.Unlock()

	// Feed the OTLP log lane its narrowing signal. This can only reduce what
	// the lane admits — the compiled ceiling in core/fleet/log_event_kind.go
	// bounds it from above and no server response can widen that.
	if pipeline != nil {
		pipeline.SetTelemetryOptIns(items)
	}
	logging.L().Info("fleet.telemetry_optins.refreshed", "classes", len(items))
}

// activateOTLPPipeline calls FleetOTLPPipeline.Activate post-enroll.
// Gates on: pipeline wired, consent != "none", profile configured.
// Best-effort: errors are logged at warn and do not fail the enroll flow.
func (a *API) activateOTLPPipeline(ctx context.Context, id fleet.Identity, nodeID string) {
	if a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	pipeline := a.fleet.otlpPipeline
	baseRes := a.fleet.telemetryRes
	consent := a.fleet.consent
	tpFunc := a.fleet.tpFunc
	client := a.fleet.client
	a.fleet.mu.RUnlock()

	// Resolve the TracerProvider lazily. By the time activateOTLPPipeline is
	// called (post-login, post-c.Start), telemetry.Init has already run and
	// tpFunc returns the real provider. Calling it here rather than capturing
	// the pointer at rpc.New time avoids the tp-nil race where c.Start / init-
	// Telemetry hasn't executed yet.
	// (harness-fleet-otlp-export-01NTLMEX01 tp-nil timing fix)
	var tp *sdktrace.TracerProvider
	if tpFunc != nil {
		tp = tpFunc()
	}

	if pipeline == nil {
		logging.L().Debug("fleet.otlp.activate.skipped", "reason", "pipeline_not_wired")
		return
	}

	// Consent gate: "none" (default) → no OTLP export (NFR-005 / FR-006).
	if consent != nil && consent.EffectiveLevel() == fleet.ConsentNone {
		logging.L().Debug("fleet.otlp.activate.skipped", "reason", "consent_none")
		return
	}

	// OTLP ingest lives on the API host, which is discovered from
	// /config.json on the dashboard host — NOT on the dashboard host itself.
	// Deriving it from profile.FleetBaseURL pointed telemetry at CloudFront,
	// which returns 200 + index.html for any path, so exports "succeeded"
	// into a void. See fleet.OTLPBaseURL.
	if client == nil {
		logging.L().Debug("fleet.otlp.activate.skipped", "reason", "no_fleet_client")
		return
	}
	cfg, cfgErr := client.FleetConfig(ctx)
	if cfgErr != nil {
		logging.L().Warn("fleet.otlp.activate.api_host_unresolved", "err", cfgErr.Error())
		return
	}
	otlpBase := fleet.OTLPBaseURL(cfg)
	if otlpBase == "" {
		logging.L().Debug("fleet.otlp.activate.skipped", "reason", "no_api_base_url")
		return
	}

	// kameas.user.id MUST equal the Zitadel JWT `sub` — the fleet OTLP
	// receiver (validateResourceAttrs) rejects with 401 otherwise. The
	// enroll response's user_id is the fleet-internal UUID, a DIFFERENT
	// identity namespace, so decode the sub from the access token instead.
	userID, subErr := fleet.SubjectFromAccessToken()
	if subErr != nil || userID == "" {
		reason := "empty"
		if subErr != nil {
			reason = subErr.Error()
		}
		logging.L().Warn("fleet.otlp.activate.no_subject", "org_id", id.OrgID, "err", reason)
		return
	}

	attrs := fleet.IdentityAttrs{
		UserID:    userID,
		OrgID:     id.OrgID,
		MachineID: nodeID,
	}

	if err := pipeline.Activate(ctx, otlpBase, baseRes, attrs, fleet.DefaultBearerProvider(), tp); err != nil {
		logging.L().Warn("fleet.otlp.activate.failed", "err", err.Error())
	}
}

// fleetIdentityToView converts a fleet.Identity to the view type.
func fleetIdentityToView(id fleet.Identity) FleetIdentity {
	fi := FleetIdentity{
		UserID:      id.UserID,
		OrgID:       id.OrgID,
		TeamID:      id.TeamID,
		Email:       id.Email,
		DisplayName: id.DisplayName,
		Tier:        id.Tier,
		OrgName:     id.OrgName,
		TeamName:    id.TeamName,
	}
	if len(id.Roles) > 0 {
		fi.Roles = make([]string, len(id.Roles))
		copy(fi.Roles, id.Roles)
	}
	return fi
}

// FleetCapabilities returns the in-memory capability snapshot from the poller.
// When the poller is not running (fleet disabled / not wired) it returns an
// empty CapabilitiesView with source "default-deny".
func (a *API) FleetCapabilities(_ context.Context) (CapabilitiesView, error) {
	p := a.fleetPoller()
	if p == nil {
		return capabilitiesToView(fleet.DefaultDenyCapabilities()), nil
	}
	return capabilitiesToView(p.Current()), nil
}

// FleetRefreshCapabilities forces an immediate capability fetch from fleet.
// On success the in-memory snapshot and disk cache are updated. On error
// the last-known snapshot is returned alongside the error.
func (a *API) FleetRefreshCapabilities(ctx context.Context) (CapabilitiesView, error) {
	p := a.fleetPoller()
	if p == nil {
		return capabilitiesToView(fleet.DefaultDenyCapabilities()), fleet.ErrFleetDisabled
	}
	caps, err := p.Refresh(ctx)
	return capabilitiesToView(caps), err
}

// capabilitiesToView converts a fleet.Capabilities snapshot to the wire-safe
// CapabilitiesView, flattening the Capability typed keys to plain strings.
func capabilitiesToView(c fleet.Capabilities) CapabilitiesView {
	enabled := make(map[string]bool, len(c.Enabled))
	for k, v := range c.Enabled {
		enabled[string(k)] = v
	}
	fetchedAt := ""
	if !c.FetchedAt.IsZero() {
		fetchedAt = c.FetchedAt.UTC().Format(time.RFC3339)
	}
	return CapabilitiesView{
		Tier:      c.Tier,
		Enabled:   enabled,
		FetchedAt: fetchedAt,
		Source:    c.Source,
	}
}

// fleetConfigPoller returns the config poller, or nil when fleet is not wired.
func (a *API) fleetConfigPoller() *fleet.ConfigPoller {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.configPoller
}

// FleetConfigPullStatus returns the current config-pull poller state.
// Returns a zero-value view when fleet is disabled or the poller is not yet wired.
func (a *API) FleetConfigPullStatus(_ context.Context) (FleetConfigPullStatusView, error) {
	enabled := fleet.ConfigDistributionEnabled()
	p := a.fleetConfigPoller()
	if p == nil {
		return FleetConfigPullStatusView{
			Source:                    "default-deny",
			ConfigDistributionEnabled: enabled,
		}, nil
	}
	st := p.Status()
	return FleetConfigPullStatusView{
		LastAppliedID:             st.LastAppliedID,
		LastAppliedAt:             st.LastAppliedAt,
		LastError:                 st.LastError,
		Source:                    st.Source,
		BundleChecksum:            st.BundleChecksum,
		ConfigDistributionEnabled: enabled,
	}, nil
}

// FleetHealth returns a compact fleet-health summary for the global health
// indicator (WP10). It combines the signing-key presence, the config source,
// and the current session state into a single call so the header chip can
// render without multiple sequential RPCs.
func (a *API) FleetHealth(ctx context.Context) (FleetHealthView, error) {
	enabled := fleet.ConfigDistributionEnabled()

	// Config source + last error.
	var configSource, configLastError string
	if !enabled {
		configSource = "no-key"
	} else {
		p := a.fleetConfigPoller()
		if p == nil {
			configSource = "default-deny-degraded"
		} else {
			st := p.Status()
			configSource = st.Source
			configLastError = st.LastError
			if configSource == "default-deny" && configLastError == "" {
				configSource = "default-deny-degraded"
			}
		}
	}

	// Session state — delegate to FleetSignedIn/Client.SignedIn so Health uses
	// the same expiry semantics (tokenExpiryGrace + consistent no-ExpiresAt
	// handling) as the account UI, rather than a divergent inline check.
	signedIn, _ := a.FleetSignedIn(ctx)

	return FleetHealthView{
		ConfigDistributionEnabled: enabled,
		ConfigSource:              configSource,
		ConfigLastError:           configLastError,
		SignedIn:                  signedIn,
	}, nil
}

// ── compositeConfigApplier ───────────────────────────────────────────────────

// compositeConfigApplier implements fleet.ConfigApplier. It fans out each
// section of the bundle to the appropriate sub-system:
//   - cedar_delta   → cedarpolicy.Engine.SetTeamBundle
//   - mcp_allowlist → recipes.ApplyFleetAllowlist
//   - model_prefs   → llmview.ApplyFleetModelPrefs (core/rpc/views/llm)
//
// The kameas_ml_weight_urls bundle section is intentionally ignored: the
// fleet-hosted-LLM / kameas-ml surface was removed
// (harness-fleet-sync-activation-01NSYNC01, dead-code cleanup). The wire
// field is retained on Bundle so signature verification of server-signed
// bundles still round-trips, but it is no longer persisted or applied.
type compositeConfigApplier struct {
	state *fleetState
}

func (a *compositeConfigApplier) ApplyBundle(ctx context.Context, b *fleet.Bundle) []error {
	var errs []error

	// Cedar delta.
	//
	// fleet-enforcement-truth-01PMZ505 WP02: a signed bundle carrying a
	// cedar_delta section that this device cannot apply (no engine wired)
	// must NOT ack "applied:true" — that is a silently-discarded team
	// policy, not a no-op (spec §1.1). The else branch is the fix: it
	// turns a skipped section into a named apply error so
	// config_pull.go's "len(applyErrs) == 0" success check goes false,
	// lastAppliedID does not advance, and the bundle is re-attempted on
	// the next poll instead of being reported as enforced.
	if len(b.CedarDelta) > 0 {
		a.state.mu.RLock()
		engine := a.state.cedarEngine
		a.state.mu.RUnlock()
		if engine != nil {
			if err := fleet.ApplyCedarDelta(ctx, engine, b.CedarDelta); err != nil {
				errs = append(errs, err)
			}
		} else {
			errs = append(errs, fmt.Errorf("fleet/config: cedar_delta present but no Cedar engine wired (SetCedarEngine never called)"))
		}
	}

	// MCP allow-list.
	if b.MCPAllowlist != nil {
		recipes.ApplyFleetAllowlist(b.MCPAllowlist)
	}

	// Model prefs.
	//
	// fleet-enforcement-truth-01PMZ505 WP04: DefaultModel and
	// ProviderAllowlist now reach real branches —
	// llmview.ApplyFleetModelPrefs installs them into the package-level
	// store core/rpc/views/llm.ListProviders (filters the list),
	// StartStream (blocks an excluded profile) and
	// profileKindAndModel (seeds DefaultModel, D-3) read. WP02's
	// stored-and-unread-with-an-error state is gone; this is the real
	// consumer, mirroring the mcp_allowlist section immediately above.
	if b.ModelPrefs != nil {
		llmview.ApplyFleetModelPrefs(b.ModelPrefs.DefaultModel, b.ModelPrefs.ProviderAllowlist)
	}

	// Weight URLs (kameas_ml_weight_urls): intentionally ignored — the
	// fleet-hosted-LLM / kameas-ml surface was removed. See the type doc above.

	// Mandated skills (fleet-skills-sync-01NDFSEX18 WP05).
	// FR-012: all section errors are collected and returned so the ACK
	// carries the full set and the caller can decide not to advance lastAppliedID.
	//
	// fleet-enforcement-truth-01PMZ505 WP02: same fix as cedar_delta — a
	// bundle carrying mandated_skills that this device cannot apply (refs
	// not wired) must not ack clean.
	if len(b.MandatedSkills) > 0 {
		a.state.mu.RLock()
		skillStore := a.state.skillStore
		skillRegistry := a.state.skillRegistry
		a.state.mu.RUnlock()
		if skillStore != nil && skillRegistry != nil {
			skillErrs := fleet.ApplyMandatedSkills(skillStore, skillRegistry, b.MandatedSkills)
			for _, se := range skillErrs {
				logging.L().Warn("fleet.config.mandated_skills.apply_error", "err", se.Error())
				errs = append(errs, se)
			}
		} else {
			errs = append(errs, fmt.Errorf("fleet/config: mandated_skills present but skill refs not wired (SetSkillRefs never called)"))
		}
	}

	// Return all errors (FR-012). An empty slice means full success.
	return errs
}

// LockdownStatusView is the wire shape returned by FleetLockdownStatus.
// The frontend's LockdownBanner uses this to render the reason text and
// the session composer uses Active to disable the chat input.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
type LockdownStatusView struct {
	// Active is true when a fleet-issued emergency lockdown is in effect.
	Active bool `json:"active"`
	// Reason is the admin-supplied reason string. Empty when Active is false
	// or when the watcher has not received a reason from the fleet server.
	Reason string `json:"reason,omitempty"`
}

// FleetLockdownStatus returns the current emergency lockdown state.
// This RPC is called by the frontend on mount (banner boot) and after
// receiving a fleet:lockdown:changed broker event.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) FleetLockdownStatus(_ context.Context) (LockdownStatusView, error) {
	active := fleet.LockdownActive()
	return LockdownStatusView{Active: active}, nil
}

// ── Telemetry opt-ins (harness-fleet-sync-activation-01NSYNC01 gap #4) ─────────

// TelemetryOptInView is the wire-safe per-class opt-in record surfaced to the
// settings UI. Mirrors fleet.TelemetryOptInItem with an RFC3339 timestamp.
type TelemetryOptInView struct {
	Class   string `json:"class"`
	OptedIn bool   `json:"optedIn"`
	OptedAt string `json:"optedAt,omitempty"`
	Source  string `json:"source,omitempty"`
}

// FleetTelemetryOptIns returns the per-class telemetry opt-in set. On a cold
// read (cache empty) it fetches from the fleet store; otherwise it returns the
// cache populated at enroll. Returns fleet.ErrFleetDisabled when fleet is not
// wired. The set is the fleet store's view (the source of truth), not the
// local-only JSON.
func (a *API) FleetTelemetryOptIns(ctx context.Context) ([]TelemetryOptInView, error) {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return nil, fleet.ErrFleetDisabled
	}
	a.fleet.mu.RLock()
	cached := a.fleet.telemetryOptIns
	a.fleet.mu.RUnlock()
	if cached == nil {
		// Cold read: pull from the fleet store now.
		a.refreshTelemetryOptIns(ctx)
		a.fleet.mu.RLock()
		cached = a.fleet.telemetryOptIns
		a.fleet.mu.RUnlock()
	}
	return telemetryOptInsToView(cached), nil
}

// FleetSetTelemetryOptIn flips a single class opt-in in the fleet store
// (source becomes 'user_self' server-side) and refreshes the local cache.
// Consent-gated by tier server-side; the harness never bypasses the export
// consent gate. Returns fleet.ErrFleetDisabled when fleet is not wired.
func (a *API) FleetSetTelemetryOptIn(ctx context.Context, class string, optedIn bool) error {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return fleet.ErrFleetDisabled
	}
	if err := c.PutTelemetryOptIns(ctx, []fleet.TelemetryOptInItem{
		{Class: class, OptedIn: optedIn},
	}); err != nil {
		return err
	}
	// Re-pull so the cache reflects the server-applied source/opted_at.
	a.refreshTelemetryOptIns(ctx)
	return nil
}

func telemetryOptInsToView(items []fleet.TelemetryOptInItem) []TelemetryOptInView {
	out := make([]TelemetryOptInView, 0, len(items))
	for _, it := range items {
		v := TelemetryOptInView{
			Class:   it.Class,
			OptedIn: it.OptedIn,
			Source:  it.Source,
		}
		if it.OptedAt != nil && !it.OptedAt.IsZero() {
			v.OptedAt = it.OptedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}
