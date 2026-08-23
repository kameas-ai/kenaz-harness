package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/oauth"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func oauthRecipe(clientID string) recipes.Recipe {
	return recipes.Recipe{
		ID:        "remote-oauth",
		Transport: recipes.TransportHTTP,
		URL:       "https://api.example.com/mcp/",
		Auth: &recipes.RecipeAuth{
			Kind:     recipes.AuthKindMCPOAuth,
			ClientID: clientID,
			Scopes:   []string{"repo"},
		},
	}
}

func TestSignInRecipe_NotOAuthRecipe(t *testing.T) {
	t.Parallel()
	api := New(Config{Catalog: testCatalog("plain")})
	_, err := api.SignInRecipe(context.Background(), "plain")
	if err == nil || !strings.Contains(err.Error(), "not an OAuth recipe") {
		t.Fatalf("want not-an-oauth-recipe error, got %v", err)
	}
}

func TestSignInRecipe_NoClientID(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{oauthRecipe("")}}
	api := New(Config{Catalog: cat})
	_, err := api.SignInRecipe(context.Background(), "remote-oauth")
	if err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("want client_id error, got %v", err)
	}
}

// ── UNIT-3 3f: the arm switch (spec.md §1.8/§1.9, kitty-specs/connector-
// lifecycle-truth-01PMZ303) ─────────────────────────────────────────────

// bareRejectMsg is the pre-UNIT-3 message SignInRecipe returned for EVERY
// arm alike whenever the resolved client id was empty. AC-001 asserts no
// mcp_oauth recipe in the shipped catalogs still hits this exact message
// without DCR or substitution ever having been attempted.
const bareRejectMsg = "has no OAuth client_id configured"

// AC-001 (spec.md §7): a table test derived from the real catalogs — for
// every mcp_oauth recipe in recipes.Registry() and recipes.Shipped(), the
// arm switch must route SignInRecipe into a real attempt (DCR discovery, or
// env substitution) instead of the old arm-blind bare reject. A hardcoded
// id list would pass falsely on a newly added recipe; deriving the set from
// the catalogs is the point of this test.
//
// Network-free by construction: every recipe's URL is swapped for a local
// httptest server before the call — this makes zero requests to any
// third-party provider, ever. The server answers 200 (not 401), so
// oauth.SignInWithDCR's Discover step fails fast with ErrNoChallenge; that
// failure — distinct from bareRejectMsg — is the proof an attempt was made.
//
// Mutation (tasks.md UNIT-3 3f AC-001): revert the switch to the single
// pre-UNIT-3 "clientID == ''" reject. Must fail, naming ≥30 recipes (the
// browser_oauth_dcr arm alone is 30).
func TestSignInRecipe_ArmSwitch_NeverBareRejectsWithoutAttempting(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // not 401 → Discover fails fast, no real grant attempted
	}))
	defer srv.Close()

	var subjects []recipes.Recipe
	seen := map[string]bool{}
	for _, cat := range []*recipes.Catalog{recipes.Registry(), recipes.Shipped()} {
		for _, r := range cat.List() {
			if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
				continue
			}
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			subjects = append(subjects, r)
		}
	}
	if len(subjects) < 30 {
		t.Fatalf("expected at least 30 mcp_oauth recipes across Registry()+Shipped() (spec.md §11 R-1), found %d — catalog census drifted", len(subjects))
	}

	var bareRejected []string
	for _, r := range subjects {
		r.URL = srv.URL // never dial the recipe's real provider from a test
		cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}
		api := New(Config{Catalog: cat, Secrets: secrets.NewMemoryBackend()})
		_, err := api.SignInRecipe(context.Background(), r.ID)
		if err != nil && strings.Contains(err.Error(), bareRejectMsg) {
			bareRejected = append(bareRejected, r.ID)
		}
	}
	if len(bareRejected) > 0 {
		t.Errorf("%d of %d mcp_oauth recipes still hit the bare client_id reject without attempting DCR or substitution: %v",
			len(bareRejected), len(subjects), bareRejected)
	}
}

// AC-002 (Go) (spec.md §7): enumerate all six Recipe.PrimaryAuth constants
// plus the oauth-with-no-Auth-block third state (spec.md §1.9) and assert
// each gets a distinct, named disposition rather than falling through to a
// shared generic message.
func TestSignInRecipe_ArmSwitch_EveryArmNamed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // never a real grant — see TestSignInRecipe_ArmSwitch_NeverBareRejectsWithoutAttempting
	}))
	defer srv.Close()

	mk := func(id, primaryAuth string, auth *recipes.RecipeAuth) recipes.Recipe {
		return recipes.Recipe{
			ID:          id,
			Transport:   recipes.TransportHTTP,
			URL:         srv.URL,
			PrimaryAuth: primaryAuth,
			Auth:        auth,
		}
	}
	oauthBlock := func(clientID string) *recipes.RecipeAuth {
		return &recipes.RecipeAuth{Kind: recipes.AuthKindMCPOAuth, ClientID: clientID}
	}

	cases := []struct {
		name        string
		recipe      recipes.Recipe
		wantErrHas  string
		wantAttempt bool // true when the error must NOT be a fail-closed message (network was attempted)
	}{
		{
			name:        "browser_oauth_dcr wires even with empty client id",
			recipe:      mk("dcr-arm", recipes.PrimaryAuthBrowserOAuthDCR, oauthBlock("")),
			wantAttempt: true,
		},
		{
			name:        "browser_oauth_pkce wires when client id resolves",
			recipe:      mk("pkce-arm-ready", recipes.PrimaryAuthBrowserOAuthPKCE, oauthBlock("baked-cid")),
			wantAttempt: true,
		},
		{
			name:       "browser_oauth_pkce fails closed with a named blocker when client id is empty",
			recipe:     mk("pkce-arm-blocked", recipes.PrimaryAuthBrowserOAuthPKCE, oauthBlock("")),
			wantErrHas: "does not support dynamic client registration",
		},
		{
			name:       "oauth arm fails closed with an auth block",
			recipe:     mk("oauth-arm-with-auth", recipes.PrimaryAuthOAuth, oauthBlock("")),
			wantErrHas: "no working sign-in path yet",
		},
		{
			name:       "oauth arm fails closed with NO auth block (spec.md §1.9 third state)",
			recipe:     mk("oauth-arm-no-auth", recipes.PrimaryAuthOAuth, nil),
			wantErrHas: "no working sign-in path yet",
		},
		{
			name:       "device_code is not reachable through SignInRecipe",
			recipe:     mk("device-code-arm", recipes.PrimaryAuthDeviceCode, oauthBlock("baked-cid")),
			wantErrHas: "call BeginDeviceAuth",
		},
		{
			name:       "keys arm is not an OAuth sign-in arm",
			recipe:     mk("keys-arm", recipes.PrimaryAuthKeys, oauthBlock("baked-cid")),
			wantErrHas: "not an OAuth sign-in arm",
		},
		{
			name:       "none arm is not an OAuth sign-in arm",
			recipe:     mk("none-arm", recipes.PrimaryAuthNone, oauthBlock("baked-cid")),
			wantErrHas: "not an OAuth sign-in arm",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{tc.recipe}}
			api := New(Config{Catalog: cat, Secrets: secrets.NewMemoryBackend()})
			_, err := api.SignInRecipe(context.Background(), tc.recipe.ID)
			if tc.wantAttempt {
				if err == nil {
					t.Fatal("want an error (the loopback test server never completes a real grant), got nil")
				}
				if strings.Contains(err.Error(), bareRejectMsg) {
					t.Errorf("wired arm still hit the bare reject: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrHas) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErrHas)
			}
		})
	}
}

// Regression (tasks.md UNIT-3 3f/AC-001): a recipe with a baked literal
// client_id and legacy (unset) primary_auth still takes the pre-UNIT-3
// "default" path through SignInRecipe — the arm switch must not turn a
// working baked-id recipe into a fail-closed one.
func TestSignInRecipe_ArmSwitch_BakedLiteralClientIDLegacyArmStillAttempts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // not 401 → fails fast at Discover, no real grant
	}))
	defer srv.Close()

	recipe := oauthRecipe("Iv23li6LDja9hM0dAJGV") // GitHub's real device-flow id; PrimaryAuth unset (legacy)
	recipe.URL = srv.URL
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{recipe}}
	api := New(Config{Catalog: cat, Secrets: secrets.NewMemoryBackend()})

	_, err := api.SignInRecipe(context.Background(), "remote-oauth")
	if err == nil {
		t.Fatal("want an error (the loopback test server never completes a real grant), got nil")
	}
	if strings.Contains(err.Error(), bareRejectMsg) || strings.Contains(err.Error(), "not an OAuth sign-in arm") {
		t.Errorf("legacy arm with a baked client id was fail-closed: %v", err)
	}
}

// ── UNIT-1: ${VAR} reaches Auth.ClientID / Auth.ClientSecret (spec.md FR-003) ──

// byoOAuthRecipe returns a recipe whose Auth.ClientID (and, if
// clientSecretKey != "", Auth.ClientSecret) are literal "${VAR}" tokens
// backed by declared EnvKeys — the shape UNIT-1 exists to substitute.
// requiredID/requiredSecret exercise D-1's asymmetry: ResolveEnv errors on
// an unresolvable *required* key but silently skips an unresolvable
// *optional* one, leaving its token literal.
func byoOAuthRecipe(id, url, clientIDKey, clientSecretKey string, requiredID, requiredSecret bool) recipes.Recipe {
	r := recipes.Recipe{
		ID:        id,
		Transport: recipes.TransportHTTP,
		URL:       url,
		Auth: &recipes.RecipeAuth{
			Kind:     recipes.AuthKindMCPOAuth,
			ClientID: "${" + clientIDKey + "}",
			Scopes:   []string{"repo"},
		},
		EnvKeys: []recipes.EnvKey{
			{Name: clientIDKey, Display: "id", Required: requiredID},
		},
	}
	if clientSecretKey != "" {
		r.Auth.ClientSecret = "${" + clientSecretKey + "}"
		r.EnvKeys = append(r.EnvKeys, recipes.EnvKey{Name: clientSecretKey, Display: "secret", Required: requiredSecret})
	}
	return r
}

// entryKey builds the "<kind>|<locator>" key MemoryBackend.SetEntries wants,
// for the recipe/env-key locator ResolveEnv derives (keychain.go:57).
func entryKey(recipeID, envName string) string {
	return secrets.RefKeychain.String() + "|" + recipes.KeychainLocator(recipeID, envName)
}

// AC-003a: a resolvable ${VAR} client_id substitutes to the resolved value.
func TestResolveOAuthClientCredentials_SubstitutesClientID(t *testing.T) {
	t.Parallel()
	recipe := byoOAuthRecipe("byo", "https://api.example.com/mcp/", "KAMEAS_X_OAUTH_CLIENT_ID", "", true, false)
	backend := secrets.NewMemoryBackend()
	backend.SetEntries(map[string][]byte{
		entryKey("byo", "KAMEAS_X_OAUTH_CLIENT_ID"): []byte("abc123"),
	})
	api := New(Config{Secrets: backend})

	clientID, clientSecret, err := api.resolveOAuthClientCredentials(context.Background(), recipe)
	if err != nil {
		t.Fatalf("resolveOAuthClientCredentials: %v", err)
	}
	if clientID != "abc123" {
		t.Errorf("clientID = %q, want abc123", clientID)
	}
	if clientSecret != "" {
		t.Errorf("clientSecret = %q, want empty (recipe declares none)", clientSecret)
	}
}

// AC-003b: an unresolvable REQUIRED client_id key produces an error naming
// the env key, and SignInRecipe makes zero HTTP requests to the recipe's
// server — asserting on the error string alone is how this defect survived
// the last sweep (spec.md §7 AC-003).
func TestSignInRecipe_UnresolvedClientIDToken_ZeroRequests(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	recipe := byoOAuthRecipe("byo", server.URL, "KAMEAS_X_OAUTH_CLIENT_ID", "", true, false)
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{recipe}}
	// Empty backend: the required key is declared but never populated.
	api := New(Config{Catalog: cat, Secrets: secrets.NewMemoryBackend()})

	_, err := api.SignInRecipe(context.Background(), "byo")
	if err == nil || !strings.Contains(err.Error(), "KAMEAS_X_OAUTH_CLIENT_ID") {
		t.Fatalf("want error naming KAMEAS_X_OAUTH_CLIENT_ID, got %v", err)
	}
	if requests != 0 {
		t.Errorf("server saw %d requests, want 0 — sign-in must not round-trip on an unresolved client_id", requests)
	}
}

// AC-003c (§1.11 N-2): front's real shape — client_id AND client_secret both
// substitute, and the resolved secret appears in no error string returned
// to the caller (D-2 — no credential value in a test assertion message
// either, per spec.md §8 R-8, so this only checks *absence*, never logs the
// secret itself).
func TestResolveOAuthClientCredentials_SubstitutesClientIDAndSecret(t *testing.T) {
	t.Parallel()
	const secretValue = "s3cr3t-do-not-log" //nolint:gosec // test fixture, not a real credential
	recipe := byoOAuthRecipe("front-like", "https://api.example.com/mcp/",
		"KAMEAS_FRONT_OAUTH_CLIENT_ID", "KAMEAS_FRONT_OAUTH_CLIENT_SECRET", true, true)
	backend := secrets.NewMemoryBackend()
	backend.SetEntries(map[string][]byte{
		entryKey("front-like", "KAMEAS_FRONT_OAUTH_CLIENT_ID"):     []byte("front-cid"),
		entryKey("front-like", "KAMEAS_FRONT_OAUTH_CLIENT_SECRET"): []byte(secretValue),
	})
	api := New(Config{Secrets: backend})

	clientID, clientSecret, err := api.resolveOAuthClientCredentials(context.Background(), recipe)
	if err != nil {
		t.Fatalf("resolveOAuthClientCredentials: %v", err)
	}
	if clientID != "front-cid" {
		t.Errorf("clientID = %q, want front-cid", clientID)
	}
	if clientSecret != secretValue {
		t.Error("clientSecret did not substitute to the resolved value")
	}

	// D-2 / §8 R-8: the secret must never appear in an error string. Force
	// an error on the *secret* leg (client_id resolves fine, client_secret
	// does not) and confirm the error names only the env key, never a
	// resolved value — driven entirely against the in-process helper, no
	// network I/O.
	failRecipe := byoOAuthRecipe("front-like-2", "https://api.example.com/mcp/",
		"KAMEAS_FRONT_OAUTH_CLIENT_ID", "KAMEAS_FRONT_OAUTH_CLIENT_SECRET", true, true)
	failBackend := secrets.NewMemoryBackend()
	failBackend.SetEntries(map[string][]byte{
		entryKey("front-like-2", "KAMEAS_FRONT_OAUTH_CLIENT_ID"): []byte("front-cid"),
		// Secret key deliberately left unset.
	})
	api2 := New(Config{Secrets: failBackend})
	_, _, failErr := api2.resolveOAuthClientCredentials(context.Background(), failRecipe)
	if failErr == nil {
		t.Fatal("want an error for the unresolved required secret key")
	}
	if !strings.Contains(failErr.Error(), "KAMEAS_FRONT_OAUTH_CLIENT_SECRET") {
		t.Errorf("error does not name the unresolved secret env key: %v", failErr)
	}
	if strings.Contains(failErr.Error(), secretValue) {
		t.Fatal("resolved client secret leaked into an error string")
	}
}

// AC-003d (D-1): the client_id key is marked OPTIONAL and left unresolved.
// ResolveEnv succeeds (an optional miss is not an error) and returns no
// entry for it, so the token survives into SubstituteString untouched. The
// guard must still fire — it must not depend on "required" being true,
// because a future recipe may declare an optional BYO key.
func TestResolveOAuthClientCredentials_OptionalUnresolvedTokenStillGuarded(t *testing.T) {
	t.Parallel()
	recipe := byoOAuthRecipe("byo-optional", "https://api.example.com/mcp/", "KAMEAS_OPTIONAL_CLIENT_ID", "", false, false)
	// Empty backend — the optional key resolves to nothing, silently.
	api := New(Config{Secrets: secrets.NewMemoryBackend()})

	_, _, err := api.resolveOAuthClientCredentials(context.Background(), recipe)
	if err == nil || !strings.Contains(err.Error(), "KAMEAS_OPTIONAL_CLIENT_ID") {
		t.Fatalf("want error naming KAMEAS_OPTIONAL_CLIENT_ID even though the key is optional, got %v", err)
	}
}

// A recipe with a literal (non-token) client_id — e.g. GitHub's real baked
// device-flow id — is unaffected by the substitution seam and never touches
// the secrets backend.
func TestResolveOAuthClientCredentials_LiteralClientIDPassesThrough(t *testing.T) {
	t.Parallel()
	recipe := oauthRecipe("Iv23li6LDja9hM0dAJGV")
	// Secrets is nil: if the literal path touched ResolveEnv this would
	// panic/error on the nil backend, which is itself part of the proof.
	api := New(Config{})

	clientID, clientSecret, err := api.resolveOAuthClientCredentials(context.Background(), recipe)
	if err != nil {
		t.Fatalf("resolveOAuthClientCredentials: %v", err)
	}
	if clientID != "Iv23li6LDja9hM0dAJGV" {
		t.Errorf("clientID = %q, want the literal value unchanged", clientID)
	}
	if clientSecret != "" {
		t.Errorf("clientSecret = %q, want empty", clientSecret)
	}
}

// Catalog-derived guard (spec.md §8 R-3): every registry recipe whose
// Auth.ClientID or Auth.ClientSecret carries a "${" token has a matching
// declared EnvKey — the contract registry_test.go partially pins per-recipe
// (":659"/":672" etc.); deriving it here means a newly added recipe #15 is
// covered automatically instead of requiring a hand-maintained list.
func TestRegistry_OAuthTokensHaveMatchingEnvKeys(t *testing.T) {
	t.Parallel()
	for _, r := range recipes.Registry().List() {
		if r.Auth == nil {
			continue
		}
		for _, field := range []struct {
			name  string
			value string
		}{
			{"client_id", r.Auth.ClientID},
			{"client_secret", r.Auth.ClientSecret},
		} {
			key, ok := unresolvedToken(field.value)
			if !ok {
				continue
			}
			found := false
			for _, ek := range r.EnvKeys {
				if ek.Name == key {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipe %q: auth.%s references %q but no matching env_keys entry declares it",
					r.ID, field.name, key)
			}
		}
	}
}

func TestInjectOAuthBearer_NonOAuthIsNoop(t *testing.T) {
	t.Parallel()
	api := New(Config{Secrets: secrets.NewMemoryBackend()})
	spec := &coremcp.ServerSpec{Name: "x"}
	if err := api.injectOAuthBearer(context.Background(), testRecipe("x"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("non-OAuth recipe should not get an Authorization header")
	}
}

func TestInjectOAuthBearer_DeferredWhenNotSignedIn(t *testing.T) {
	t.Parallel()
	api := New(Config{Secrets: secrets.NewMemoryBackend()})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	// OAuth recipe but no stored credential → deferred auth, no header, no error.
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer deferred: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("deferred-auth recipe should not get an Authorization header")
	}
}

func TestInjectOAuthBearer_InjectsStoredToken(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	cred := &oauth.StoredCredential{
		AccessToken: "stored-at",
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(time.Hour), // valid, no refresh needed
	}
	blob, err := cred.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	locator := recipes.OAuthCredentialLocator("remote-oauth")
	backend.SetEntries(map[string][]byte{
		secrets.RefKeychain.String() + "|" + locator: blob,
	})

	api := New(Config{Secrets: backend})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer stored-at" {
		t.Errorf("Authorization = %q, want Bearer stored-at", got)
	}
}

// fakeTokenSource satisfies ConnectorTokenSource for the served-mode
// broker fallback tests (spec 091 D8).
type fakeTokenSource struct {
	token string
	err   error
	calls int
}

func (f *fakeTokenSource) ConnectorToken(context.Context, string) (string, error) {
	f.calls++
	return f.token, f.err
}

func TestInjectOAuthBearer_BrokerFallbackWhenNoLocalCredential(t *testing.T) {
	t.Parallel()
	src := &fakeTokenSource{token: "broker-at"}
	api := New(Config{Secrets: secrets.NewMemoryBackend(), ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer broker-at" {
		t.Errorf("Authorization = %q, want broker bearer", got)
	}
	if src.calls != 1 {
		t.Errorf("broker calls = %d, want 1", src.calls)
	}
}

func TestInjectOAuthBearer_BrokerFallbackFailureIsDeferred(t *testing.T) {
	t.Parallel()
	src := &fakeTokenSource{err: context.DeadlineExceeded}
	api := New(Config{Secrets: secrets.NewMemoryBackend(), ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	// A broker failure is deferred auth — no header, no error, no spawn
	// failure.
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("failed broker fallback must leave the spec unauthenticated")
	}
}

func TestInjectOAuthBearer_LocalCredentialWinsOverBroker(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	cred := &oauth.StoredCredential{
		AccessToken: "stored-at",
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	blob, err := cred.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	backend.SetEntries(map[string][]byte{
		secrets.RefKeychain.String() + "|" + recipes.OAuthCredentialLocator("remote-oauth"): blob,
	})
	src := &fakeTokenSource{token: "broker-at"}
	api := New(Config{Secrets: backend, ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer stored-at" {
		t.Errorf("Authorization = %q, want stored token", got)
	}
	if src.calls != 0 {
		t.Errorf("broker consulted despite a valid local credential (%d calls)", src.calls)
	}
}
