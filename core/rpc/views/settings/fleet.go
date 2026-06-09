package settings

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/fleet_hosted"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// AdapterRegistrar is the minimal interface from the LLM registry that the
// fleet wiring code needs. Using an interface avoids a hard import of
// core/llm/registry from this package.
type AdapterRegistrar interface {
	RegisterAdapter(a llm.ProviderAdapter)
}

// fleetState holds the fleet client, dataDir, capability poller, config
// poller, the fleet_hosted LLM adapter (when CapHostedInference is enabled),
// and the emergency-lockdown watcher (fleet-emergency-lockdown-01NDFSEX12).
// It is attached to API after construction via SetFleetClient.
type fleetState struct {
	mu              sync.RWMutex
	client          *fleet.Client
	dataDir         string
	poller          *fleet.CapabilityPoller
	configPoller    *fleet.ConfigPoller
	llmRegistrar    AdapterRegistrar
	fleetAdapter    *fleet_hosted.Adapter
	lockdownWatcher *fleet.Watcher
	lockdownBroker  fleet.BrokerSink

	// cedarEngine is the Cedar policy engine wired at SetCedarEngine time.
	// Used by the composite ConfigApplier to apply team policy bundles.
	cedarEngine *cedarpolicy.Engine

	// fleetModelPrefs is the last-received fleet model preferences.
	// Protected by mu.
	fleetModelPrefs *fleet.BundleModelPrefs

	// skillStore + skillRegistry are wired at boot time via SetSkillRefs so the
	// compositeConfigApplier can call fleet.ApplyMandatedSkills when a bundle
	// carries the mandated_skills section (fleet-skills-sync-01NDFSEX18 WP05).
	// Both may be nil when fleet skill sync is not configured.
	skillStore    *slashcmd.SkillStore
	skillRegistry *slashcmd.Registry
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
	// Start the capability poller lazily. When c is a nop client the poller
	// will degrade gracefully on every Refresh call.
	if a.fleet.poller == nil {
		p := fleet.NewCapabilityPoller(c, dataDir)
		a.fleet.poller = p
		p.Start(context.Background())
	}
	// Start the config poller lazily.
	if a.fleet.configPoller == nil {
		applier := &compositeConfigApplier{state: a.fleet}
		cp := fleet.NewConfigPoller(c, dataDir, applier)
		a.fleet.configPoller = cp
		cp.Start(context.Background())
	}
	// Wire the fleet_hosted LLM adapter when we have a profile URL.
	// The adapter gates itself at resolve time via the EnabledFunc so
	// tier changes propagate within one poll interval without restart.
	if c != nil && c.Profile().FleetBaseURL != "" {
		a.wireFleetHostedAdapter(c)
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
}

// SetCedarEngine wires the Cedar policy engine into the fleet state so that
// the config poller can apply fleet-distributed policy bundles.
// Must be called before SetFleetClient to take effect; if called after,
// the engine is stored and will be used by subsequent bundle applies.
func (a *API) SetCedarEngine(engine *cedarpolicy.Engine) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.cedarEngine = engine
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

// wireFleetHostedAdapter creates the fleet_hosted LLM adapter and registers
// it in the LLM registry (if one has been set via SetLLMRegistrar).
// Called under a.fleet.mu.Lock() from SetFleetClient.
func (a *API) wireFleetHostedAdapter(c *fleet.Client) {
	profile := c.Profile()
	if profile.FleetBaseURL == "" {
		return
	}
	poller := a.fleet.poller // already set above
	bearer := func() (string, error) {
		ts, err := fleet.LoadTokens()
		if err != nil {
			return "", err
		}
		return ts.AccessToken, nil
	}
	enabled := func() bool {
		if poller == nil {
			return false
		}
		cur := poller.Current()
		return cur.Has(fleet.CapHostedInference)
	}
	adapter := fleet_hosted.New(profile.FleetBaseURL, bearer, enabled)
	a.fleet.fleetAdapter = adapter
	if a.fleet.llmRegistrar != nil {
		a.fleet.llmRegistrar.RegisterAdapter(adapter)
	}
}

// SetLLMRegistrar wires the LLM adapter registry into the fleet state so
// that the fleet_hosted adapter can be registered when SetFleetClient is
// called. Must be called before SetFleetClient to take effect; otherwise
// the adapter is registered lazily on the next SetFleetClient call.
func (a *API) SetLLMRegistrar(r AdapterRegistrar) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.llmRegistrar = r
	// If SetFleetClient was called first, register any pending adapter.
	if a.fleet.fleetAdapter != nil {
		r.RegisterAdapter(a.fleet.fleetAdapter)
	}
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

// FleetSignOut clears tokens and identity cache.
func (a *API) FleetSignOut(ctx context.Context) error {
	logging.L().Info("fleet.rpc.sign_out.start")
	if fleet.Disabled() {
		logging.L().Warn("fleet.rpc.sign_out.disabled_by_env")
		return fleet.ErrFleetDisabled
	}
	if err := fleet.ClearTokens(); err != nil {
		logging.L().Error("fleet.rpc.sign_out.clear_tokens_failed", "err", err.Error())
		return err
	}
	dataDir := a.fleetDataDir()
	if dataDir != "" {
		_ = os.Remove(fleet.IdentityFilePath(dataDir))
	}
	logging.L().Info("fleet.rpc.sign_out.success")
	return nil
}

// FleetSignedIn reports whether valid tokens exist.
func (a *API) FleetSignedIn(_ context.Context) (bool, error) {
	if fleet.Disabled() {
		return false, nil
	}
	_, err := fleet.LoadTokens()
	return err == nil, nil
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
	return fleetIdentityToView(id), nil
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
	p := a.fleetConfigPoller()
	if p == nil {
		return FleetConfigPullStatusView{Source: "default-deny"}, nil
	}
	st := p.Status()
	return FleetConfigPullStatusView{
		LastAppliedID:  st.LastAppliedID,
		LastAppliedAt:  st.LastAppliedAt,
		LastError:      st.LastError,
		Source:         st.Source,
		BundleChecksum: st.BundleChecksum,
	}, nil
}

// ── compositeConfigApplier ───────────────────────────────────────────────────

// compositeConfigApplier implements fleet.ConfigApplier. It fans out each
// section of the bundle to the appropriate sub-system:
//   - cedar_delta   → cedarpolicy.Engine.SetTeamBundle
//   - mcp_allowlist → recipes.ApplyFleetAllowlist
//   - model_prefs   → stored in fleetState.fleetModelPrefs
//   - weight_urls   → fleet.SetWeightURLs
type compositeConfigApplier struct {
	state *fleetState
}

func (a *compositeConfigApplier) ApplyBundle(ctx context.Context, b *fleet.Bundle) error {
	var errs []error

	// Cedar delta.
	if len(b.CedarDelta) > 0 {
		a.state.mu.RLock()
		engine := a.state.cedarEngine
		a.state.mu.RUnlock()
		if engine != nil {
			if err := fleet.ApplyCedarDelta(ctx, engine, b.CedarDelta); err != nil {
				errs = append(errs, err)
			}
		}
	}

	// MCP allow-list.
	if b.MCPAllowlist != nil {
		recipes.ApplyFleetAllowlist(b.MCPAllowlist)
	}

	// Model prefs.
	if b.ModelPrefs != nil {
		a.state.mu.Lock()
		a.state.fleetModelPrefs = b.ModelPrefs
		a.state.mu.Unlock()
	}

	// Weight URLs.
	if len(b.KameasMLWeightURLs) > 0 {
		a.state.mu.RLock()
		dataDir := a.state.dataDir
		a.state.mu.RUnlock()
		fleet.SetWeightURLs(dataDir, b.KameasMLWeightURLs)
	}

	// Mandated skills (fleet-skills-sync-01NDFSEX18 WP05).
	// Partial-success pattern: individual skill errors are collected but
	// don't prevent the bundle ACK from advancing (matching the rest of
	// the bundle apply logic).
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
		}
	}

	if len(errs) > 0 {
		return errs[0] // return first error; others are logged by the poller
	}
	return nil
}

// FleetModelPrefs returns the current fleet-managed model preferences, or nil
// when no bundle has been applied.
func (a *API) FleetModelPrefs() *fleet.BundleModelPrefs {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.fleetModelPrefs
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
