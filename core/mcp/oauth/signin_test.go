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

// fakeRemote stands in for a remote MCP server + its authorization server,
// wired so ProbeChallenge → metadata discovery → token exchange all resolve
// against the same httptest server.
func fakeRemote(t *testing.T) (*httptest.Server, string) {
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
	// Protected-resource metadata → authorization server = this server's /as.
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + base + `/mcp","authorization_servers":["` + base + `/as"],"scopes_supported":["repo","read:org"]}`))
	})
	// AS metadata (RFC 8414, suffix form).
	mux.HandleFunc("/as/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + base + `/as","authorization_endpoint":"` + base + `/as/authorize","token_endpoint":"` + base + `/as/token","code_challenge_methods_supported":["S256"]}`))
	})
	// Token endpoint.
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-remote","refresh_token":"rt-remote","token_type":"bearer","expires_in":3600}`))
	})
	t.Cleanup(srv.Close)
	return srv, base
}

func TestProbeChallenge(t *testing.T) {
	srv, base := fakeRemote(t)
	got, err := ProbeChallenge(context.Background(), srv.Client(), base+"/mcp/")
	if err != nil {
		t.Fatalf("ProbeChallenge: %v", err)
	}
	if got != base+"/.well-known/oauth-protected-resource/mcp/" {
		t.Errorf("resource metadata url = %q", got)
	}
}

func TestProbeChallenge_NoChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, err := ProbeChallenge(context.Background(), srv.Client(), srv.URL); !errors.Is(err, ErrNoChallenge) {
		t.Errorf("want ErrNoChallenge, got %v", err)
	}
}

func TestDiscover(t *testing.T) {
	srv, base := fakeRemote(t)
	asm, prm, err := Discover(context.Background(), srv.Client(), base+"/mcp/")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if asm.TokenEndpoint != base+"/as/token" {
		t.Errorf("token_endpoint = %q", asm.TokenEndpoint)
	}
	if len(prm.ScopesSupported) != 2 {
		t.Errorf("scopes = %v", prm.ScopesSupported)
	}
}

func TestSignIn_FullFlow(t *testing.T) {
	srv, base := fakeRemote(t)

	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		// Authorization endpoint should be the discovered one.
		if !strings.HasPrefix(authURL, base+"/as/authorize") {
			t.Errorf("auth url = %q", authURL)
		}
		go func() {
			cb := q.Get("redirect_uri") + "?code=abc&state=" + url.QueryEscape(q.Get("state"))
			resp, e := http.Get(cb) //nolint:noctx // test
			if e == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	cred, err := SignIn(context.Background(), SignInConfig{
		ServerURL:   base + "/mcp/",
		ClientID:    "cid-123",
		OpenBrowser: openBrowser,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if cred.AccessToken != "at-remote" || cred.RefreshToken != "rt-remote" {
		t.Errorf("tokens: %+v", cred)
	}
	if cred.TokenEndpoint != base+"/as/token" || cred.ClientID != "cid-123" {
		t.Errorf("refresh metadata not bundled: %+v", cred)
	}
	if cred.AuthorizationHeader() != "Bearer at-remote" {
		t.Errorf("auth header = %q", cred.AuthorizationHeader())
	}
}

func TestSignIn_RequiresClientID(t *testing.T) {
	_, err := SignIn(context.Background(), SignInConfig{ServerURL: "https://x", OpenBrowser: func(string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Errorf("want client_id error, got %v", err)
	}
}

func TestStoredCredential_MarshalRoundTrip(t *testing.T) {
	c := &StoredCredential{
		AccessToken:   "at",
		RefreshToken:  "rt",
		TokenType:     "bearer",
		ExpiresAt:     time.Unix(1_700_000_000, 0).UTC(),
		TokenEndpoint: "https://as/token",
		ClientID:      "cid",
	}
	blob, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalCredential(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" || got.ClientID != "cid" || !got.ExpiresAt.Equal(c.ExpiresAt) {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestEnsureValid_NotExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := &StoredCredential{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}
	out, refreshed, err := EnsureValid(context.Background(), nil, c, now)
	if err != nil || refreshed {
		t.Fatalf("EnsureValid: refreshed=%v err=%v", refreshed, err)
	}
	if out.AccessToken != "at" {
		t.Errorf("token changed: %q", out.AccessToken)
	}
}

func TestEnsureValid_RefreshesExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-old" {
			t.Errorf("bad refresh form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-new","refresh_token":"rt-new","token_type":"bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	c := &StoredCredential{
		AccessToken:   "at-old",
		RefreshToken:  "rt-old",
		ExpiresAt:     now.Add(-time.Minute), // expired
		TokenEndpoint: srv.URL,
		ClientID:      "cid",
	}
	out, refreshed, err := EnsureValid(context.Background(), srv.Client(), c, now)
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if !refreshed {
		t.Error("expected refreshed=true")
	}
	if out.AccessToken != "at-new" || out.RefreshToken != "rt-new" {
		t.Errorf("refreshed creds: %+v", out)
	}
}

func TestEnsureValid_ExpiredNoRefreshToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := &StoredCredential{AccessToken: "at", ExpiresAt: now.Add(-time.Minute)} // no refresh token
	if _, _, err := EnsureValid(context.Background(), nil, c, now); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("want ErrNotSignedIn, got %v", err)
	}
}

func TestEnsureValid_Nil(t *testing.T) {
	if _, _, err := EnsureValid(context.Background(), nil, nil, time.Now()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("want ErrNotSignedIn for nil, got %v", err)
	}
}
