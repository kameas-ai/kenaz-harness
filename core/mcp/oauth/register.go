package oauth

// RFC 7591 Dynamic Client Registration (DCR) — POST to registration_endpoint.
//
// DCR allows the harness to register itself with any MCP authorization server
// that advertises a registration_endpoint in its RFC 8414 metadata, without
// requiring an operator to pre-register a client_id. The authorization server
// issues a fresh client_id (and optionally a client_secret) which we persist
// and reuse across launches.
//
// SECURITY: any client_secret returned by the registration endpoint MUST be
// stored only in the OS credstore (never plaintext on disk). The secret MUST
// NOT cross the Wails RPC boundary or appear in logs. RegisterClient returns
// the secret in-memory only; the caller is responsible for routing it through
// the credstore before it escapes to disk or logs.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClientMetadata is the registration request body sent to the
// registration_endpoint as defined by RFC 7591 §2.
type ClientMetadata struct {
	// ClientName is a human-readable name shown in the provider's consent UI.
	ClientName string `json:"client_name"`
	// RedirectURIs is the list of loopback redirect URIs the client will use.
	RedirectURIs []string `json:"redirect_uris"`
	// GrantTypes enumerates the OAuth grant types the client intends to use.
	GrantTypes []string `json:"grant_types"`
	// ResponseTypes enumerates the response types.
	ResponseTypes []string `json:"response_types"`
	// TokenEndpointAuthMethod is "none" for public clients (no client_secret).
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	// Scope is the space-separated set of scopes the client may request.
	Scope string `json:"scope,omitempty"`
}

// RegisteredClient is the response from a successful dynamic registration.
// It contains the server-issued credentials that must be persisted (via
// DCRStore) so we don't re-register on every launch.
//
// SECURITY: ClientSecret (when present) must be routed to the credstore
// immediately after RegisterClient returns. It must never be logged or
// returned through the Wails RPC boundary.
type RegisteredClient struct {
	// ClientID is the server-issued identifier. Always non-empty on success.
	ClientID string `json:"client_id"`
	// ClientSecret is present only when the server issued one (confidential
	// client variant). Public clients receive an empty string here.
	ClientSecret string `json:"client_secret,omitempty"`
	// ClientIDIssuedAt is the epoch time at which the client_id was issued.
	// Zero means the server did not return the field.
	ClientIDIssuedAt int64 `json:"client_id_issued_at,omitempty"`
	// ClientSecretExpiresAt is the epoch time at which the client_secret
	// expires. Zero or absent means the secret does not expire.
	// We must re-register when the current time is past this value.
	ClientSecretExpiresAt int64 `json:"client_secret_expires_at,omitempty"`
}

// SecretExpired reports whether the registered client's secret has expired
// at the given time. Returns false when no secret was issued or no expiry
// was provided (treat as non-expiring).
func (r *RegisteredClient) SecretExpired(now time.Time) bool {
	if r.ClientSecret == "" || r.ClientSecretExpiresAt == 0 {
		return false
	}
	return now.Unix() >= r.ClientSecretExpiresAt
}

// registrationErrorResponse is the RFC 7591 §3.2.2 error body.
type registrationErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RegisterClientOptions controls optional parameters for RegisterClient.
type RegisterClientOptions struct {
	// Scopes is the set of scopes to advertise in the metadata document.
	// When empty, the scope field is omitted from the request.
	Scopes []string
	// ExtraRedirectURIs appends additional redirect URIs. The standard
	// loopback pattern (http://127.0.0.1) is always included.
	ExtraRedirectURIs []string
}

// RegisterClient POSTs a dynamic client registration request to
// asm.RegistrationEndpoint per RFC 7591 §3.
//
// It registers a public client (token_endpoint_auth_method=none) with
// authorization_code + refresh_token grant types and a loopback redirect URI
// suitable for the interactive PKCE flow.
//
// Returns ErrNoDCREndpoint when asm.RegistrationEndpoint is empty.
// Returns a descriptive error on non-2xx HTTP responses.
//
// The returned RegisteredClient MUST be persisted via DCRStore before use.
// Any ClientSecret within it MUST be routed to the credstore, NOT written
// plaintext to disk.
func RegisterClient(ctx context.Context, client *http.Client, asm *AuthServerMetadata, opts RegisterClientOptions) (*RegisteredClient, error) {
	if asm.RegistrationEndpoint == "" {
		return nil, ErrNoDCREndpoint
	}
	if client == nil {
		client = http.DefaultClient
	}

	redirectURIs := []string{
		// RFC 8252 §7.3 relaxes loopback redirect-URI matching on the PORT
		// only — the authorization server MUST allow any port at request
		// time, but the scheme, host and PATH must still match the
		// registered value exactly. AuthorizeInteractive (loopback.go)
		// always sends "http://127.0.0.1:<ephemeral-port>/callback"; the
		// registered value must carry the same "/callback" path or a
		// conformant server rejects the redirect_uri as unregistered.
		// Registering the port-free form here (no scheme/host/path
		// wildcard exists in the spec) is the correct fix — pinning a
		// fixed port instead is unnecessary because §7.3 already makes
		// the port negotiable, unlike Slack's PKCE lane
		// (InteractiveConfig.FixedPort), which needs a fixed port only
		// because Slack requires an exact pre-registered redirect_uri
		// with no §7.3-style port relaxation.
		"http://127.0.0.1/callback",
	}
	redirectURIs = append(redirectURIs, opts.ExtraRedirectURIs...)

	meta := ClientMetadata{
		ClientName:              "Kenaz Harness",
		RedirectURIs:            redirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none", // public client
	}
	if len(opts.Scopes) > 0 {
		meta.Scope = strings.Join(opts.Scopes, " ")
	}

	body, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("oauth: dcr: marshal registration request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asm.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("oauth: dcr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: dcr: registration request: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth: dcr: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Attempt to parse RFC 7591 error body for a useful message.
		var errResp registrationErrorResponse
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error != "" {
			return nil, fmt.Errorf("oauth: dcr: registration failed (HTTP %d): %s: %s",
				resp.StatusCode, errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("oauth: dcr: registration endpoint returned HTTP %d", resp.StatusCode)
	}

	var rc RegisteredClient
	if err := json.Unmarshal(respBody, &rc); err != nil {
		return nil, fmt.Errorf("oauth: dcr: decode registration response: %w", err)
	}
	if rc.ClientID == "" {
		return nil, fmt.Errorf("oauth: dcr: registration response missing client_id")
	}
	return &rc, nil
}

// ErrNoDCREndpoint is returned by RegisterClient when the authorization server
// metadata carries no registration_endpoint — DCR is not supported.
var ErrNoDCREndpoint = fmt.Errorf("oauth: dcr: authorization server does not advertise a registration_endpoint")
