package tools

// MCP OAuth sign-in (harness-fleet/MCP authorization spec). For recipes whose
// Auth.Kind == mcp_oauth (e.g. GitHub's official remote MCP server), the
// harness obtains a bearer token via the OAuth authorization-code+PKCE flow
// (core/mcp/oauth) and injects it as the Authorization header — instead of
// asking the user to paste a personal access token.
//
// Persistence: the StoredCredential blob lives in the OS keychain under
// recipes.OAuthCredentialLocator(id). It is loaded + refreshed at spawn time
// and re-persisted when a refresh rotates the tokens.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/oauth"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// loadOAuthCredential reads + parses the StoredCredential for a recipe from the
// keychain. Returns (nil, nil) when none is stored (the user has not signed in
// yet — a deferred-auth install), distinguishing "absent" from "error".
func (a *API) loadOAuthCredential(ctx context.Context, recipeID string) (*oauth.StoredCredential, error) {
	if a.cfg.Secrets == nil {
		return nil, nil
	}
	ref := secrets.CredentialReference{
		Kind:     secrets.RefKeychain,
		Locator:  recipes.OAuthCredentialLocator(recipeID),
		Optional: true,
	}
	s, err := a.cfg.Secrets.Resolve(ctx, ref)
	if err != nil {
		// Absent / unresolvable optional credential → treat as not-signed-in.
		return nil, nil //nolint:nilerr // absent credential is not an error here
	}
	defer s.Destroy()

	var cred *oauth.StoredCredential
	useErr := s.Use(func(v []byte) error {
		c, e := oauth.UnmarshalCredential(v)
		if e != nil {
			return e
		}
		cred = c
		return nil
	})
	if useErr != nil {
		return nil, fmt.Errorf("tools: read oauth credential for %q: %w", recipeID, useErr)
	}
	return cred, nil
}

// persistOAuthCredential writes a StoredCredential blob to the keychain.
func (a *API) persistOAuthCredential(ctx context.Context, recipeID string, cred *oauth.StoredCredential) error {
	if a.cfg.Keychain == nil {
		return errors.New("tools: no keychain configured for oauth credential")
	}
	blob, err := cred.Marshal()
	if err != nil {
		return fmt.Errorf("tools: marshal oauth credential: %w", err)
	}
	if err := a.cfg.Keychain.Write(ctx, recipes.OAuthCredentialLocator(recipeID), blob); err != nil {
		return fmt.Errorf("tools: persist oauth credential for %q: %w", recipeID, err)
	}
	return nil
}

// injectOAuthBearer adds the Authorization: Bearer header to a remote recipe's
// ServerSpec from the stored OAuth credential, refreshing the token first if it
// has expired (and re-persisting the rotated tokens). It is a no-op for
// non-OAuth recipes and for OAuth recipes with no stored credential yet
// (deferred auth — the install succeeds; the server stays unauthenticated until
// the user signs in). Errors are returned only for genuine failures (bad
// keychain blob, refresh failure); absence is not an error.
func (a *API) injectOAuthBearer(ctx context.Context, recipe recipes.Recipe, spec *coremcp.ServerSpec) error {
	if recipe.Auth == nil || recipe.Auth.Kind != recipes.AuthKindMCPOAuth {
		return nil
	}
	cred, err := a.loadOAuthCredential(ctx, recipe.ID)
	if err != nil {
		return err
	}
	if cred == nil {
		// No local credential. In served mode (spec 091 D8) fall back to a
		// host-brokered short-lived token; otherwise deferred auth — leave
		// the spec unauthenticated.
		return a.injectBrokerBearer(ctx, recipe, spec)
	}
	valid, refreshed, err := oauth.EnsureValid(ctx, nil, cred, time.Now())
	if err != nil {
		// Token expired and unrefreshable → behave as deferred auth (with
		// the served broker fallback); the user must sign in again. Don't
		// fail the spawn.
		if errors.Is(err, oauth.ErrNotSignedIn) {
			return a.injectBrokerBearer(ctx, recipe, spec)
		}
		return fmt.Errorf("tools: ensure oauth token for %q: %w", recipe.ID, err)
	}
	if refreshed {
		if perr := a.persistOAuthCredential(ctx, recipe.ID, valid); perr != nil {
			// Non-fatal: we can still use the freshly-refreshed token this run.
			a.emit(ctx, "mcp.oauth.persist_failed", map[string]any{"recipe_id": recipe.ID, "err": perr.Error()})
		}
	}
	if spec.HeadersTemplate == nil {
		spec.HeadersTemplate = map[string]string{}
	}
	spec.HeadersTemplate["Authorization"] = valid.AuthorizationHeader()
	return nil
}

// injectBrokerBearer is the served-mode fallback for OAuth recipes with no
// locally-stored credential (spec 091 D8): the host auth broker mints a
// short-lived access token for the whitelisted connector — the refresh
// token never crosses into the VM. A missing source or a broker failure
// is deferred auth, never a spawn failure; the token bytes are never
// logged or emitted.
func (a *API) injectBrokerBearer(ctx context.Context, recipe recipes.Recipe, spec *coremcp.ServerSpec) error {
	if a.cfg.ConnectorTokens == nil {
		return nil
	}
	tok, err := a.cfg.ConnectorTokens.ConnectorToken(ctx, recipe.ID)
	if err != nil || tok == "" {
		if err != nil {
			a.emit(ctx, "mcp.oauth.broker_fallback_failed",
				map[string]any{"recipe_id": recipe.ID, "err": err.Error()})
		}
		return nil
	}
	if spec.HeadersTemplate == nil {
		spec.HeadersTemplate = map[string]string{}
	}
	spec.HeadersTemplate["Authorization"] = "Bearer " + tok
	return nil
}

// unresolvedToken reports whether s is still a literal, unsubstituted
// "${NAME}" token, returning NAME. recipes.substituteString (D-1) leaves an
// unknown/unresolved token literal and only logs a warn, so "no error from
// ResolveEnv" proves nothing about whether a given token actually resolved —
// callers that need to know must check the result explicitly, which is what
// this does.
func unresolvedToken(s string) (string, bool) {
	if len(s) < 4 || !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return "", false
	}
	return s[2 : len(s)-1], true
}

// resolveOAuthClientCredentials resolves and substitutes any "${VAR}" token
// recipe.Auth.ClientID / .ClientSecret carries (FR-003), routing both
// through the same recipes.ResolveEnv + recipes.SubstituteString path the
// stdio spawn path already uses (impl.go:348). Literal (non-token) values —
// including "" and GitHub's real baked device-flow client id — pass through
// untouched and never touch the secrets backend, so recipes with no BYO
// credential are unaffected and this performs zero network I/O either way.
//
// A token that survives substitution — because the env key is unset, or
// (D-1) because it is an *optional* key ResolveEnv silently skipped — is
// reported as a user-actionable error naming the still-unresolved env key.
// The returned clientSecret is not yet consumed by any OAuth flow (that is
// confidential-client support, a signature change deferred to a later unit
// — spec.md §1.12 R-3); it is resolved here only so FR-003's contract ("both
// fields resolved through the same env path before any OAuth config is
// built") holds for the whole RecipeAuth, and so a caller that gains a
// secret-consuming code path later does not have to re-derive this seam.
// The resolved secret must never reach a log line, an error string, or a
// Wails wire type (D-2) — this function returns it only to the caller's
// local Go variable.
func (a *API) resolveOAuthClientCredentials(ctx context.Context, recipe recipes.Recipe) (clientID, clientSecret string, err error) {
	if recipe.Auth == nil {
		return "", "", nil
	}
	clientID, clientSecret = recipe.Auth.ClientID, recipe.Auth.ClientSecret
	if !strings.Contains(clientID, "${") && !strings.Contains(clientSecret, "${") {
		return clientID, clientSecret, nil
	}
	if a.cfg.Secrets == nil {
		return "", "", errors.New("tools: no secrets backend configured")
	}
	resolved, rerr := recipes.ResolveEnv(ctx, a.cfg.Secrets, recipe)
	if rerr != nil {
		return "", "", fmt.Errorf("tools: recipe %q: resolving OAuth credentials: %w", recipe.ID, rerr)
	}
	clientID = recipes.SubstituteString(clientID, resolved)
	clientSecret = recipes.SubstituteString(clientSecret, resolved)
	if key, ok := unresolvedToken(clientID); ok {
		return "", "", fmt.Errorf("tools: recipe %q: OAuth client_id references unset env key %s — add it in the connector's setup", recipe.ID, key)
	}
	if key, ok := unresolvedToken(clientSecret); ok {
		return "", "", fmt.Errorf("tools: recipe %q: OAuth client_secret references unset env key %s — add it in the connector's setup", recipe.ID, key)
	}
	return clientID, clientSecret, nil
}

// SignInRecipe runs the interactive MCP OAuth flow for a recipe (opening the
// system browser), persists the resulting credential to the keychain, then
// installs/respawns the recipe so the bearer takes effect immediately. The
// recipe must declare Auth.Kind == mcp_oauth with a client id that resolves
// to a non-empty value — either a literal (a registered OAuth app) or a
// "${VAR}" token substituted from the operator's own bring-your-own app
// (FR-003, FR-003b) — otherwise a clear, user-actionable error is returned
// and no HTTP request is made.
func (a *API) SignInRecipe(ctx context.Context, id string) (stdio.RecipeStatus, error) {
	a.mu.Lock()
	if a.cfg.Catalog == nil {
		a.mu.Unlock()
		return stdio.RecipeStatus{}, errors.New("tools: no catalog configured")
	}
	recipe, ok := a.cfg.Catalog.Get(id)
	a.mu.Unlock()
	if !ok {
		return stdio.RecipeStatus{}, fmt.Errorf("%w: %q", recipes.ErrRecipeNotFound, id)
	}
	if recipe.Auth == nil || recipe.Auth.Kind != recipes.AuthKindMCPOAuth {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q is not an OAuth recipe", id)
	}
	clientID, _, err := a.resolveOAuthClientCredentials(ctx, recipe)
	if err != nil {
		return stdio.RecipeStatus{}, err
	}
	if clientID == "" {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q has no OAuth client_id configured — register an OAuth app and set auth.client_id", id)
	}

	cred, err := oauth.SignIn(ctx, oauth.SignInConfig{
		ServerURL:   recipe.URL,
		ClientID:    clientID,
		Scopes:      recipe.Auth.Scopes,
		OpenBrowser: oauth.OpenSystemBrowser,
	})
	if err != nil {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: oauth sign-in for %q: %w", id, err)
	}
	if perr := a.persistOAuthCredential(ctx, id, cred); perr != nil {
		return stdio.RecipeStatus{}, perr
	}
	a.emit(ctx, "mcp.oauth.signed_in", map[string]any{"recipe_id": id, "scope": cred.Scope})

	// Install/respawn so injectOAuthBearer picks up the freshly-stored token.
	return a.InstallRecipe(ctx, id, nil, nil)
}
