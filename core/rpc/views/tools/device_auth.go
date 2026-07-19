package tools

// Device-flow sign-in for GitHub (RFC 8628) — harness-side implementation.
//
// The GitHub device flow cannot use the existing loopback PKCE path (GitHub
// requires an exact-match callback URL and rejects ephemeral random-port
// redirects). Instead:
//
//  1. BeginDeviceAuth POSTs to the GitHub device-authorization endpoint and
//     returns only the safe display fields (user_code + verification_uri) to
//     the frontend. The DeviceAuthorizationResponse (containing device_code) is
//     stored in memory; the device_code MUST NOT cross the Wails boundary.
//
//  2. PollDeviceAuth polls the GitHub token endpoint until the user approves
//     (or the code expires / the user denies). The resulting ghu_ token is
//     persisted directly to the OS keychain/credstore and NEVER returned via
//     any Wails RPC value, frontend state, or log line. The caller receives a
//     plain RecipeStatus.
//
// SECURITY invariant: the ghu_ bearer token must remain in the Go process's
// memory until it is written to the credstore. scripts/ci/check-no-cred-bytes-
// in-rpc.sh enforces no credential leaks through the Wails boundary at PR gate.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/oauth"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
)

// DeviceAuthBeginResult is the wire shape returned by BeginDeviceAuth.
// It contains ONLY the display-safe fields (no device_code, no token).
// The frontend shows UserCode + a link to VerificationURI so the user
// can authorize in a browser tab.
type DeviceAuthBeginResult struct {
	// UserCode is the short human-readable code the user enters at VerificationURI.
	UserCode string `json:"userCode"`
	// VerificationURI is the URL the user visits to enter UserCode.
	VerificationURI string `json:"verificationUri"`
	// ExpiresIn is the number of seconds until the device code expires.
	ExpiresIn int `json:"expiresIn"`
}

// pendingDeviceAuth holds the in-flight device authorization state for one
// recipe (keyed by recipe ID). It is used to correlate a BeginDeviceAuth
// call with its subsequent PollDeviceAuth call from the same frontend session.
type pendingDeviceAuth struct {
	dar *oauth.DeviceAuthorizationResponse
	cfg oauth.DeviceConfig
	// pollCtx is cancelled by cancel() when this session is replaced by a
	// newer BeginDeviceAuth for the same recipe; PollDeviceAuth derives its
	// poll context from it so an in-flight poll is actually interrupted.
	pollCtx context.Context
	cancel  context.CancelFunc
}

// deviceAuthSessions holds in-progress device auth sessions keyed by recipe ID.
// Only one session per recipe is active at a time; a new BeginDeviceAuth call
// cancels and replaces any previous session for that recipe.
var deviceAuthSessions sync.Map // map[string]*pendingDeviceAuth

// BeginDeviceAuth starts the RFC 8628 device-authorization flow for a recipe
// whose PrimaryAuth == "device_code" and Auth.Kind == "mcp_oauth".
// It posts to the device-authorization endpoint and returns the
// display-safe DeviceAuthBeginResult. The device_code is kept in memory;
// the frontend receives only user_code + verification_uri.
func (a *API) BeginDeviceAuth(ctx context.Context, id string) (DeviceAuthBeginResult, error) {
	a.mu.Lock()
	if a.cfg.Catalog == nil {
		a.mu.Unlock()
		return DeviceAuthBeginResult{}, errors.New("tools: no catalog configured")
	}
	recipe, ok := a.cfg.Catalog.Get(id)
	a.mu.Unlock()
	if !ok {
		return DeviceAuthBeginResult{}, fmt.Errorf("%w: %q", recipes.ErrRecipeNotFound, id)
	}
	if recipe.Auth == nil || recipe.Auth.Kind != recipes.AuthKindMCPOAuth {
		return DeviceAuthBeginResult{}, fmt.Errorf("tools: recipe %q is not an OAuth recipe", id)
	}
	if recipe.Auth.ClientID == "" {
		return DeviceAuthBeginResult{}, fmt.Errorf("tools: recipe %q has no OAuth client_id configured", id)
	}

	cfg := oauth.GitHubDeviceConfig(recipe.Auth.ClientID, recipe.Auth.Scopes, nil)
	dar, err := oauth.RequestDeviceAuthorization(ctx, cfg)
	if err != nil {
		return DeviceAuthBeginResult{}, fmt.Errorf("tools: device auth request for %q: %w", id, err)
	}

	// Cancel any previous pending session for this recipe.
	if prev, loaded := deviceAuthSessions.LoadAndDelete(id); loaded {
		prev.(*pendingDeviceAuth).cancel()
	}
	pollCtx, cancel := context.WithCancel(context.Background())
	deviceAuthSessions.Store(id, &pendingDeviceAuth{dar: dar, cfg: cfg, pollCtx: pollCtx, cancel: cancel})

	return DeviceAuthBeginResult{
		UserCode:        dar.UserCode,
		VerificationURI: dar.VerificationURI,
		ExpiresIn:       dar.ExpiresIn,
	}, nil
}

// PollDeviceAuth polls the GitHub token endpoint for the device auth session
// started by a prior BeginDeviceAuth call for the same recipe id. It blocks
// until the user approves, the code expires, the user denies, or ctx is
// cancelled.
//
// On success the ghu_ token is written to the credstore and NEVER returned
// via any RPC value or frontend state. The recipe is installed/respawned so
// the bearer token takes effect immediately.
//
// TODO (live verify): confirm that api.githubcopilot.com/mcp/ accepts the
// resulting ghu_ token as a Bearer. This requires a human in the app to
// complete the GitHub device flow against the real Kameas GitHub App
// (client_id=Iv23li6LDja9hM0dAJGV). The full machine-to-machine path from
// PollDeviceAuth → credstore → injectOAuthBearer → MCP transport → GitHub
// MCP endpoint is wired but has not been end-to-end tested against production.
func (a *API) PollDeviceAuth(ctx context.Context, id string) (stdio.RecipeStatus, error) {
	rawSession, ok := deviceAuthSessions.Load(id)
	if !ok {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: no pending device auth session for recipe %q — call BeginDeviceAuth first", id)
	}
	session := rawSession.(*pendingDeviceAuth)

	// Poll under a context that stops if EITHER the caller cancels (the Wails
	// binding ctx) OR a newer BeginDeviceAuth replaces this session (session.pollCtx,
	// cancelled by the previous session's cancel()). Without merging both, a second
	// sign-in attempt could not interrupt an in-flight poll.
	combinedCtx, cancelCombined := context.WithCancel(ctx)
	defer cancelCombined()
	go func() {
		select {
		case <-session.pollCtx.Done():
			cancelCombined()
		case <-combinedCtx.Done():
		}
	}()

	// PollDeviceToken blocks until done (or combinedCtx cancelled / expiry).
	tok, err := oauth.PollDeviceToken(combinedCtx, session.cfg, session.dar)
	// Always clean up the session entry, whether we succeeded or not.
	deviceAuthSessions.Delete(id)
	session.cancel()

	if err != nil {
		if errors.Is(err, oauth.ErrDeviceExpired) {
			return stdio.RecipeStatus{}, fmt.Errorf("tools: device code expired for %q — start sign-in again", id)
		}
		if errors.Is(err, oauth.ErrDeviceAccessDenied) {
			return stdio.RecipeStatus{}, fmt.Errorf("tools: GitHub sign-in denied for %q", id)
		}
		return stdio.RecipeStatus{}, fmt.Errorf("tools: device poll for %q: %w", id, err)
	}

	// Build a StoredCredential from the device-flow token.
	// GitHub device tokens (ghu_) have no refresh token and no expires_in;
	// treat as non-expiring until a 401 re-triggers the flow.
	cred := &oauth.StoredCredential{
		AccessToken:  tok.AccessToken,
		TokenType:    tok.TokenType,
		Scope:        tok.Scope,
		ExpiresAt:    time.Time{}, // non-expiring
		TokenEndpoint: "",         // no refresh flow for ghu_ tokens
		ClientID:     session.cfg.ClientID,
	}

	// Persist to credstore. The token MUST NOT appear in any return value.
	if perr := a.persistOAuthCredential(ctx, id, cred); perr != nil {
		return stdio.RecipeStatus{}, perr
	}

	a.emit(ctx, "mcp.oauth.device_signed_in", map[string]any{
		"recipe_id": id,
		"scope":     cred.Scope,
	})

	// Install/respawn so injectOAuthBearer picks up the freshly-stored token.
	return a.InstallRecipe(ctx, id, nil, nil)
}
