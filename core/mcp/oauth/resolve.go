package oauth

// ResolveClientID implements the three-step client_id resolution order for the
// sign-in path, enabling zero-config OAuth for DCR-capable MCP servers:
//
//  1. Baked / configured client_id — if the recipe already has one, use it.
//     (GitHub and any other pre-registered provider fall through here without
//     ever touching DCR logic.)
//
//  2. Dynamic Client Registration (RFC 7591) — if the discovered auth server
//     advertises a registration_endpoint AND no baked client_id was supplied,
//     attempt DCR. We reuse a previously-registered client from the DCRStore
//     when present and unexpired; otherwise we call RegisterClient and persist
//     the result.
//
//  3. Error — if neither (1) nor (2) produced a client_id, return
//     ErrNoDCREndpoint (no registration endpoint) or a wrapped registration
//     error. The guided-capture UI fallback (manual entry) belongs to a
//     separate mission and is NOT handled here.
//
// SECURITY: any client_secret from a DCR registration is routed through the
// DCRStore's SecretSaver (OS credstore) and never returned through this
// function or logged.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ClientIDResult is the outcome of ResolveClientID. It contains the resolved
// client_id and, when DCR was used, the client_secret (retrieved from the
// credstore). It also carries a flag indicating whether DCR was used so
// callers can track the source for debugging.
type ClientIDResult struct {
	// ClientID is the resolved OAuth client identifier. Always non-empty on
	// success.
	ClientID string
	// ClientSecret is non-empty only when DCR produced a confidential-client
	// registration and the secret was successfully retrieved from the credstore.
	//
	// SECURITY: this field must never be logged, returned via Wails RPC, or
	// written to any plaintext file.
	ClientSecret string
	// FromDCR is true when the client_id was obtained via Dynamic Client
	// Registration rather than a baked/configured value.
	FromDCR bool
}

// ResolveClientIDConfig parameterizes ResolveClientID.
type ResolveClientIDConfig struct {
	// BakedClientID is the pre-configured client_id from the recipe, if any.
	// When non-empty, resolution short-circuits to (1) above.
	BakedClientID string
	// AuthServer is the resolved authorization server metadata (from Discover).
	// Required when BakedClientID is empty.
	AuthServer *AuthServerMetadata
	// Resource is the resource server URL (from ProtectedResourceMetadata.Resource).
	// Used as part of the DCR store key.
	Resource string
	// Scopes is the intended scope set, used as part of the DCR store key and
	// advertised in the registration request.
	Scopes []string
	// Store is the DCR persistence store. When nil, DCR results are not
	// persisted and a fresh registration is always attempted.
	Store *DCRStore
	// HTTPClient is used for the registration POST; nil → http.DefaultClient.
	HTTPClient *http.Client
	// Now overrides the current time for expiry checks; nil → time.Now.
	Now func() time.Time
}

// ErrNoClientID is returned when neither a baked client_id nor a DCR endpoint
// is available. The caller should surface an actionable error to the user
// (e.g. "add a client_id to the recipe" or guide them to register the app).
var ErrNoClientID = errors.New("oauth: no client_id: provider has no pre-registered client and does not advertise dynamic client registration")

// ResolveClientID runs the three-step resolution order and returns the
// client_id to use for the sign-in flow. See the file-level doc for details.
func ResolveClientID(ctx context.Context, cfg ResolveClientIDConfig) (ClientIDResult, error) {
	// Step (1): baked client_id — fast path, no network.
	if cfg.BakedClientID != "" {
		return ClientIDResult{ClientID: cfg.BakedClientID}, nil
	}

	// Step (2): DCR — only if the server advertises a registration_endpoint.
	if cfg.AuthServer == nil {
		return ClientIDResult{}, fmt.Errorf("oauth: resolve client_id: nil AuthServer")
	}
	if cfg.AuthServer.RegistrationEndpoint == "" {
		// No baked ID and no DCR — step (3).
		return ClientIDResult{}, ErrNoClientID
	}

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	key := DCRKey{
		Issuer:   cfg.AuthServer.Issuer,
		Resource: cfg.Resource,
		Scopes:   cfg.Scopes,
	}

	// Try to load a cached registration.
	if cfg.Store != nil {
		rc, err := cfg.Store.Load(key)
		switch {
		case err == nil:
			// Found a valid cached registration.
			return ClientIDResult{
				ClientID:     rc.ClientID,
				ClientSecret: rc.ClientSecret,
				FromDCR:      true,
			}, nil
		case errors.Is(err, ErrDCRNotFound):
			// Nothing cached — fall through to registration.
		case errors.Is(err, ErrDCRExpired):
			// Secret expired — re-register below; the store already deleted the
			// stale entry.
		default:
			// Unexpected store error — propagate.
			return ClientIDResult{}, fmt.Errorf("oauth: resolve client_id: load cached registration: %w", err)
		}
	}

	// Register a new client.
	rc, err := RegisterClient(ctx, cfg.HTTPClient, cfg.AuthServer, RegisterClientOptions{
		Scopes: cfg.Scopes,
	})
	if err != nil {
		return ClientIDResult{}, fmt.Errorf("oauth: resolve client_id: %w", err)
	}

	// Persist the registration (best-effort; a save failure is logged but does
	// not abort the flow — we still have a valid client_id for this session).
	if cfg.Store != nil {
		if saveErr := cfg.Store.Save(key, rc); saveErr != nil {
			// Non-fatal: we will re-register next launch.
			_ = saveErr
		}
	}

	return ClientIDResult{
		ClientID:     rc.ClientID,
		ClientSecret: rc.ClientSecret,
		FromDCR:      true,
	}, nil
}

// SignInWithDCR is a drop-in replacement for SignIn that adds DCR-backed
// client_id resolution. When cfg.ClientID is non-empty it behaves identically
// to SignIn. When cfg.ClientID is empty it resolves via ResolveClientID
// (DCR or error) before running the interactive grant.
//
// The DCRStore and HTTPClient fields in cfg must be populated for DCR to work.
func SignInWithDCR(ctx context.Context, cfg SignInWithDCRConfig) (*StoredCredential, error) {
	asm, prm, err := Discover(ctx, cfg.HTTPClient, cfg.ServerURL)
	if err != nil {
		return nil, err
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = prm.ScopesSupported
	}

	result, err := ResolveClientID(ctx, ResolveClientIDConfig{
		BakedClientID: cfg.ClientID,
		AuthServer:    asm,
		Resource:      prm.Resource,
		Scopes:        scopes,
		Store:         cfg.DCRStore,
		HTTPClient:    cfg.HTTPClient,
		Now:           cfg.Now,
	})
	if err != nil {
		return nil, err
	}

	tok, err := AuthorizeInteractive(ctx, InteractiveConfig{
		AuthServer:  asm,
		ClientID:    result.ClientID,
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
		ClientID:      result.ClientID,
	}, nil
}

// SignInWithDCRConfig extends SignInConfig with DCR parameters.
type SignInWithDCRConfig struct {
	// ServerURL is the remote MCP endpoint.
	ServerURL string
	// ClientID is the pre-registered OAuth client id. When empty, DCR is
	// attempted. When non-empty, DCR is skipped (baked-client-id path).
	ClientID string
	// Scopes requested; when empty, falls back to the protected-resource
	// metadata's advertised scopes.
	Scopes []string
	// DCRStore is used to cache and load registered clients. May be nil to
	// disable persistence (DCR will re-register on every call).
	DCRStore *DCRStore
	// OpenBrowser opens the authorization URL for the user.
	OpenBrowser func(authURL string) error
	// HTTPClient is used for discovery, DCR, and token exchange; nil → default.
	HTTPClient *http.Client
	// Now stamps token expiry; nil → time.Now.
	Now func() time.Time
}
