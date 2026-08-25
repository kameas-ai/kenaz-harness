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

// newDCRStore builds the Dynamic Client Registration (RFC 7591) persistence
// store for the current process (spec.md kitty-specs/connector-lifecycle-
// truth-01PMZ303 UNIT-3 3e), or returns nil when persistence cannot be
// anchored to a data directory. A nil store is safe by construction —
// oauth.ResolveClientID/SignInWithDCR treat cfg.Store == nil as "always
// re-register", which is exactly today's (pre-3e) behaviour, never a panic.
//
// SecretSaver/SecretLoader are wired over the same
// Resolve/Use/Destroy + Keychain.Write seam loadOAuthCredential and
// persistOAuthCredential already use for the per-recipe OAuth credential,
// keyed through recipes.DCRClientSecretLocator instead of
// recipes.OAuthCredentialLocator — DCR secrets are addressed by DCRKey
// (issuer+resource+scopes), not recipe ID, so they need a distinct locator
// namespace.
//
// Both closures are supplied whenever their underlying dependency exists.
// This is deliberate, not incidental: DCRStore.Save only guards the
// credstore write with "rc.ClientSecret != "" && s.saveFn != nil" — when
// saveFn is nil it skips the credstore write silently and still commits a
// has_secret:true JSON entry (dcr_store.go Save), which DCRStore.Load can
// then never populate a secret for (Load's credstore read is itself guarded
// on s.loadFn != nil). A nil SecretSaver here would not "gracefully support
// only public clients" as NewDCRStore's doc allows for — on any provider
// that returns a client_secret it would silently produce a cached entry
// that claims to have one and can never retrieve it, which is worse than
// not caching at all. So this passes nil only when the dependency it would
// close over (a.cfg.Keychain / a.cfg.Secrets) is itself nil — a
// configuration that today's production wiring (core/rpc/api.go) never
// produces; DataDir, Keychain and Secrets are always set together.
// openBrowser resolves the authorization-URL hook: the injected test seam
// when set, oauth.OpenSystemBrowser otherwise. See Config.OpenBrowser for
// why the seam exists.
func (a *API) openBrowser() func(authURL string) error {
	if a.cfg.OpenBrowser != nil {
		return a.cfg.OpenBrowser
	}
	return oauth.OpenSystemBrowser
}

func (a *API) newDCRStore(ctx context.Context) *oauth.DCRStore {
	if a.cfg.DataDir == "" {
		return nil
	}
	var saveFn oauth.SecretSaver
	if a.cfg.Keychain != nil {
		saveFn = func(key, secret string) error {
			return a.cfg.Keychain.Write(ctx, recipes.DCRClientSecretLocator(key), []byte(secret))
		}
	}
	var loadFn oauth.SecretLoader
	if a.cfg.Secrets != nil {
		loadFn = func(key string) (string, error) {
			ref := secrets.CredentialReference{
				Kind:     secrets.RefKeychain,
				Locator:  recipes.DCRClientSecretLocator(key),
				Optional: true,
			}
			s, err := a.cfg.Secrets.Resolve(ctx, ref)
			if err != nil {
				// Absent / unresolvable optional credential → no secret
				// stored yet. Matches SecretLoader's documented contract:
				// return ("", nil) when nothing is stored.
				return "", nil //nolint:nilerr // absent credential is not an error here
			}
			defer s.Destroy()
			var out string
			useErr := s.Use(func(v []byte) error {
				out = string(v)
				return nil
			})
			if useErr != nil {
				return "", fmt.Errorf("tools: read dcr client_secret: %w", useErr)
			}
			return out, nil
		}
	}
	return oauth.NewDCRStore(oauth.DefaultDCRStorePath(a.cfg.DataDir), saveFn, loadFn)
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
// installs/respawns the recipe so the bearer takes effect immediately.
//
// The dispatch is arm-aware (spec.md §1.8/§1.9, kitty-specs/connector-
// lifecycle-truth-01PMZ303 UNIT-3 3f): every Recipe.PrimaryAuth value gets a
// named disposition instead of the single "clientID == ''" reject this used
// to fall back to for every arm alike. An arm that cannot complete a sign-in
// fails closed here with a message naming the reason — never a provider
// round-trip and never the generic "has no OAuth client_id configured"
// error, which used to fire identically whether the recipe was DCR-capable,
// PKCE-with-a-missing-BYO-id, or had no working path at all.
//
//   - PrimaryAuthOAuth ("oauth", 6 recipes, 4 with an Auth block and 2
//     without — spec.md §1.9): fails closed unconditionally. None of the six
//     is DCR-capable, PKCE-with-a-client-id, or device-code, and whether it
//     moves to one of those arms is an open product question (E-006), not
//     something this unit resolves.
//   - PrimaryAuthBrowserOAuthPKCE ("browser_oauth_pkce", 16 recipes): wired
//     when the client id resolves to a non-empty value (14 via UNIT-1's
//     ${VAR} substitution); the 2 that ship client_id: "" (google-docs,
//     google-sheets) fail closed with a named blocker instead of attempting
//     DCR against a provider this arm declares is not DCR-capable.
//   - PrimaryAuthBrowserOAuthDCR ("browser_oauth_dcr", 30 recipes): wired —
//     routed through oauth.SignInWithDCR, which resolves an empty client id
//     via RFC 7591 dynamic client registration. UNIT-0's ★1 (does DCR
//     actually succeed against a real provider) was never executed — this
//     environment has no live network access to third-party providers to
//     verify it (see the UNIT-3 commit body) — so this ships the honest
//     attempt without the confidential-client or registration-recovery half
//     (spec.md §1.12 R-3/R-4, UNIT-3 3b/3c): registration is always a
//     public client (TokenEndpointAuthMethod "none" is still hardcoded) and
//     a revoked registration has no automatic recovery yet. Cross-launch
//     persistence (UNIT-3 3e) IS wired — newDCRStore below anchors the
//     registered client_id (and any client_secret a provider returns
//     anyway) at <DataDir>/oauth/dcr_clients.json, so a repeat sign-in
//     reuses the cached registration instead of registering a new OAuth
//     client with the provider on every call. A provider that does not
//     accept DCR from an unregistered client (registry.json's own vercel
//     warning flags this as expected, not a surprise) surfaces
//     oauth.SignInWithDCR's real discovery/registration error, not a
//     client-id-shaped one.
//   - PrimaryAuthDeviceCode, PrimaryAuthKeys, PrimaryAuthNone: named
//     dispositions that reject explicitly — these are not OAuth sign-in
//     arms and SignInRecipe is not their entry point (device_code uses
//     BeginDeviceAuth/PollDeviceAuth instead).
//   - Legacy/unset primary_auth (""): the pre-existing bare "clientID == ''"
//     guard, for any recipe with a baked client id outside the six-arm
//     taxonomy.
//
// Auth.Kind == mcp_oauth is still required for every arm reachable past the
// oauth-arm short-circuit below.
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

	// The oauth arm (spec.md §1.9) is checked before the Auth-nil guard
	// below because 2 of its 6 recipes (google-calendar, google-drive)
	// carry no Auth block at all — a third state, not a subset of "oauth
	// with an empty client_id" — and both must still name the real
	// blocker (E-006) instead of falling through to the generic
	// "not an OAuth recipe" message.
	if recipe.PrimaryAuth == recipes.PrimaryAuthOAuth {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q has no working sign-in path yet (primary_auth=%q, E-006 — kitty-specs/connector-lifecycle-truth-01PMZ303): it is not dynamically-registerable, ships no pre-registered client id, and has no device-code flow", id, recipes.PrimaryAuthOAuth)
	}

	if recipe.Auth == nil || recipe.Auth.Kind != recipes.AuthKindMCPOAuth {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q is not an OAuth recipe", id)
	}
	clientID, _, err := a.resolveOAuthClientCredentials(ctx, recipe)
	if err != nil {
		return stdio.RecipeStatus{}, err
	}

	switch recipe.PrimaryAuth {
	case recipes.PrimaryAuthBrowserOAuthDCR:
		// clientID may legitimately be empty — oauth.SignInWithDCR below
		// resolves it via RFC 7591 dynamic client registration.
	case recipes.PrimaryAuthBrowserOAuthPKCE:
		if clientID == "" {
			return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q has no pre-registered OAuth client_id and its provider does not support dynamic client registration (browser_oauth_pkce) — register an OAuth app with the provider and set auth.client_id; Kameas does not register or host one for this connector", id)
		}
	case recipes.PrimaryAuthDeviceCode:
		// Named disposition, not a fallthrough: this is GitHub's shape today
		// (baked client id, Auth.Kind mcp_oauth, primary_auth=device_code).
		// Routing it through the loopback authorization-code+PKCE grant below
		// would reach the browser and then fail late — GitHub rejects
		// random-port loopback redirects (RecipeKeyPromptModal.vue's own
		// isHarnessDeviceFlow comment). The device-code flow lives at
		// BeginDeviceAuth/PollDeviceAuth (device_auth.go); this recipe is
		// simply not reachable through SignInRecipe.
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q uses the device-code flow (primary_auth=%q) — call BeginDeviceAuth, not SignInRecipe", id, recipes.PrimaryAuthDeviceCode)
	case recipes.PrimaryAuthKeys, recipes.PrimaryAuthNone:
		// Named disposition: these arms lead with env keys or need no
		// credential at all — even a recipe that happens to carry an
		// Auth.Kind == mcp_oauth block (none do today) has no business
		// being routed through the browser grant while declaring one of
		// these arms as primary.
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q declares primary_auth=%q, which is not an OAuth sign-in arm", id, recipe.PrimaryAuth)
	default:
		// Legacy/unset primary_auth (""): the pre-existing bare guard.
		if clientID == "" {
			return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q has no OAuth client_id configured — register an OAuth app and set auth.client_id", id)
		}
	}

	// oauth.SignInWithDCR is a drop-in replacement for oauth.SignIn: when
	// clientID is non-empty it behaves identically (resolve.go doc), so it
	// is safe to route both the DCR arm's empty-client-id recipes and every
	// other arm's baked/substituted client id through the same call.
	cred, err := oauth.SignInWithDCR(ctx, oauth.SignInWithDCRConfig{
		ServerURL:   recipe.URL,
		ClientID:    clientID,
		Scopes:      recipe.Auth.Scopes,
		OpenBrowser: a.openBrowser(),
		DCRStore:    a.newDCRStore(ctx), // UNIT-3 3e — nil only when DataDir is unset (see newDCRStore).
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
