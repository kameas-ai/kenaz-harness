package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

// fakeRemoteWithDCR extends fakeRemote to also serve a registration endpoint
// on the authorization server, allowing end-to-end DCR testing.
func fakeRemoteWithDCR(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	srv := httptest.NewServer(mux)
	base = srv.URL

	// MCP endpoint: 401 with a resource_metadata pointer.
	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer error="invalid_request", resource_metadata="`+base+`/.well-known/oauth-protected-resource/mcp/"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	// Protected-resource metadata.
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + base + `/mcp","authorization_servers":["` + base + `/as"],"scopes_supported":["repo","read:org"]}`))
	})
	// AS metadata — advertises registration_endpoint.
	mux.HandleFunc("/as/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{` +
			`"issuer":"` + base + `/as",` +
			`"authorization_endpoint":"` + base + `/as/authorize",` +
			`"token_endpoint":"` + base + `/as/token",` +
			`"registration_endpoint":"` + base + `/as/register",` +
			`"code_challenge_methods_supported":["S256"]` +
			`}`))
	})
	// Registration endpoint — returns a fresh client_id.
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"dcr-issued-cid","client_id_issued_at":1700000000}`))
	})
	// Token endpoint.
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-dcr","refresh_token":"rt-dcr","token_type":"bearer","expires_in":3600}`))
	})
	t.Cleanup(srv.Close)
	return srv, base
}

// --- ResolveClientID unit tests ---

func TestResolveClientID_BakedClientID_ShortCircuits(t *testing.T) {
	asm := &AuthServerMetadata{
		Issuer:               "https://as.example.com",
		RegistrationEndpoint: "https://as.example.com/register", // set but must not be called
	}
	result, err := ResolveClientID(context.Background(), ResolveClientIDConfig{
		BakedClientID: "pre-registered-cid",
		AuthServer:    asm,
	})
	if err != nil {
		t.Fatalf("ResolveClientID with baked id: %v", err)
	}
	if result.ClientID != "pre-registered-cid" {
		t.Errorf("client_id = %q", result.ClientID)
	}
	if result.FromDCR {
		t.Error("FromDCR should be false for baked client_id")
	}
}

func TestResolveClientID_NoBakedID_NoDCR_ReturnsError(t *testing.T) {
	asm := &AuthServerMetadata{
		Issuer: "https://as.example.com",
		// No RegistrationEndpoint
	}
	_, err := ResolveClientID(context.Background(), ResolveClientIDConfig{
		AuthServer: asm,
	})
	if !errors.Is(err, ErrNoClientID) {
		t.Errorf("want ErrNoClientID, got %v", err)
	}
}

func TestResolveClientID_DCR_FreshRegistration(t *testing.T) {
	registerCalled := 0
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalled++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"fresh-cid"}`))
	}))
	defer regSrv.Close()

	asm := &AuthServerMetadata{
		Issuer:               "https://as.example.com",
		RegistrationEndpoint: regSrv.URL,
	}

	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr.json"), nil, nil)

	result, err := ResolveClientID(context.Background(), ResolveClientIDConfig{
		AuthServer: asm,
		Resource:   "https://api.example.com/mcp",
		Store:      store,
		HTTPClient: regSrv.Client(),
	})
	if err != nil {
		t.Fatalf("ResolveClientID DCR: %v", err)
	}
	if result.ClientID != "fresh-cid" {
		t.Errorf("client_id = %q", result.ClientID)
	}
	if !result.FromDCR {
		t.Error("FromDCR should be true")
	}
	if registerCalled != 1 {
		t.Errorf("register endpoint called %d times, want 1", registerCalled)
	}
}

func TestResolveClientID_DCR_ReusesCachedRegistration(t *testing.T) {
	registerCalled := 0
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalled++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"new-cid"}`))
	}))
	defer regSrv.Close()

	asm := &AuthServerMetadata{
		Issuer:               "https://as.example.com",
		RegistrationEndpoint: regSrv.URL,
	}

	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr.json"), nil, nil)
	// Pre-populate the store with an existing registration.
	key := DCRKey{Issuer: "https://as.example.com", Resource: "https://api.example.com/mcp"}
	_ = store.Save(key, &RegisteredClient{ClientID: "cached-cid"})

	result, err := ResolveClientID(context.Background(), ResolveClientIDConfig{
		AuthServer: asm,
		Resource:   "https://api.example.com/mcp",
		Store:      store,
		HTTPClient: regSrv.Client(),
	})
	if err != nil {
		t.Fatalf("ResolveClientID: %v", err)
	}
	if result.ClientID != "cached-cid" {
		t.Errorf("want cached client_id, got %q", result.ClientID)
	}
	if registerCalled != 0 {
		t.Errorf("register endpoint should not be called when cache hit, got %d calls", registerCalled)
	}
}

func TestResolveClientID_DCR_ReregistersOnExpiry(t *testing.T) {
	registerCalled := 0
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalled++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"reregistered-cid"}`))
	}))
	defer regSrv.Close()

	asm := &AuthServerMetadata{
		Issuer:               "https://as.example.com",
		RegistrationEndpoint: regSrv.URL,
	}

	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr.json"), nil, nil)
	store.nowFn = func() time.Time { return timeUnix(5000) }

	// Pre-populate with an expired entry.
	key := DCRKey{Issuer: "https://as.example.com", Resource: "https://api.example.com/mcp"}
	_ = store.Save(key, &RegisteredClient{
		ClientID:              "expired-cid",
		ClientSecret:          "old-secret",
		ClientSecretExpiresAt: 4999, // < 5000 (now) → expired
	})

	result, err := ResolveClientID(context.Background(), ResolveClientIDConfig{
		AuthServer: asm,
		Resource:   "https://api.example.com/mcp",
		Store:      store,
		HTTPClient: regSrv.Client(),
	})
	if err != nil {
		t.Fatalf("ResolveClientID after expiry: %v", err)
	}
	if result.ClientID != "reregistered-cid" {
		t.Errorf("want reregistered client_id, got %q", result.ClientID)
	}
	if registerCalled != 1 {
		t.Errorf("register should be called once on expiry, got %d", registerCalled)
	}
}

// --- SignInWithDCR integration tests ---

func TestSignInWithDCR_BakedClientID_SkipsDCR(t *testing.T) {
	// Use the standard fakeRemote (no registration endpoint).
	srv, base := fakeRemote(t)

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
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

	cred, err := SignInWithDCR(context.Background(), SignInWithDCRConfig{
		ServerURL:   base + "/mcp/",
		ClientID:    "baked-cid", // baked → no DCR
		OpenBrowser: openBrowser,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("SignInWithDCR (baked): %v", err)
	}
	if cred.ClientID != "baked-cid" {
		t.Errorf("client_id = %q", cred.ClientID)
	}
	if cred.AccessToken != "at-remote" {
		t.Errorf("access_token = %q", cred.AccessToken)
	}
}

func TestSignInWithDCR_DCR_HappyPath(t *testing.T) {
	// fakeRemoteWithDCR advertises registration_endpoint.
	srv, base := fakeRemoteWithDCR(t)

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
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

	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr.json"), nil, nil)

	cred, err := SignInWithDCR(context.Background(), SignInWithDCRConfig{
		ServerURL:   base + "/mcp/",
		ClientID:    "", // no baked id → DCR
		DCRStore:    store,
		OpenBrowser: openBrowser,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("SignInWithDCR (DCR): %v", err)
	}
	// Should have received the DCR-issued client_id.
	if cred.ClientID != "dcr-issued-cid" {
		t.Errorf("client_id = %q (want dcr-issued-cid)", cred.ClientID)
	}
	if cred.AccessToken != "at-dcr" {
		t.Errorf("access_token = %q (want at-dcr)", cred.AccessToken)
	}
}

func TestSignInWithDCR_NoDCRAndNoClientID_ReturnsError(t *testing.T) {
	// fakeRemote does NOT advertise registration_endpoint.
	srv, base := fakeRemote(t)

	_, err := SignInWithDCR(context.Background(), SignInWithDCRConfig{
		ServerURL:   base + "/mcp/",
		ClientID:    "", // no baked id
		OpenBrowser: func(string) error { return nil },
		HTTPClient:  srv.Client(),
	})
	if err == nil {
		t.Fatal("want error when no client_id and no DCR endpoint")
	}
	if !errors.Is(err, ErrNoClientID) {
		t.Errorf("want ErrNoClientID, got %v", err)
	}
}

func TestSignInWithDCR_DCR_StoreReusesPreviousRegistration(t *testing.T) {
	srv, base := fakeRemoteWithDCR(t)
	registerCount := 0

	// Wrap the test server to count /as/register calls.
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/as/register" {
			registerCount++
		}
		inner.ServeHTTP(w, r)
	})

	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr.json"), nil, nil)

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
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

	cfg := SignInWithDCRConfig{
		ServerURL:   base + "/mcp/",
		ClientID:    "",
		DCRStore:    store,
		OpenBrowser: openBrowser,
		HTTPClient:  srv.Client(),
	}

	// First sign-in: registers.
	if _, err := SignInWithDCR(context.Background(), cfg); err != nil {
		t.Fatalf("first SignInWithDCR: %v", err)
	}
	if registerCount != 1 {
		t.Errorf("first sign-in: want 1 registration, got %d", registerCount)
	}

	// Second sign-in with the same store: must reuse, no new registration.
	if _, err := SignInWithDCR(context.Background(), cfg); err != nil {
		t.Fatalf("second SignInWithDCR: %v", err)
	}
	if registerCount != 1 {
		t.Errorf("second sign-in: want still 1 registration (cached), got %d", registerCount)
	}
}
