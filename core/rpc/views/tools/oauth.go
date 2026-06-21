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
		// Not signed in yet — deferred auth. Leave the spec unauthenticated.
		return nil
	}
	valid, refreshed, err := oauth.EnsureValid(ctx, nil, cred, time.Now())
	if err != nil {
		// Token expired and unrefreshable → behave as deferred auth; the user
		// must sign in again. Don't fail the spawn.
		if errors.Is(err, oauth.ErrNotSignedIn) {
			return nil
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

// SignInRecipe runs the interactive MCP OAuth flow for a recipe (opening the
// system browser), persists the resulting credential to the keychain, then
// installs/respawns the recipe so the bearer takes effect immediately. The
// recipe must declare Auth.Kind == mcp_oauth with a non-empty ClientID (a
// registered OAuth app); otherwise a clear error is returned.
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
	if recipe.Auth.ClientID == "" {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q has no OAuth client_id configured — register an OAuth app and set auth.client_id", id)
	}

	cred, err := oauth.SignIn(ctx, oauth.SignInConfig{
		ServerURL:   recipe.URL,
		ClientID:    recipe.Auth.ClientID,
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
