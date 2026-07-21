package oauth

// Package-level Slack OAuth constants and sign-in wiring.
//
// Slack uses authorization-code + PKCE S256 with a fixed-port loopback
// redirect URI (see loopback.go, SlackLoopbackPort). It is a public client
// (no client_secret sent). DCR is not advertised; the client_id must be
// provided by the operator or resolved at runtime via an env/build override.
//
// Live discovery (probed 2026-07-15):
//   - Remote MCP:              https://mcp.slack.com
//   - Authorization endpoint:  https://slack.com/oauth/v2_user/authorize
//   - Token endpoint:          https://slack.com/api/oauth.v2.user.access
//   - PKCE:                    S256 supported
//   - DCR:                     NOT advertised (no registration_endpoint)
//
// Because Slack does not publish RFC 8414 AS metadata, we provide static
// endpoint constants below and bypass the discovery round-trip. This avoids
// an extra network call on every sign-in and is safe because Slack's endpoints
// are stable documented URLs.
//
// TODO(slack-oauth): when the Kameas Slack app is registered, slot the real
// client_id into the recipe's auth.client_id field (registry.json) and remove
// the KAMEAS_SLACK_OAUTH_CLIENT_ID env override path.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Slack OAuth 2.0 authorization-server endpoints (static — no RFC 8414 metadata).
const (
	// SlackAuthorizationEndpoint is Slack's user OAuth authorization URL.
	SlackAuthorizationEndpoint = "https://slack.com/oauth/v2_user/authorize"
	// SlackTokenEndpoint is Slack's user OAuth token exchange URL.
	SlackTokenEndpoint = "https://slack.com/api/oauth.v2.user.access"
)

// SlackClientIDEnvVar is the environment / build-time override that supplies
// the Kameas Slack app's OAuth client_id when it is not baked into the recipe.
//
// TODO(slack-oauth): once the Kameas Slack app is registered, bake the real
// client_id directly into registry.json auth.client_id and retire this env var.
const SlackClientIDEnvVar = "KAMEAS_SLACK_OAUTH_CLIENT_ID"

// SlackDefaultScopes are the OAuth scopes requested when the recipe does not
// specify them explicitly.
var SlackDefaultScopes = []string{
	"channels:read",
	"channels:history",
	"chat:write",
	"users:read",
	"search:read",
}

// ErrSlackNoClientID is returned when neither the recipe auth.client_id field
// nor the KAMEAS_SLACK_OAUTH_CLIENT_ID env var is set. The Slack app must be
// registered with Slack's developer portal and the client_id must be supplied.
var ErrSlackNoClientID = fmt.Errorf(
	"oauth: Slack sign-in requires a registered client_id — "+
		"set %s or bake auth.client_id into the slack recipe; "+
		"TODO: register the Kameas Slack app at https://api.slack.com/apps",
	SlackClientIDEnvVar,
)

// SlackSignInConfig parameterizes the Slack PKCE sign-in flow.
type SlackSignInConfig struct {
	// ClientID is the OAuth client_id for the Kameas Slack app. When empty,
	// ResolveSlackClientID() is called to attempt resolution from the
	// KAMEAS_SLACK_OAUTH_CLIENT_ID env var. If still empty, sign-in fails with
	// ErrSlackNoClientID.
	//
	// SECURITY: this is a public client identifier (not a secret).
	ClientID string
	// Scopes requested. When empty, SlackDefaultScopes is used.
	Scopes []string
	// OpenBrowser opens the authorization URL; defaults to OpenSystemBrowser.
	OpenBrowser func(authURL string) error
	// HTTPClient used for the token exchange; nil → http.DefaultClient.
	HTTPClient *http.Client
	// Now stamps token expiry; nil → time.Now.
	Now func() time.Time
}

// ResolveSlackClientID returns the configured Slack OAuth client_id: it
// returns bakedClientID when non-empty (recipe auth.client_id path), then
// falls back to the KAMEAS_SLACK_OAUTH_CLIENT_ID environment variable.
// When neither is set it returns ("", ErrSlackNoClientID).
//
// SECURITY: the returned value is a public client identifier, not a secret.
func ResolveSlackClientID(bakedClientID string) (string, error) {
	if bakedClientID != "" {
		return bakedClientID, nil
	}
	if v := os.Getenv(SlackClientIDEnvVar); v != "" {
		return v, nil
	}
	return "", ErrSlackNoClientID
}

// SlackSignIn runs the Slack authorization-code + PKCE sign-in using the
// fixed-port loopback (SlackLoopbackPort). It does not call the discovery
// endpoints — Slack's authorization and token URLs are known statically.
//
// The returned StoredCredential is ready to persist to the credstore. The
// access token must be stored under the recipe's OAuth locator and injected as
// Authorization: Bearer <token> on every request to https://mcp.slack.com.
//
// SECURITY: the StoredCredential carries bearer + refresh secrets. Callers
// MUST persist it only through the OS keychain / credstore backend. It must
// never be returned via Wails RPC, logged, or written to plaintext files.
func SlackSignIn(ctx context.Context, cfg SlackSignInConfig) (*StoredCredential, error) {
	clientID, err := ResolveSlackClientID(cfg.ClientID)
	if err != nil {
		return nil, err
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = SlackDefaultScopes
	}

	openBrowser := cfg.OpenBrowser
	if openBrowser == nil {
		openBrowser = OpenSystemBrowser
	}

	// Use the static Slack authorization-server metadata — no discovery
	// round-trip needed because Slack does not publish RFC 8414 metadata.
	asm := &AuthServerMetadata{
		Issuer:                        "https://slack.com",
		AuthorizationEndpoint:         SlackAuthorizationEndpoint,
		TokenEndpoint:                 SlackTokenEndpoint,
		CodeChallengeMethodsSupported: []string{"S256"},
		// Slack does not advertise a registration_endpoint (no DCR).
		GrantTypesSupported: []string{"authorization_code"},
	}

	tok, err := AuthorizeInteractive(ctx, InteractiveConfig{
		AuthServer:  asm,
		ClientID:    clientID,
		Scopes:      scopes,
		OpenBrowser: openBrowser,
		HTTPClient:  cfg.HTTPClient,
		Now:         cfg.Now,
		FixedPort:   SlackLoopbackPort,
	})
	if err != nil {
		return nil, fmt.Errorf("slack sign-in: %w", err)
	}

	return &StoredCredential{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		TokenType:     tok.TokenType,
		Scope:         tok.Scope,
		ExpiresAt:     tok.ExpiresAt,
		TokenEndpoint: SlackTokenEndpoint,
		ClientID:      clientID,
	}, nil
}

// SlackSignInWithDiscovery is an alternative to SlackSignIn that uses the MCP
// OAuth discovery flow (RFC 9728 → RFC 8414) against mcp.slack.com rather than
// the static endpoints. Use this if Slack begins advertising RFC 8414 metadata.
// In the current (2026-07-15) Slack configuration, the discovery probe returns
// the same endpoints as the static constants above.
//
// SECURITY: see SlackSignIn for credential handling requirements.
func SlackSignInWithDiscovery(ctx context.Context, cfg SlackSignInConfig) (*StoredCredential, error) {
	clientID, err := ResolveSlackClientID(cfg.ClientID)
	if err != nil {
		return nil, err
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = SlackDefaultScopes
	}

	openBrowser := cfg.OpenBrowser
	if openBrowser == nil {
		openBrowser = OpenSystemBrowser
	}

	// Discover authorization-server metadata from the remote MCP endpoint.
	asm, prm, err := Discover(ctx, cfg.HTTPClient, "https://mcp.slack.com")
	if err != nil {
		return nil, fmt.Errorf("slack sign-in (discovery): discover auth server: %w", err)
	}

	if errors.Is(err, ErrNoChallenge) {
		// mcp.slack.com returned no 401 challenge — fall back to static endpoints.
		return SlackSignIn(ctx, cfg)
	}

	// Use scopes from discovery if none were requested.
	if len(scopes) == 0 {
		scopes = prm.ScopesSupported
	}

	tok, err := AuthorizeInteractive(ctx, InteractiveConfig{
		AuthServer:  asm,
		ClientID:    clientID,
		Scopes:      scopes,
		OpenBrowser: openBrowser,
		HTTPClient:  cfg.HTTPClient,
		Now:         cfg.Now,
		FixedPort:   SlackLoopbackPort,
	})
	if err != nil {
		return nil, fmt.Errorf("slack sign-in (discovery): authorize: %w", err)
	}

	return &StoredCredential{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		TokenType:     tok.TokenType,
		Scope:         tok.Scope,
		ExpiresAt:     tok.ExpiresAt,
		TokenEndpoint: asm.TokenEndpoint,
		ClientID:      clientID,
	}, nil
}
