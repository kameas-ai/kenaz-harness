package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// pickFreePort returns a free TCP port on 127.0.0.1 that can be used in tests.
// It binds and immediately releases so the OS reclaims the port number for the
// test to listen on next. There is a small TOCTOU window, but it is acceptable
// for test usage.
func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("pickFreePort close: %v", err)
	}
	return port
}

// TestAuthorizeInteractive_FixedPort_RedirectURIExact verifies that when
// FixedPort is set the redirect_uri sent to the authorization endpoint is
// exactly http://127.0.0.1:<FixedPort>/callback, not an ephemeral port.
// This is the critical invariant for providers (e.g. Slack) that do exact-match
// validation against pre-registered redirect URIs.
func TestAuthorizeInteractive_FixedPort_RedirectURIExact(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-fixed","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	port := pickFreePort(t)

	asm := &AuthServerMetadata{
		AuthorizationEndpoint: "https://unused.example/authorize",
		TokenEndpoint:         tokenSrv.URL,
	}

	var capturedRedirectURI string

	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		capturedRedirectURI = q.Get("redirect_uri")

		// Simulate browser redirect back to the exact redirect_uri.
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		go func() {
			cb := redirect + "?code=fixed-code&state=" + url.QueryEscape(state)
			resp, err := http.Get(cb) //nolint:noctx // test helper
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "slack-client",
		Scopes:      []string{"channels:read"},
		OpenBrowser: openBrowser,
		HTTPClient:  tokenSrv.Client(),
		FixedPort:   port,
	})
	if err != nil {
		t.Fatalf("AuthorizeInteractive fixed-port: %v", err)
	}
	if tok.AccessToken != "at-fixed" {
		t.Errorf("access_token = %q, want at-fixed", tok.AccessToken)
	}

	wantRedirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	if capturedRedirectURI != wantRedirect {
		t.Errorf("redirect_uri = %q, want %q (fixed port must be exact)", capturedRedirectURI, wantRedirect)
	}
}

// TestAuthorizeInteractive_FixedPort_PortInUse verifies that ErrFixedPortInUse
// is returned when the configured fixed port is already bound.
func TestAuthorizeInteractive_FixedPort_PortInUse(t *testing.T) {
	// Bind a port and hold it so the fixed-port listener cannot acquire it.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind blocker: %v", err)
	}
	defer blocker.Close()
	busyPort := blocker.Addr().(*net.TCPAddr).Port

	asm := &AuthServerMetadata{
		AuthorizationEndpoint: "https://unused.example/authorize",
		TokenEndpoint:         "https://unused.example/token",
	}

	_, err = AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "cid",
		OpenBrowser: func(string) error { return nil },
		FixedPort:   busyPort,
	})
	if !isErrFixedPortInUse(err) {
		t.Errorf("want ErrFixedPortInUse, got %v", err)
	}
}

// isErrFixedPortInUse reports whether err wraps ErrFixedPortInUse.
func isErrFixedPortInUse(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrFixedPortInUse.Error())
}

// TestAuthorizeInteractive_EphemeralPort_Unchanged confirms the zero-value
// FixedPort preserves the existing ephemeral-port behavior: the redirect_uri
// must still be a loopback URL but may use any port.
func TestAuthorizeInteractive_EphemeralPort_Unchanged(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-ephemeral","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	asm := &AuthServerMetadata{
		AuthorizationEndpoint: "https://unused.example/authorize",
		TokenEndpoint:         tokenSrv.URL,
	}

	var capturedRedirectURI string
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		capturedRedirectURI = q.Get("redirect_uri")
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		go func() {
			cb := redirect + "?code=eph-code&state=" + url.QueryEscape(state)
			resp, err := http.Get(cb) //nolint:noctx // test helper
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "cid",
		OpenBrowser: openBrowser,
		HTTPClient:  tokenSrv.Client(),
		// FixedPort = 0 → ephemeral (default behavior unchanged)
	})
	if err != nil {
		t.Fatalf("AuthorizeInteractive ephemeral: %v", err)
	}
	if tok.AccessToken != "at-ephemeral" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
	// Redirect URI must be loopback.
	if !strings.HasPrefix(capturedRedirectURI, "http://127.0.0.1:") {
		t.Errorf("redirect_uri = %q, want loopback 127.0.0.1:*", capturedRedirectURI)
	}
	if !strings.HasSuffix(capturedRedirectURI, "/callback") {
		t.Errorf("redirect_uri = %q, want /callback suffix", capturedRedirectURI)
	}
}

// TestSlackLoopbackPort ensures the constant is in the expected usable port range
// (1024–65535, non-privileged) so it can be bound without root.
func TestSlackLoopbackPort(t *testing.T) {
	if SlackLoopbackPort < 1024 || SlackLoopbackPort > 65535 {
		t.Errorf("SlackLoopbackPort = %d, want 1024–65535 (non-privileged)", SlackLoopbackPort)
	}
}

// TestAuthorizeInteractive_FixedPort_NoSecret verifies that the token exchange
// for a fixed-port (Slack-style public client) does NOT send a client_secret —
// PKCE public clients must omit the secret entirely.
func TestAuthorizeInteractive_FixedPort_NoSecret(t *testing.T) {
	port := pickFreePort(t)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// Assert that no client_secret was sent (public PKCE client).
		if secret := r.Form.Get("client_secret"); secret != "" {
			t.Errorf("token request carried client_secret=%q; public PKCE clients must not send a secret", secret)
		}
		// Assert code_verifier is present (PKCE required).
		if r.Form.Get("code_verifier") == "" {
			t.Errorf("token request missing code_verifier")
		}
		// Assert redirect_uri is the fixed-port loopback.
		wantRedirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
		if got := r.Form.Get("redirect_uri"); got != wantRedirect {
			t.Errorf("token request redirect_uri = %q, want %q", got, wantRedirect)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-noscret","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	asm := &AuthServerMetadata{
		AuthorizationEndpoint: "https://slack.com/oauth/v2_user/authorize",
		TokenEndpoint:         tokenSrv.URL,
	}

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		go func() {
			cb := q.Get("redirect_uri") + "?code=slk&state=" + url.QueryEscape(q.Get("state"))
			resp, err := http.Get(cb) //nolint:noctx // test helper
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	_, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "KAMEAS_SLACK_CLIENT_PLACEHOLDER",
		OpenBrowser: openBrowser,
		HTTPClient:  tokenSrv.Client(),
		FixedPort:   port,
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("Slack-style fixed-port flow: %v", err)
	}
}
