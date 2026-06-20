package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNoChallenge is returned when probing a server URL does not yield a 401
// with an MCP-spec resource_metadata pointer — the server either needs no auth
// or does not advertise the discovery flow.
var ErrNoChallenge = errors.New("oauth: server returned no OAuth challenge")

// ProbeChallenge sends an unauthenticated MCP `initialize` POST to serverURL
// and, on a 401, returns the resource_metadata URL advertised in the
// WWW-Authenticate header. A non-401 response yields ErrNoChallenge.
func ProbeChallenge(ctx context.Context, client *http.Client, serverURL string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kenaz-harness","version":"0"}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("oauth: build probe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: probe request: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		return "", ErrNoChallenge
	}
	return ParseResourceMetadataURL(resp.Header.Get("WWW-Authenticate"))
}

// Discover resolves a remote MCP server URL to the authorization-server
// metadata needed to run a grant. It probes for the 401 challenge, fetches the
// RFC 9728 protected-resource metadata, then resolves the first listed
// authorization server (RFC 8414 with the GitHub-shaped fallback).
func Discover(ctx context.Context, client *http.Client, serverURL string) (*AuthServerMetadata, *ProtectedResourceMetadata, error) {
	metaURL, err := ProbeChallenge(ctx, client, serverURL)
	if err != nil {
		return nil, nil, err
	}
	prm, err := FetchProtectedResourceMetadata(ctx, client, metaURL)
	if err != nil {
		return nil, nil, err
	}
	asm, err := DiscoverAuthServer(ctx, client, prm.AuthorizationServers[0])
	if err != nil {
		return nil, nil, err
	}
	return asm, prm, nil
}

// SignInConfig parameterizes a full remote-server sign-in.
type SignInConfig struct {
	// ServerURL is the remote MCP endpoint (e.g. the GitHub MCP server URL).
	ServerURL string
	// ClientID is the pre-registered OAuth client id (required — see RecipeAuth).
	ClientID string
	// Scopes requested; when empty, falls back to the protected-resource
	// metadata's advertised scopes.
	Scopes []string
	// OpenBrowser opens the authorization URL for the user.
	OpenBrowser func(authURL string) error
	// HTTPClient is used for discovery + token exchange; nil → default.
	HTTPClient *http.Client
	// Now stamps token expiry; nil → time.Now.
	Now func() time.Time
}

// SignIn runs discovery + the interactive authorization-code+PKCE grant and
// returns a StoredCredential ready to persist. It bundles the token-endpoint
// and client id with the tokens so later refreshes need no re-discovery.
func SignIn(ctx context.Context, cfg SignInConfig) (*StoredCredential, error) {
	if cfg.ClientID == "" {
		return nil, errors.New("oauth: sign-in requires a registered client_id")
	}
	asm, prm, err := Discover(ctx, cfg.HTTPClient, cfg.ServerURL)
	if err != nil {
		return nil, err
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = prm.ScopesSupported
	}
	tok, err := AuthorizeInteractive(ctx, InteractiveConfig{
		AuthServer:  asm,
		ClientID:    cfg.ClientID,
		Scopes:      scopes,
		OpenBrowser: cfg.OpenBrowser,
		HTTPClient:  cfg.HTTPClient,
		Now:         cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	return &StoredCredential{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		TokenType:     tok.TokenType,
		Scope:         tok.Scope,
		ExpiresAt:     tok.ExpiresAt,
		TokenEndpoint: asm.TokenEndpoint,
		ClientID:      cfg.ClientID,
	}, nil
}
