package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// shortID returns the first 8 chars of s for log-safe identifiers.
// Empty input renders as "(empty)" so the field never disappears.
func shortID(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "…"
}

// TokenSet holds the OAuth2 token set returned by the Zitadel PKCE flow.
type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Auth-flow typed errors.
var (
	// ErrStateMismatch is returned when the OAuth2 callback state does not
	// match the state generated at flow start. Indicates a CSRF attempt or
	// browser tab confusion.
	ErrStateMismatch = errors.New("fleet: OAuth2 state mismatch")

	// ErrAuthTimeout is returned when the loopback callback listener does
	// not receive a request within the 60-second window.
	ErrAuthTimeout = errors.New("fleet: OAuth2 auth flow timed out (60s)")

	// ErrAuthFailed is returned when the authorization server returns an
	// error response instead of a code.
	ErrAuthFailed = errors.New("fleet: OAuth2 authorization failed")
)

// generatePKCEVerifier generates a cryptographically random PKCE code
// verifier (43-128 chars from the unreserved charset).
func generatePKCEVerifier() (string, error) {
	// 96 bytes → 128 base64url chars (within PKCE spec range).
	b := make([]byte, 96)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("fleet: generate pkce verifier: %w", err)
	}
	// Use URL-safe base64 without padding; trim to valid PKCE charset.
	s := base64.RawURLEncoding.EncodeToString(b)
	// PKCE verifier charset: A-Z a-z 0-9 - . _ ~
	// base64url output is already in [A-Za-z0-9-_] which is a valid subset.
	return s, nil
}

// computePKCEChallenge computes the S256 PKCE challenge from the verifier.
func computePKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// generateState generates a random hex state value for CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("fleet: generate state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// openBrowser opens url in the default system browser.
func openBrowser(rawURL string) error {
	return openBrowserPlatform(rawURL)
}

// DeviceCodeFlow performs the PKCE loopback OAuth2 authorization code flow
// against the Zitadel instance in profile. It:
//
//  1. Generates a PKCE verifier + challenge + state.
//  2. Listens on an ephemeral loopback port for the callback.
//  3. Opens the system browser to the authorization URL.
//  4. Waits up to 60s for the callback.
//  5. Exchanges the code for a TokenSet.
func DeviceCodeFlow(ctx context.Context, profile EnvProfile) (TokenSet, error) {
	logging.L().Info("fleet.auth.device_code_flow.start",
		"profile", profile.Name,
		"issuer", profile.ZitadelIssuer,
		"client_id", shortID(profile.NativeClientID),
		"audience", shortID(profile.APIAudience),
		"scopes", strings.Join(profile.OIDCScopes, " "),
	)
	verifier, err := generatePKCEVerifier()
	if err != nil {
		return TokenSet{}, err
	}
	challenge := computePKCEChallenge(verifier)

	state, err := generateState()
	if err != nil {
		return TokenSet{}, err
	}

	// Start ephemeral loopback listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return TokenSet{}, fmt.Errorf("fleet: loopback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	logging.L().Info("fleet.auth.loopback.ready",
		"port", port,
		"redirect_uri", redirectURI,
		"pkce_challenge", shortID(challenge),
		"state", shortID(state),
	)

	// Build authorization URL.
	authURL := fmt.Sprintf(
		"%s/oauth/v2/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		profile.ZitadelIssuer,
		url.QueryEscape(profile.NativeClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(strings.Join(profile.OIDCScopes, " ")),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
	)

	// Channel to receive the callback result.
	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	// Start callback server.
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			errDesc := q.Get("error_description")
			logging.L().Warn("fleet.auth.callback.idp_error", "error", errParam, "error_description", errDesc)
			_, _ = fmt.Fprintf(w, "<html><body><p>Authorization failed: %s. You may close this tab.</p></body></html>", errParam)
			resultCh <- callbackResult{err: fmt.Errorf("%w: %s: %s", ErrAuthFailed, errParam, errDesc)}
			return
		}
		if q.Get("state") != state {
			logging.L().Warn("fleet.auth.callback.state_mismatch",
				"expected", shortID(state),
				"received", shortID(q.Get("state")),
			)
			_, _ = fmt.Fprint(w, "<html><body><p>State mismatch. You may close this tab.</p></body></html>")
			resultCh <- callbackResult{err: ErrStateMismatch}
			return
		}
		code := q.Get("code")
		if code == "" {
			logging.L().Warn("fleet.auth.callback.missing_code")
			_, _ = fmt.Fprint(w, "<html><body><p>Missing code. You may close this tab.</p></body></html>")
			resultCh <- callbackResult{err: fmt.Errorf("%w: no code in callback", ErrAuthFailed)}
			return
		}
		logging.L().Info("fleet.auth.callback.received",
			"code_len", len(code),
			"code_prefix", shortID(code),
		)
		_, _ = fmt.Fprint(w, "<html><body><p>Signed in successfully. You may close this tab.</p></body></html>")
		resultCh <- callbackResult{code: code}
	})

	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() { _ = srv.Close() }()

	// Open browser.
	if err := openBrowser(authURL); err != nil {
		return TokenSet{}, fmt.Errorf("fleet: open browser: %w", err)
	}

	// Wait for callback or timeout.
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()

	var cbResult callbackResult
	select {
	case cbResult = <-resultCh:
	case <-timer.C:
		return TokenSet{}, ErrAuthTimeout
	case <-ctx.Done():
		return TokenSet{}, ctx.Err()
	}
	if cbResult.err != nil {
		return TokenSet{}, cbResult.err
	}

	// Exchange code for tokens.
	return exchangeCode(ctx, profile, cbResult.code, redirectURI, verifier)
}

// exchangeCode exchanges an authorization code for a TokenSet.
func exchangeCode(ctx context.Context, profile EnvProfile, code, redirectURI, verifier string) (TokenSet, error) {
	tokenURL := profile.ZitadelIssuer + "/oauth/v2/token"
	logging.L().Info("fleet.auth.token_exchange.start",
		"url", tokenURL,
		"redirect_uri", redirectURI,
		"client_id", shortID(profile.NativeClientID),
		"verifier_len", len(verifier),
		"code_prefix", shortID(code),
	)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"client_id":     {profile.NativeClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		logging.L().Error("fleet.auth.token_exchange.build_request_failed", "err", err.Error())
		return TokenSet{}, fmt.Errorf("fleet: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logging.L().Error("fleet.auth.token_exchange.transport_error", "err", err.Error(), "url", tokenURL)
		return TokenSet{}, fmt.Errorf("fleet: token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Truncate body so log doesn't explode on a giant HTML error page.
		bodyPreview := string(body)
		if len(bodyPreview) > 800 {
			bodyPreview = bodyPreview[:800] + "…(truncated)"
		}
		logging.L().Error("fleet.auth.token_exchange.failed",
			"status", resp.StatusCode,
			"url", tokenURL,
			"redirect_uri", redirectURI,
			"client_id", shortID(profile.NativeClientID),
			"body", bodyPreview,
		)
		return TokenSet{}, fmt.Errorf("%w: status %d: %s", ErrAuthFailed, resp.StatusCode, body)
	}
	ts, err := parseTokenResponse(body)
	if err != nil {
		logging.L().Error("fleet.auth.token_exchange.parse_failed", "err", err.Error())
		return ts, err
	}
	logging.L().Info("fleet.auth.token_exchange.success",
		"access_token_len", len(ts.AccessToken),
		"refresh_token_present", ts.RefreshToken != "",
		"id_token_present", ts.IDToken != "",
		"expires_at", ts.ExpiresAt.Format(time.RFC3339),
	)
	return ts, nil
}

// RefreshTokenSet exchanges a refresh token for a new TokenSet.
func RefreshTokenSet(ctx context.Context, profile EnvProfile, refreshToken string) (TokenSet, error) {
	tokenURL := profile.ZitadelIssuer + "/oauth/v2/token"
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {profile.NativeClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, fmt.Errorf("fleet: refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return TokenSet{}, fmt.Errorf("fleet: refresh exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return TokenSet{}, fmt.Errorf("%w: status %d during refresh: %s", ErrTokenExpired, resp.StatusCode, body)
	}
	return parseTokenResponse(body)
}

// tokenResponse is the JSON shape returned by Zitadel's token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
}

func parseTokenResponse(body []byte) (TokenSet, error) {
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenSet{}, fmt.Errorf("fleet: parse token response: %w", err)
	}
	if tr.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("%w: empty access_token in response", ErrAuthFailed)
	}
	expiresAt := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.ExpiresIn <= 0 {
		// Default 1h if the server omits expires_in.
		expiresAt = time.Now().Add(time.Hour)
	}
	return TokenSet{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		ExpiresAt:    expiresAt,
	}, nil
}
