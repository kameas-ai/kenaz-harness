package settings

import (
	"context"
	"os"
	"runtime"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// fleetState holds the fleet client and dataDir for the Settings API.
// It is attached to API after construction via SetFleetClient.
type fleetState struct {
	mu      sync.RWMutex
	client  *fleet.Client
	dataDir string
}

// SetFleetClient wires a fleet.Client into the API. Called from rpc.New()
// during chassis boot. When not called, fleet methods return
// fleet.ErrFleetDisabled.
func (a *API) SetFleetClient(c *fleet.Client, dataDir string) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.client = c
	a.fleet.dataDir = dataDir
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
