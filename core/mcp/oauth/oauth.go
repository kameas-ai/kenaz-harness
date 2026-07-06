// Package oauth implements the client half of the Model Context Protocol
// authorization flow (the 2025 MCP spec, which composes RFC 9728 OAuth 2.0
// Protected Resource Metadata, RFC 8414 Authorization Server Metadata, and the
// OAuth 2.1 authorization-code grant with PKCE).
//
// It exists so the harness can sign in to remote (HTTP/SSE) MCP servers that
// require OAuth — e.g. GitHub's official remote server at
// https://api.githubcopilot.com/mcp/ — instead of asking the user to paste a
// long-lived personal access token. The flow:
//
//  1. A request to the MCP server returns 401 with a WWW-Authenticate header
//     carrying resource_metadata="…/.well-known/oauth-protected-resource/…".
//  2. Fetch that protected-resource metadata → authorization_servers[].
//  3. Fetch the authorization server's metadata (RFC 8414). Servers that do
//     not publish metadata (GitHub returns 404) fall back to conventional
//     endpoints derived from the issuer.
//  4. Run the authorization-code + PKCE grant over a loopback redirect and
//     exchange the code for an access (+ refresh) token.
//  5. Replay the MCP request with Authorization: Bearer <access token>; on
//     expiry, refresh.
//
// This package is deliberately transport-agnostic and side-effect-light: the
// pure protocol steps (parsing, discovery, token exchange, refresh) are
// independently testable; only AuthorizeInteractive performs I/O (loopback
// listener + browser open).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrNoResourceMetadata is returned when a 401 WWW-Authenticate header carries
// no resource_metadata pointer, so MCP-spec discovery cannot proceed.
var ErrNoResourceMetadata = errors.New("oauth: no resource_metadata in WWW-Authenticate header")

// ErrNoAuthorizationServer is returned when protected-resource metadata lists
// no authorization servers.
var ErrNoAuthorizationServer = errors.New("oauth: protected-resource metadata lists no authorization_servers")

// ProtectedResourceMetadata is the RFC 9728 document served at
// {resource}/.well-known/oauth-protected-resource[/{path}].
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
	BearerMethods        []string `json:"bearer_methods_supported"`
	ResourceName         string   `json:"resource_name"`
}

// AuthServerMetadata is the subset of the RFC 8414 authorization-server
// metadata the harness needs to run an authorization-code + PKCE grant.
type AuthServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	ScopesSupported               []string `json:"scopes_supported"`
}

// Tokens is the result of a successful grant or refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	// ExpiresAt is the absolute expiry; zero when the server returned no
	// expires_in (treated as non-expiring until a 401 says otherwise).
	ExpiresAt time.Time
}

// Expired reports whether the access token is at or past its expiry, with a
// small skew so we refresh slightly early rather than mid-request.
func (t Tokens) Expired(now time.Time) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(t.ExpiresAt.Add(-30 * time.Second))
}

// resourceMetadataRe extracts the resource_metadata parameter from a Bearer
// WWW-Authenticate challenge. The value is a quoted URL per RFC 9728 §5.1.
var resourceMetadataRe = regexp.MustCompile(`resource_metadata\s*=\s*"([^"]+)"`)

// ParseResourceMetadataURL pulls the resource_metadata URL out of a 401
// response's WWW-Authenticate header. Returns ErrNoResourceMetadata when the
// header carries no such pointer (the server is not advertising MCP-spec
// discovery and the caller must fall back to a configured authorization
// server, if any).
func ParseResourceMetadataURL(wwwAuthenticate string) (string, error) {
	m := resourceMetadataRe.FindStringSubmatch(wwwAuthenticate)
	if len(m) != 2 || strings.TrimSpace(m[1]) == "" {
		return "", ErrNoResourceMetadata
	}
	return m[1], nil
}

// FetchProtectedResourceMetadata GETs and decodes the RFC 9728 document.
func FetchProtectedResourceMetadata(ctx context.Context, client *http.Client, metadataURL string) (*ProtectedResourceMetadata, error) {
	var prm ProtectedResourceMetadata
	if err := getJSON(ctx, client, metadataURL, &prm); err != nil {
		return nil, fmt.Errorf("oauth: fetch protected-resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, ErrNoAuthorizationServer
	}
	return &prm, nil
}

// DiscoverAuthServer resolves an issuer URL to its authorization-code and
// token endpoints. It tries the two RFC 8414 well-known locations (the
// path-insertion form first, then the suffix form). When the issuer publishes
// no metadata — GitHub's https://github.com/login/oauth returns 404 — it falls
// back to the conventional endpoints derived from the issuer
// (issuer + "/authorize", issuer + "/access_token"), which matches GitHub and
// most pre-RFC-8414 providers.
func DiscoverAuthServer(ctx context.Context, client *http.Client, issuer string) (*AuthServerMetadata, error) {
	issuer = strings.TrimRight(issuer, "/")
	for _, candidate := range wellKnownCandidates(issuer) {
		var asm AuthServerMetadata
		if err := getJSON(ctx, client, candidate, &asm); err == nil && asm.AuthorizationEndpoint != "" && asm.TokenEndpoint != "" {
			if asm.Issuer == "" {
				asm.Issuer = issuer
			}
			return &asm, nil
		}
	}
	// Fallback: derive conventional endpoints. GitHub's OAuth lives at
	// {issuer}/authorize and {issuer}/access_token.
	tokenEndpoint := issuer + "/access_token"
	if strings.HasSuffix(issuer, "/oauth") || strings.Contains(issuer, "github.com/login/oauth") {
		tokenEndpoint = issuer + "/access_token"
	}
	return &AuthServerMetadata{
		Issuer:                        issuer,
		AuthorizationEndpoint:         issuer + "/authorize",
		TokenEndpoint:                 tokenEndpoint,
		CodeChallengeMethodsSupported: []string{"S256"},
		GrantTypesSupported:           []string{"authorization_code", "refresh_token"},
	}, nil
}

// wellKnownCandidates returns the RFC 8414 metadata URLs to try for an issuer.
// For issuer https://host/path the path-insertion form is
// https://host/.well-known/oauth-authorization-server/path and the suffix form
// is https://host/path/.well-known/oauth-authorization-server. The OpenID
// Connect discovery document is tried last.
func wellKnownCandidates(issuer string) []string {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil
	}
	path := strings.Trim(u.Path, "/")
	base := u.Scheme + "://" + u.Host
	out := make([]string, 0, 3)
	if path != "" {
		out = append(out, base+"/.well-known/oauth-authorization-server/"+path)
		out = append(out, base+"/.well-known/openid-configuration/"+path)
	}
	out = append(out, strings.TrimRight(issuer, "/")+"/.well-known/oauth-authorization-server")
	out = append(out, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration")
	return out
}

// PKCE holds a verifier/challenge pair for one authorization-code exchange.
type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// NewPKCE generates a high-entropy code verifier and its S256 challenge
// (RFC 7636).
func NewPKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("oauth: pkce entropy: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		Method:    "S256",
	}, nil
}

// randomState returns a URL-safe anti-CSRF state value.
func randomState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("oauth: state entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// BuildAuthorizationURL assembles the authorization-code request URL with PKCE.
func BuildAuthorizationURL(asm *AuthServerMetadata, clientID, redirectURI, state string, scopes []string, pkce PKCE) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	sep := "?"
	if strings.Contains(asm.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return asm.AuthorizationEndpoint + sep + q.Encode()
}

// tokenResponse is the OAuth token-endpoint JSON shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// ExchangeCode swaps an authorization code for tokens (RFC 6749 §4.1.3 with
// the RFC 7636 code_verifier). now stamps ExpiresAt from expires_in.
func ExchangeCode(ctx context.Context, client *http.Client, asm *AuthServerMetadata, clientID, code, redirectURI, codeVerifier string, now time.Time) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)
	return postToken(ctx, client, asm.TokenEndpoint, form, now)
}

// Refresh exchanges a refresh token for a fresh access token (RFC 6749 §6).
// Providers that rotate refresh tokens return a new one; when absent the prior
// refresh token is preserved.
func Refresh(ctx context.Context, client *http.Client, asm *AuthServerMetadata, clientID, refreshToken string, now time.Time) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	tok, err := postToken(ctx, client, asm.TokenEndpoint, form, now)
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// postToken POSTs an x-www-form-urlencoded body to the token endpoint and
// decodes the response, tolerating GitHub's form-encoded replies as well as
// JSON. It always sends Accept: application/json (GitHub honours this).
func postToken(ctx context.Context, client *http.Client, tokenEndpoint string, form url.Values, now time.Time) (*Tokens, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token request: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth: read token response: %w", err)
	}

	tr, parseErr := parseTokenBody(resp.Header.Get("Content-Type"), body)
	if parseErr != nil {
		return nil, parseErr
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("oauth: token endpoint error %q: %s", tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth: token endpoint status %d", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return nil, errors.New("oauth: token response carried no access_token")
	}
	out := &Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
	}
	if tr.ExpiresIn > 0 {
		out.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return out, nil
}

// parseTokenBody decodes a token-endpoint body, accepting JSON or (GitHub's
// legacy default) application/x-www-form-urlencoded.
func parseTokenBody(contentType string, body []byte) (*tokenResponse, error) {
	if strings.Contains(contentType, "application/json") {
		var tr tokenResponse
		if err := json.Unmarshal(body, &tr); err != nil {
			return nil, fmt.Errorf("oauth: decode token json: %w", err)
		}
		return &tr, nil
	}
	// Form-encoded fallback (GitHub when Accept is not honoured).
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		// Last resort: maybe it is JSON without the header.
		var tr tokenResponse
		if jsonErr := json.Unmarshal(body, &tr); jsonErr == nil {
			return &tr, nil
		}
		return nil, fmt.Errorf("oauth: decode token form body: %w", err)
	}
	tr := &tokenResponse{
		AccessToken:  vals.Get("access_token"),
		RefreshToken: vals.Get("refresh_token"),
		TokenType:    vals.Get("token_type"),
		Scope:        vals.Get("scope"),
		Error:        vals.Get("error"),
		ErrorDesc:    vals.Get("error_description"),
	}
	if exp := vals.Get("expires_in"); exp != "" {
		// expires_in is numeric; ignore parse failure (treated as no expiry).
		var n int
		_, _ = fmt.Sscanf(exp, "%d", &n)
		tr.ExpiresIn = n
	}
	return tr, nil
}

// getJSON GETs a URL and decodes a JSON body into v, requiring a 2xx status.
func getJSON(ctx context.Context, client *http.Client, rawURL string, v any) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
