package settings

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/fleet"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/fleet_hosted"
)

// AdapterRegistrar is the minimal interface from the LLM registry that the
// fleet wiring code needs. Using an interface avoids a hard import of
// core/llm/registry from this package.
type AdapterRegistrar interface {
	RegisterAdapter(a llm.ProviderAdapter)
}

// fleetState holds the fleet client, dataDir, capability poller, and the
// fleet_hosted LLM adapter (when CapHostedInference is enabled).
// It is attached to API after construction via SetFleetClient.
type fleetState struct {
	mu            sync.RWMutex
	client        *fleet.Client
	dataDir       string
	poller        *fleet.CapabilityPoller
	llmRegistrar  AdapterRegistrar
	fleetAdapter  *fleet_hosted.Adapter
}

// SetFleetClient wires a fleet.Client into the API and starts the capability
// poller. Called from rpc.New() during chassis boot. When not called, fleet
// methods return fleet.ErrFleetDisabled.
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
	// Wire the fleet_hosted LLM adapter when we have a profile URL.
	// The adapter gates itself at resolve time via the EnabledFunc so
	// tier changes propagate within one poll interval without restart.
	if c != nil && c.Profile().FleetBaseURL != "" {
		a.wireFleetHostedAdapter(c)
	}
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

// FleetSignIn kicks off the PKCE loopback OAuth flow. On success it
// calls FleetRefreshIdentity to populate the cached identity.
func (a *API) FleetSignIn(ctx context.Context) (FleetIdentity, error) {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	profile := fleet.ResolveProfile()
	ts, err := fleet.DeviceCodeFlow(ctx, profile)
	if err != nil {
		return FleetIdentity{}, err
	}
	if err := fleet.SaveTokens(ts); err != nil {
		return FleetIdentity{}, err
	}
	return a.fleetEnroll(ctx)
}

// FleetSignOut clears tokens and identity cache.
func (a *API) FleetSignOut(ctx context.Context) error {
	if fleet.Disabled() {
		return fleet.ErrFleetDisabled
	}
	if err := fleet.ClearTokens(); err != nil {
		return err
	}
	dataDir := a.fleetDataDir()
	if dataDir != "" {
		// Best-effort delete.
		_ = os.Remove(fleet.IdentityFilePath(dataDir))
	}
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
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	dataDir := a.fleetDataDir()
	nodeID, _ := fleet.NodeID(dataDir)
	id, err := c.RefreshIdentity(ctx, nodeID, runtime.GOOS, "0.18.0")
	if err != nil {
		return FleetIdentity{}, err
	}
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
