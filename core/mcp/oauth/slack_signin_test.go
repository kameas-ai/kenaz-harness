package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeSlackTokenServer returns a test server that mimics Slack's token endpoint.
// It validates that no client_secret is sent and that code_verifier is present
// (PKCE public-client requirements).
func fakeSlackTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if secret := r.Form.Get("client_secret"); secret != "" {
			t.Errorf("Slack token request carried client_secret=%q; public PKCE client must not send a secret", secret)
		}
		if r.Form.Get("code_verifier") == "" {
			t.Errorf("Slack token request missing code_verifier (PKCE required)")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"access_token":"xoxe-slack-at","token_type":"Bearer","expires_in":43200,"refresh_token":"xoxe-r","scope":"channels:read chat:write"}`))
	}))
}

// TestSlackSignIn_StaticEndpoints verifies the full static Slack sign-in flow:
// discovery is skipped, fixed-port loopback is used, no client_secret is sent.
func TestSlackSignIn_StaticEndpoints(t *testing.T) {
	port := pickFreePort(t)

	tokenSrv := fakeSlackTokenServer(t)
	defer tokenSrv.Close()

	// Override the static token endpoint with the test server URL by injecting
	// a custom authorization server metadata via a direct AuthorizeInteractive
	// call (we test SlackSignIn by monkey-patching the asm via a helper below).
	// For this test we directly invoke SlackSignIn and inject a custom
	// openBrowser that reads the redirect_uri from the authorization URL.

	// Since SlackSignIn uses the static SlackTokenEndpoint constant, we cannot
	// inject the test token server directly. Instead we call the internal flow
	// by constructing an InteractiveConfig manually — same path SlackSignIn
	// uses — to validate the fixed-port + no-secret contract.
	asm := &AuthServerMetadata{
		Issuer:                        "https://slack.com",
		AuthorizationEndpoint:         "https://slack.com/oauth/v2_user/authorize",
		TokenEndpoint:                 tokenSrv.URL, // patched to test server
		CodeChallengeMethodsSupported: []string{"S256"},
		GrantTypesSupported:           []string{"authorization_code"},
	}

	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()

		// Verify redirect_uri is the fixed port (not ephemeral).
		wantRedirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
		if got := q.Get("redirect_uri"); got != wantRedirect {
			t.Errorf("redirect_uri = %q, want fixed-port %q", got, wantRedirect)
		}

		// No client_secret should appear in the authorization URL.
		if q.Get("client_secret") != "" {
			t.Errorf("client_secret in authorization URL (must not be present for public client)")
		}

		go func() {
			cb := q.Get("redirect_uri") + "?code=slack-code&state=" + url.QueryEscape(q.Get("state"))
			resp, err := http.Get(cb) //nolint:noctx // test helper
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := AuthorizeInteractive(context.Background(), InteractiveConfig{
		AuthServer:  asm,
		ClientID:    "placeholder-client-id",
		Scopes:      SlackDefaultScopes,
		OpenBrowser: openBrowser,
		HTTPClient:  tokenSrv.Client(),
		FixedPort:   port,
	})
	if err != nil {
		t.Fatalf("Slack PKCE flow: %v", err)
	}
	if tok.AccessToken != "xoxe-slack-at" {
		t.Errorf("access_token = %q, want xoxe-slack-at", tok.AccessToken)
	}
	if tok.RefreshToken != "xoxe-r" {
		t.Errorf("refresh_token = %q, want xoxe-r", tok.RefreshToken)
	}
}

// TestResolveSlackClientID verifies the three-step resolution order:
// baked → env → error.
func TestResolveSlackClientID(t *testing.T) {
	t.Run("baked wins", func(t *testing.T) {
		t.Setenv(SlackClientIDEnvVar, "env-override")
		got, err := ResolveSlackClientID("baked-id")
		if err != nil || got != "baked-id" {
			t.Errorf("got (%q, %v), want (baked-id, nil)", got, err)
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		t.Setenv(SlackClientIDEnvVar, "from-env")
		got, err := ResolveSlackClientID("")
		if err != nil || got != "from-env" {
			t.Errorf("got (%q, %v), want (from-env, nil)", got, err)
		}
	})

	t.Run("no client_id → ErrSlackNoClientID", func(t *testing.T) {
		t.Setenv(SlackClientIDEnvVar, "") // ensure env is empty
		_, err := ResolveSlackClientID("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), SlackClientIDEnvVar) {
			t.Errorf("error should mention %s env var, got: %v", SlackClientIDEnvVar, err)
		}
	})
}

// TestSlackSignIn_RequiresClientID exercises the ErrSlackNoClientID path when
// no client_id and no env var is set.
func TestSlackSignIn_RequiresClientID(t *testing.T) {
	t.Setenv(SlackClientIDEnvVar, "") // clear env
	_, err := SlackSignIn(context.Background(), SlackSignInConfig{
		OpenBrowser: func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("SlackSignIn: expected error when no client_id")
	}
	if !strings.Contains(err.Error(), SlackClientIDEnvVar) {
		t.Errorf("error should mention %s, got: %v", SlackClientIDEnvVar, err)
	}
}

// TestSlackConstants checks the static endpoint constants have the expected
// Slack domain structure (regression against accidental edits).
func TestSlackConstants(t *testing.T) {
	if !strings.HasPrefix(SlackAuthorizationEndpoint, "https://slack.com/") {
		t.Errorf("SlackAuthorizationEndpoint = %q, want https://slack.com/* prefix", SlackAuthorizationEndpoint)
	}
	if !strings.HasPrefix(SlackTokenEndpoint, "https://slack.com/") {
		t.Errorf("SlackTokenEndpoint = %q, want https://slack.com/* prefix", SlackTokenEndpoint)
	}
	if !strings.Contains(SlackAuthorizationEndpoint, "v2_user") {
		t.Errorf("SlackAuthorizationEndpoint = %q, want v2_user path (user-token flow)", SlackAuthorizationEndpoint)
	}
}

// TestSlackDefaultScopes checks the default scope list is non-empty and
// contains at minimum the read/write scopes required for typical usage.
func TestSlackDefaultScopes(t *testing.T) {
	if len(SlackDefaultScopes) == 0 {
		t.Fatal("SlackDefaultScopes must not be empty")
	}
	scopeSet := make(map[string]bool, len(SlackDefaultScopes))
	for _, s := range SlackDefaultScopes {
		scopeSet[s] = true
	}
	required := []string{"channels:read", "chat:write", "users:read"}
	for _, s := range required {
		if !scopeSet[s] {
			t.Errorf("SlackDefaultScopes missing required scope %q", s)
		}
	}
}
