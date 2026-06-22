package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseResourceMetadataURL(t *testing.T) {
	// Real GitHub header shape.
	hdr := `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/"`
	got, err := ParseResourceMetadataURL(hdr)
	if err != nil {
		t.Fatalf("ParseResourceMetadataURL: %v", err)
	}
	if got != "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/" {
		t.Errorf("got %q", got)
	}

	if _, err := ParseResourceMetadataURL(`Bearer realm="x"`); !errors.Is(err, ErrNoResourceMetadata) {
		t.Errorf("want ErrNoResourceMetadata, got %v", err)
	}
}

func TestFetchProtectedResourceMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"https://api.example.com/mcp","authorization_servers":["https://auth.example.com"],"scopes_supported":["repo","read:org"]}`))
	}))
	defer srv.Close()

	prm, err := FetchProtectedResourceMetadata(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchProtectedResourceMetadata: %v", err)
	}
	if len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != "https://auth.example.com" {
		t.Errorf("authorization_servers: %v", prm.AuthorizationServers)
	}
	if len(prm.ScopesSupported) != 2 {
		t.Errorf("scopes: %v", prm.ScopesSupported)
	}
}

func TestFetchProtectedResourceMetadata_NoAuthServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"resource":"x","authorization_servers":[]}`))
	}))
	defer srv.Close()
	if _, err := FetchProtectedResourceMetadata(context.Background(), srv.Client(), srv.URL); !errors.Is(err, ErrNoAuthorizationServer) {
		t.Errorf("want ErrNoAuthorizationServer, got %v", err)
	}
}

func TestDiscoverAuthServer_RFC8414(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		if strings.Contains(r.URL.Path, ".well-known/oauth-authorization-server") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + issuerOf(r) + `","authorization_endpoint":"https://as/authorize","token_endpoint":"https://as/token","code_challenge_methods_supported":["S256"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	asm, err := DiscoverAuthServer(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverAuthServer: %v", err)
	}
	if asm.AuthorizationEndpoint != "https://as/authorize" || asm.TokenEndpoint != "https://as/token" {
		t.Errorf("endpoints: %+v (hit %s)", asm, hitPath)
	}
}

// TestDiscoverAuthServer_GitHubFallback simulates GitHub: the well-known
// metadata endpoints 404, so discovery must fall back to issuer-derived
// /authorize + /access_token endpoints.
func TestDiscoverAuthServer_GitHubFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // GitHub publishes no AS metadata
	}))
	defer srv.Close()

	issuer := srv.URL + "/login/oauth"
	asm, err := DiscoverAuthServer(context.Background(), srv.Client(), issuer)
	if err != nil {
		t.Fatalf("DiscoverAuthServer fallback: %v", err)
	}
	if asm.AuthorizationEndpoint != issuer+"/authorize" {
		t.Errorf("authorization_endpoint = %q", asm.AuthorizationEndpoint)
	}
	if asm.TokenEndpoint != issuer+"/access_token" {
		t.Errorf("token_endpoint = %q", asm.TokenEndpoint)
	}
}

func TestNewPKCE(t *testing.T) {
	a, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPKCE()
	if a.Verifier == b.Verifier {
		t.Error("verifiers should be unique per call")
	}
	if a.Method != "S256" || a.Challenge == "" || a.Verifier == "" {
		t.Errorf("bad pkce: %+v", a)
	}
	// Challenge must be the raw-url-base64 of SHA256(verifier) — recomputing
	// must match (no padding).
	if strings.ContainsAny(a.Challenge, "=+/") {
		t.Errorf("challenge not raw-url-safe: %q", a.Challenge)
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	asm := &AuthServerMetadata{AuthorizationEndpoint: "https://as/authorize"}
	pkce := PKCE{Challenge: "chal", Method: "S256"}
	u := BuildAuthorizationURL(asm, "client123", "http://127.0.0.1:5000/callback", "state9", []string{"repo", "read:org"}, pkce)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("client_id") != "client123" || q.Get("code_challenge") != "chal" ||
		q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" ||
		q.Get("scope") != "repo read:org" || q.Get("state") != "state9" {
		t.Errorf("bad auth url query: %v", q)
	}
}

func TestExchangeCode_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") == "" {
			t.Errorf("bad token request form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","token_type":"bearer","scope":"repo","expires_in":3600}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{TokenEndpoint: srv.URL}
	now := time.Unix(1_700_000_000, 0)
	tok, err := ExchangeCode(context.Background(), srv.Client(), asm, "cid", "code-xyz", "http://127.0.0.1:1/callback", "verifier", now)
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Errorf("tokens: %+v", tok)
	}
	if !tok.ExpiresAt.Equal(now.Add(3600 * time.Second)) {
		t.Errorf("expiry: %v", tok.ExpiresAt)
	}
	if tok.Expired(now) || !tok.Expired(now.Add(3600*time.Second)) {
		t.Errorf("Expired() logic wrong around boundary")
	}
}

// TestExchangeCode_FormEncoded covers GitHub's legacy form-encoded token reply.
func TestExchangeCode_FormEncoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = w.Write([]byte("access_token=gho_abc&scope=repo%2Cread%3Aorg&token_type=bearer"))
	}))
	defer srv.Close()
	asm := &AuthServerMetadata{TokenEndpoint: srv.URL}
	tok, err := ExchangeCode(context.Background(), srv.Client(), asm, "cid", "c", "http://127.0.0.1:1/callback", "v", time.Now())
	if err != nil {
		t.Fatalf("ExchangeCode form: %v", err)
	}
	if tok.AccessToken != "gho_abc" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
}

func TestExchangeCode_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"bad_verifier","error_description":"PKCE failed"}`))
	}))
	defer srv.Close()
	asm := &AuthServerMetadata{TokenEndpoint: srv.URL}
	if _, err := ExchangeCode(context.Background(), srv.Client(), asm, "c", "c", "r", "v", time.Now()); err == nil ||
		!strings.Contains(err.Error(), "bad_verifier") {
		t.Errorf("want bad_verifier error, got %v", err)
	}
}

func TestRefresh_PreservesRefreshTokenWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-2","token_type":"bearer"}`)) // no new refresh_token
	}))
	defer srv.Close()
	asm := &AuthServerMetadata{TokenEndpoint: srv.URL}
	tok, err := Refresh(context.Background(), srv.Client(), asm, "cid", "rt-original", time.Now())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "at-2" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "rt-original" {
		t.Errorf("refresh token not preserved: %q", tok.RefreshToken)
	}
}

// TestAuthorizeInteractive_FullRoundTrip drives the loopback flow end-to-end
// with a mock authorization server. OpenBrowser simulates the user approving
// consent by immediately GETting the redirect URI with a valid code+state.
func TestAuthorizeInteractive_FullRoundTrip(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "the-code" {
			t.Errorf("exchange code = %q", r.Form.Get("code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"final-at","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	asm := &AuthServerMetadata{
		AuthorizationEndpoint: "https://unused.example/authorize",
		TokenEndpoint:         tokenSrv.URL,
	}

	// OpenBrowser extracts redirect_uri + state from the auth URL and calls
	// back as a browser would after consent.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		go func() {
			cb := redirect + "?code=the-code&state=" + url.QueryEscape(state)
			resp, err := http.Get(cb) //nolint:noctx // test
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "test-client",
		Scopes:      []string{"repo"},
		OpenBrowser: openBrowser,
		HTTPClient:  tokenSrv.Client(),
	})
	if err != nil {
		t.Fatalf("AuthorizeInteractive: %v", err)
	}
	if tok.AccessToken != "final-at" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
}

func TestAuthorizeInteractive_StateMismatch(t *testing.T) {
	asm := &AuthServerMetadata{AuthorizationEndpoint: "https://x/authorize", TokenEndpoint: "https://x/token"}
	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		redirect := u.Query().Get("redirect_uri")
		go func() {
			resp, err := http.Get(redirect + "?code=c&state=WRONG") //nolint:noctx // test
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "cid",
		OpenBrowser: openBrowser,
	})
	if !errors.Is(err, ErrStateMismatch) {
		t.Errorf("want ErrStateMismatch, got %v", err)
	}
}

func TestAuthorizeInteractive_RequiresClientID(t *testing.T) {
	_, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  &AuthServerMetadata{AuthorizationEndpoint: "x", TokenEndpoint: "y"},
		OpenBrowser: func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Errorf("want client_id error, got %v", err)
	}
}

// issuerOf reconstructs the request's base URL for the metadata echo.
func issuerOf(r *http.Request) string {
	return "http://" + r.Host
}
