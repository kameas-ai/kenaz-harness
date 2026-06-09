package fleet

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ClientOpts configures Client construction.
type ClientOpts struct {
	// DataDir is the harness data directory used for identity and node-id
	// persistence. If empty, file-backed caching is disabled.
	DataDir string
	// HTTPTimeout overrides the default per-request timeout of 30s.
	HTTPTimeout time.Duration
}

// Client is the fleet integration client. It holds an EnvProfile, an
// HTTP client for the fleet server, and coordinates auth + identity.
//
// Callers construct a Client via NewClient. When HARNESS_FLEET_DISABLED=1
// is active NewClient returns a Client with isNop=true that returns
// ErrFleetDisabled for every operation.
type Client struct {
	profile     EnvProfile
	httpClient  *http.Client
	dataDir     string
	httpTimeout time.Duration
	isNop       bool
}

// NewClient constructs and returns a Client using the resolved env profile.
// If Disabled() is true, NewClient returns a nopClient.
func NewClient(opts ClientOpts) (*Client, error) {
	if Disabled() {
		return nopClientInstance(), nil
	}
	profile := ResolveProfile()
	timeout := opts.HTTPTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		profile:     profile,
		httpClient:  &http.Client{},
		dataDir:     opts.DataDir,
		httpTimeout: timeout,
	}, nil
}

// Profile returns the resolved env profile for this client.
func (c *Client) Profile() EnvProfile {
	if c == nil || c.isNop {
		return EnvProfile{}
	}
	return c.profile
}

// FleetConfig returns the runtime config for this client's SPA host,
// resolving from cache or fetching /config.json fresh. The returned
// FleetConfig carries APIBaseURL — the host that owns /api/v1/*.
func (c *Client) FleetConfig(ctx context.Context) (FleetConfig, error) {
	if c == nil || c.isNop {
		return FleetConfig{}, ErrFleetDisabled
	}
	if c.profile.FleetBaseURL == "" {
		return FleetConfig{}, ErrProfileNotConfigured
	}
	return ResolveFleetConfig(ctx, c.dataDir, c.profile.FleetBaseURL)
}

// APIURL resolves the absolute URL for a /api/v1/... path against the
// API host returned by /config.json. path must start with "/api/v1/".
// Callers concatenate it with the rest of the path; this helper only
// returns the host prefix + path, no query parameters.
func (c *Client) APIURL(ctx context.Context, path string) (string, error) {
	cfg, err := c.FleetConfig(ctx)
	if err != nil {
		return "", err
	}
	if path == "" {
		return cfg.APIBaseURL, nil
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return cfg.APIBaseURL + path, nil
}

// nopClientInstance returns a Client that returns ErrFleetDisabled for
// everything. Callers always receive a non-nil *Client.
func nopClientInstance() *Client {
	return &Client{isNop: true}
}

// SignIn is a stub placeholder that returns ErrFleetDisabled on nop clients.
// Full implementation ships in WP02.
func (c *Client) SignIn(ctx context.Context) (Identity, error) {
	if c == nil || c.isNop {
		return Identity{}, ErrFleetDisabled
	}
	if !c.profile.Configured() {
		return Identity{}, ErrProfileNotConfigured
	}
	return Identity{}, ErrNotSignedIn // real impl in WP02
}

// SignOut clears persisted tokens and identity. Returns ErrFleetDisabled
// on nop clients.
func (c *Client) SignOut(ctx context.Context) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	if err := ClearTokens(); err != nil {
		return err
	}
	if c.dataDir != "" {
		_ = clearIdentityFile(c.dataDir) // best-effort
	}
	return nil
}

// SignedIn reports whether the client has a non-expired cached identity.
func (c *Client) SignedIn(ctx context.Context) (bool, error) {
	if c == nil || c.isNop {
		return false, nil
	}
	_, err := LoadTokens()
	if err != nil {
		return false, nil
	}
	return true, nil
}

// CurrentIdentity returns the cached identity or ErrNotSignedIn.
func (c *Client) CurrentIdentity(ctx context.Context) (Identity, error) {
	if c == nil || c.isNop {
		return Identity{}, ErrFleetDisabled
	}
	if c.dataDir == "" {
		return Identity{}, ErrNotSignedIn
	}
	id, err := LoadIdentity(c.dataDir)
	if err != nil {
		return Identity{}, ErrNotSignedIn
	}
	return id, nil
}

// RefreshIdentity calls the fleet enroll endpoint and caches the result.
func (c *Client) RefreshIdentity(ctx context.Context, nodeID, platform, version string) (Identity, error) {
	if c == nil || c.isNop {
		return Identity{}, ErrFleetDisabled
	}
	if !c.profile.Configured() {
		return Identity{}, ErrProfileNotConfigured
	}
	return c.enrollIdentity(ctx, nodeID, platform, version)
}
