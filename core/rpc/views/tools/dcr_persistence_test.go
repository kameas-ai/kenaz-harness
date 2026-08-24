package tools

// UNIT-3 3e (kitty-specs/connector-lifecycle-truth-01PMZ303): before this
// wiring, SignInRecipe passed DCRStore: nil to oauth.SignInWithDCR, so every
// sign-in against a browser_oauth_dcr recipe re-registered a brand new OAuth
// client with the provider — no registration ever survived a relaunch, and
// two sign-in attempts in the same process each hit the registration
// endpoint. These tests drive SignInRecipe (and the newDCRStore helper it
// now calls) against a fake, network-free RFC 7591 provider and count
// registration-endpoint POSTs directly, so a regression back to
// "DCRStore: nil" is caught by a request count, not by an error string.
//
// NOTE ON SCOPE: this does not verify DCR works against a REAL provider —
// UNIT-0's *1 was never executed and this environment has no live network
// access to third-party providers. These tests are persistence tests for an
// OAuth arm nobody has watched succeed against a real authorization server.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/oauth"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// dcrFakeProvider is a network-free RFC 7591-capable MCP remote: 401 with a
// resource_metadata pointer, protected-resource + authorization-server
// metadata advertising a registration_endpoint, a registration endpoint
// that counts POSTs and issues a fresh client_id each time it is actually
// called, and a token endpoint that completes the authorization-code grant.
type dcrFakeProvider struct {
	srv *httptest.Server
	base string

	mu            sync.Mutex
	registerCalls int
}

func newDCRFakeProvider(t *testing.T) *dcrFakeProvider {
	t.Helper()
	p := &dcrFakeProvider{}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	p.srv = srv
	p.base = srv.URL

	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer error="invalid_request", resource_metadata="`+p.base+`/.well-known/oauth-protected-resource/mcp/"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + p.base + `/mcp","authorization_servers":["` + p.base + `/as"],"scopes_supported":["repo"]}`))
	})
	mux.HandleFunc("/as/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{` +
			`"issuer":"` + p.base + `/as",` +
			`"authorization_endpoint":"` + p.base + `/as/authorize",` +
			`"token_endpoint":"` + p.base + `/as/token",` +
			`"registration_endpoint":"` + p.base + `/as/register",` +
			`"code_challenge_methods_supported":["S256"]` +
			`}`))
	})
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.registerCalls++
		n := p.registerCalls
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"dcr-issued-cid-` + itoa(n) + `"}`))
	})
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-dcr","refresh_token":"rt-dcr","token_type":"bearer","expires_in":3600}`))
	})
	t.Cleanup(srv.Close)
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (p *dcrFakeProvider) registerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.registerCalls
}

// dcrOpenBrowser completes the loopback authorization-code grant
// automatically, mirroring core/mcp/oauth's own test helpers
// (resolve_test.go) — no real browser, no real user.
func dcrOpenBrowser(authURL string) error {
	u, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := u.Query()
	go func() {
		cb := q.Get("redirect_uri") + "?code=abc&state=" + url.QueryEscape(q.Get("state"))
		resp, e := http.Get(cb) //nolint:noctx // test
		if e == nil {
			_ = resp.Body.Close()
		}
	}()
	return nil
}

// dcrRecipe returns a browser_oauth_dcr recipe pointed at the given fake
// provider base URL, with no baked client_id (so DCR is attempted).
func dcrRecipe(id, providerBase string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		Transport:   recipes.TransportHTTP,
		URL:         providerBase + "/mcp/",
		PrimaryAuth: recipes.PrimaryAuthBrowserOAuthDCR,
		Auth: &recipes.RecipeAuth{
			Kind:     recipes.AuthKindMCPOAuth,
			ClientID: "",
		},
	}
}

// dcrTestConfig builds a Config wired the way core/rpc/api.go wires
// production: DataDir, Keychain and Secrets all present and backed by the
// same in-memory store, plus a no-op pool/enabled-list so SignInRecipe's
// tail call into InstallRecipe succeeds without spawning anything real.
func dcrTestConfig(t *testing.T, cat *recipes.Catalog, backend *secrets.MemoryBackend) Config {
	t.Helper()
	return Config{
		Catalog:  cat,
		Enabled:  &recipes.EnabledRecipes{},
		Pool:     newFakePool(),
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	}
}

// TestSignInRecipe_DCR_ReusesRegistrationAcrossCalls is the load-bearing
// test for UNIT-3 3e: two consecutive SignInRecipe calls against the same
// long-lived API/Config must register exactly once with the provider.
//
// Mutation (must fail): revert oauth.go's
// `DCRStore: a.newDCRStore(ctx)` back to `DCRStore: nil`. Every call then
// re-registers — registerCount ends at 2, not 1 — because
// oauth.ResolveClientID treats cfg.Store == nil as "always re-register"
// (resolve.go: `if cfg.Store != nil { ... }` guards both the cache Load and
// the Save).
func TestSignInRecipe_DCR_ReusesRegistrationAcrossCalls(t *testing.T) {
	provider := newDCRFakeProvider(t)
	backend := secrets.NewMemoryBackend()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{dcrRecipe("dcr-recipe", provider.base)}}
	api := New(dcrTestConfig(t, cat, backend))

	// Route the interactive grant through our fake loopback completion
	// instead of a real system browser. SignInRecipe always calls
	// oauth.OpenSystemBrowser directly, so drive oauth.SignInWithDCR the
	// same way SignInRecipe does, through the production newDCRStore, to
	// keep this a same-process persistence test without touching the OS.
	signIn := func() error {
		recipe, ok := cat.Get("dcr-recipe")
		if !ok {
			t.Fatal("recipe not found in catalog")
		}
		cred, err := oauth.SignInWithDCR(context.Background(), oauth.SignInWithDCRConfig{
			ServerURL:   recipe.URL,
			ClientID:    "",
			OpenBrowser: dcrOpenBrowser,
			DCRStore:    api.newDCRStore(context.Background()),
		})
		if err != nil {
			return err
		}
		if cred.ClientID == "" {
			t.Fatal("credential has empty client_id")
		}
		return nil
	}

	if err := signIn(); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Fatalf("after first sign-in: register endpoint called %d times, want 1", got)
	}

	if err := signIn(); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Errorf("after second sign-in: register endpoint called %d times, want STILL 1 (cached registration reused)", got)
	}
}

// TestSignInRecipe_DCR_PersistedCredentialSurvivesInstall drives the
// production code SignInRecipe itself runs AFTER oauth.SignInWithDCR
// returns — persistOAuthCredential + InstallRecipe's respawn tail — twice,
// so the assertion covers more of the real call site than
// TestSignInRecipe_DCR_ReusesRegistrationAcrossCalls alone.
//
// NOT a call to api.SignInRecipe() itself: SignInRecipe hardcodes
// oauth.OpenSystemBrowser (loopback.go), which really execs `open`/
// `xdg-open`/`start` on the host — popping a real browser window is not
// something a test may do, and SignInRecipe has no seam to inject a fake
// one. So this reproduces SignInRecipe's DCR branch (newDCRStore +
// oauth.SignInWithDCR + persistOAuthCredential + InstallRecipe) verbatim,
// substituting only OpenBrowser for dcrOpenBrowser — the same substitution
// this file's other tests make, and the one the task brief explicitly
// allows ("Drive ResolveClientID (or the SignInWithDCR path)").
// TestSignInRecipe_DCR_CallSiteReachesRegistration below drives the actual
// api.SignInRecipe() entrypoint on a registration-failure fixture instead,
// to confirm the real line is wired without ever reaching OpenBrowser.
//
// Mutation (must fail): same as above — revert `DCRStore:
// a.newDCRStore(ctx)` to `DCRStore: nil` in oauth.go's SignInRecipe. The
// second call re-registers, so registerCount ends at 2.
func TestSignInRecipe_DCR_PersistedCredentialSurvivesInstall(t *testing.T) {
	provider := newDCRFakeProvider(t)
	backend := secrets.NewMemoryBackend()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{dcrRecipe("dcr-recipe", provider.base)}}
	api := New(dcrTestConfig(t, cat, backend))

	signInOnce := func() error {
		recipe, ok := api.cfg.Catalog.Get("dcr-recipe")
		if !ok {
			t.Fatal("recipe not found")
		}
		cred, err := oauth.SignInWithDCR(context.Background(), oauth.SignInWithDCRConfig{
			ServerURL:   recipe.URL,
			ClientID:    "",
			OpenBrowser: dcrOpenBrowser,
			DCRStore:    api.newDCRStore(context.Background()),
		})
		if err != nil {
			return err
		}
		if err := api.persistOAuthCredential(context.Background(), "dcr-recipe", cred); err != nil {
			return err
		}
		_, err = api.InstallRecipe(context.Background(), "dcr-recipe", nil, nil)
		return err
	}

	if err := signInOnce(); err != nil {
		t.Fatalf("first sign-in+install: %v", err)
	}
	if err := signInOnce(); err != nil {
		t.Fatalf("second sign-in+install: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Errorf("register endpoint called %d times across two full sign-ins, want 1", got)
	}
}

// dcrFailingRegisterProvider is like newDCRFakeProvider but its
// registration endpoint always fails (500), so RegisterClient — and every
// caller up to and including oauth.SignInWithDCR — errors out BEFORE
// AuthorizeInteractive ever calls OpenBrowser. That makes it the one
// fixture safe to drive through the real, unmodified api.SignInRecipe()
// entrypoint (which hardcodes oauth.OpenSystemBrowser): the grant never
// reaches the point where a real browser would open.
func dcrFailingRegisterProvider(t *testing.T) *dcrFakeProvider {
	t.Helper()
	p := newDCRFakeProvider(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.registerCalls++
		p.mu.Unlock()
		http.Error(w, "registration refused", http.StatusInternalServerError)
	})
	// Re-point every other path at the working provider's handler.
	mux.Handle("/", p.srv.Config.Handler)
	p.srv.Config.Handler = mux
	return p
}

// TestSignInRecipe_DCR_CallSiteReachesRegistration calls the real,
// unmodified api.SignInRecipe() entrypoint — not a hand-rolled
// replication of its DCR branch — against a provider whose registration
// endpoint always fails. Because registration fails, SignInWithDCR returns
// before ever calling OpenBrowser (oauth.go's hardcoded
// oauth.OpenSystemBrowser), so this is safe to run unattended while still
// proving oauth.go:'s `DCRStore: a.newDCRStore(ctx)` line is live: the
// registration endpoint is reached at all (proving the DCR arm, not the
// bare "has no OAuth client_id configured" reject, is what runs) and the
// returned error is the real provider error, not a client-id-shaped one.
func TestSignInRecipe_DCR_CallSiteReachesRegistration(t *testing.T) {
	provider := dcrFailingRegisterProvider(t)
	backend := secrets.NewMemoryBackend()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{dcrRecipe("dcr-recipe", provider.base)}}
	api := New(dcrTestConfig(t, cat, backend))

	_, err := api.SignInRecipe(context.Background(), "dcr-recipe")
	if err == nil {
		t.Fatal("want an error (registration always fails on this fixture), got nil")
	}
	if strings.Contains(err.Error(), bareRejectMsg) {
		t.Errorf("SignInRecipe hit the bare client_id reject instead of attempting DCR: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Errorf("register endpoint called %d times, want 1 — the real SignInRecipe call site did not reach RegisterClient through the wired DCRStore path", got)
	}
}

// TestSignInRecipe_DCR_PersistsAcrossNewAPIInstance is the cross-launch
// case: a *new* API value (fresh process in spirit — the harness restarts
// and rebuilds Config from scratch) built over the SAME DataDir must reuse
// the registration the first instance persisted to
// <DataDir>/oauth/dcr_clients.json, not re-register.
func TestSignInRecipe_DCR_PersistsAcrossNewAPIInstance(t *testing.T) {
	provider := newDCRFakeProvider(t)
	backend := secrets.NewMemoryBackend()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{dcrRecipe("dcr-recipe", provider.base)}}
	cfg := dcrTestConfig(t, cat, backend) // one shared DataDir for both instances

	firstAPI := New(cfg)
	recipe, _ := cat.Get("dcr-recipe")
	_, err := oauth.SignInWithDCR(context.Background(), oauth.SignInWithDCRConfig{
		ServerURL:   recipe.URL,
		ClientID:    "",
		OpenBrowser: dcrOpenBrowser,
		DCRStore:    firstAPI.newDCRStore(context.Background()),
	})
	if err != nil {
		t.Fatalf("first instance sign-in: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Fatalf("after first instance: register endpoint called %d times, want 1", got)
	}

	// A brand new API value over the identical Config (same DataDir) —
	// simulates a relaunch rebuilding the chassis from scratch.
	secondAPI := New(cfg)
	_, err = oauth.SignInWithDCR(context.Background(), oauth.SignInWithDCRConfig{
		ServerURL:   recipe.URL,
		ClientID:    "",
		OpenBrowser: dcrOpenBrowser,
		DCRStore:    secondAPI.newDCRStore(context.Background()),
	})
	if err != nil {
		t.Fatalf("second instance sign-in: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Errorf("after a fresh API instance over the same DataDir: register endpoint called %d times, want STILL 1 (persisted across the simulated relaunch)", got)
	}
}

// TestNewDCRStore_NoDataDir_ReturnsNilSafely covers the degraded case named
// in the requirements: DataDir == "" must produce a nil store (today's
// pre-3e "always re-register" behaviour), never a panic and never a store
// anchored at a nonsense relative path.
func TestNewDCRStore_NoDataDir_ReturnsNilSafely(t *testing.T) {
	api := New(Config{Secrets: secrets.NewMemoryBackend()})
	if got := api.newDCRStore(context.Background()); got != nil {
		t.Errorf("newDCRStore with empty DataDir = %v, want nil", got)
	}
}

// TestNewDCRStore_ExpiredSecretTriggersReregistration exercises the
// ErrDCRExpired path through the actual production wiring (real
// Keychain/Secrets closures, real file on disk under a t.TempDir()), not a
// bare oauth.NewDCRStore(path, nil, nil) call — so a mistake in the
// wiring (e.g. the wrong locator, or losing the context the closures
// capture) that only shows up on the second, credstore-touching load is
// still caught here.
func TestNewDCRStore_ExpiredSecretTriggersReregistration(t *testing.T) {
	backend := secrets.NewMemoryBackend()
	api := New(Config{
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})
	store := api.newDCRStore(context.Background())
	if store == nil {
		t.Fatal("newDCRStore returned nil with DataDir/Keychain/Secrets all set")
	}

	key := oauth.DCRKey{Issuer: "https://as.example.com", Resource: "https://api.example.com/mcp"}
	if err := store.Save(key, &oauth.RegisteredClient{
		ClientID:              "expiring-cid",
		ClientSecret:          "expiring-secret",
		ClientSecretExpiresAt: 1, // epoch 1 — long in the past relative to any real clock
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := store.Load(key); !errors.Is(err, oauth.ErrDCRExpired) {
		t.Fatalf("Load after expiry = %v, want ErrDCRExpired", err)
	}

	// The store must have deleted the stale entry so the NEXT call
	// re-registers instead of returning the same expired entry forever.
	if _, err := store.Load(key); !errors.Is(err, oauth.ErrDCRNotFound) {
		t.Fatalf("Load after the expired entry was consumed = %v, want ErrDCRNotFound (clear path to re-register)", err)
	}
}

// TestNewDCRStore_ClientSecretRoundTripsThroughRealKeychainAndSecrets is
// the falsification target for requirement 2 (do not pass nil
// SecretSaver/SecretLoader). It proves the closures newDCRStore builds are
// not decorative: a client_secret Saved through one store instance is
// retrievable through a second store instance built the same way, which
// only works if the secret actually reached the Keychain/Secrets backend
// rather than being silently dropped.
//
// Mutation (must fail): change newDCRStore to build the store with
// `oauth.NewDCRStore(oauth.DefaultDCRStorePath(a.cfg.DataDir), nil, nil)`
// — i.e. keep the store construction (3e's headline fix) but drop the
// credstore closures. This still compiles (SecretSaver/SecretLoader are
// nil-tolerant by their own doc) but the loaded ClientSecret becomes "".
func TestNewDCRStore_ClientSecretRoundTripsThroughRealKeychainAndSecrets(t *testing.T) {
	backend := secrets.NewMemoryBackend()
	api := New(Config{
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})

	key := oauth.DCRKey{Issuer: "https://as.example.com", Resource: "https://api.example.com/mcp"}
	writer := api.newDCRStore(context.Background())
	if err := writer.Save(key, &oauth.RegisteredClient{
		ClientID:     "confidential-cid",
		ClientSecret: "s3cr3t-from-provider",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A second store instance, built the same way over the same
	// DataDir/backend — the cross-instance read this mission cares about.
	reader := api.newDCRStore(context.Background())
	rc, err := reader.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rc.ClientSecret != "s3cr3t-from-provider" {
		t.Errorf("ClientSecret = %q, want %q — the credstore closures did not round-trip the secret", rc.ClientSecret, "s3cr3t-from-provider")
	}
}

// TestSignInRecipe_DCR_RealCallSite_ReusesRegistration closes the coverage
// gap the UNIT-3 3e wiring shipped with: every other test in this file
// reproduces SignInRecipe's DCR branch rather than calling it, because
// SignInRecipe hardcoded oauth.OpenSystemBrowser and a successful grant
// would have popped a real browser window on the host. That left the
// literal `DCRStore: a.newDCRStore(ctx)` assignment caught by NO test —
// revert it to nil and the suite stayed green. That is the same
// vacuous-coverage shape as review finding B4, one layer down.
//
// Config.OpenBrowser now injects the hook, so this drives the REAL
// api.SignInRecipe() twice, through a complete loopback grant, and asserts
// the provider saw exactly one registration.
//
// Mutation (must fail): set `DCRStore: nil` at oauth.go's SignInWithDCR
// call site — registerCount becomes 2.
func TestSignInRecipe_DCR_RealCallSite_ReusesRegistration(t *testing.T) {
	provider := newDCRFakeProvider(t)
	backend := secrets.NewMemoryBackend()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{dcrRecipe("dcr-recipe", provider.base)}}

	cfg := dcrTestConfig(t, cat, backend)
	cfg.OpenBrowser = dcrOpenBrowser // the seam — never set in production
	api := New(cfg)

	if _, err := api.SignInRecipe(context.Background(), "dcr-recipe"); err != nil {
		t.Fatalf("first SignInRecipe: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Fatalf("after first SignInRecipe: register endpoint called %d times, want 1", got)
	}

	if _, err := api.SignInRecipe(context.Background(), "dcr-recipe"); err != nil {
		t.Fatalf("second SignInRecipe: %v", err)
	}
	if got := provider.registerCount(); got != 1 {
		t.Errorf("after second SignInRecipe: register endpoint called %d times, want STILL 1 — "+
			"the cached DCR registration must be reused, or every sign-in click creates "+
			"another OAuth client in the user's account at the provider", got)
	}
}
