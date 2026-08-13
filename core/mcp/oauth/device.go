package oauth

// Device grant (RFC 8628) — GitHub-flavoured device-authorization flow.
//
// GitHub does NOT advertise its endpoints via RFC 8414 metadata discovery,
// so callers must supply the endpoints explicitly (see DeviceConfig). The
// two hard-coded GitHub URLs are:
//
//   - Device authorization: POST https://github.com/login/device/code
//   - Token polling:        POST https://github.com/login/oauth/access_token
//
// Flow:
//  1. POST device-authorization endpoint → {device_code, user_code,
//     verification_uri, expires_in, interval}.
//  2. Display user_code + verification_uri to the user.
//  3. Poll token endpoint at `interval` seconds. On "slow_down" add 5 s.
//     Stop on: access_token present, "expired_token", "access_denied", or
//     ctx cancellation / expires_in wall-clock expiry.
//  4. Return Tokens (or an error with a concrete DeviceError type so callers
//     can distinguish terminal from transient failures).
//
// SECURITY: no client_secret is sent (public client). The ghu_ access token
// MUST be stored only in the OS keychain / credstore and must never appear
// in Wails RPC return values, frontend state, or logs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHubDeviceAuthURL is the GitHub device-authorization endpoint.
// NOT discoverable via RFC 8414; must be set explicitly.
const GitHubDeviceAuthURL = "https://github.com/login/device/code"

// GitHubDeviceTokenURL is the GitHub device-flow token polling endpoint.
// NOT discoverable via RFC 8414; must be set explicitly.
const GitHubDeviceTokenURL = "https://github.com/login/oauth/access_token"

// DeviceAuthorizationResponse is the body returned by the device-authorization
// endpoint (RFC 8628 §3.2).
type DeviceAuthorizationResponse struct {
	// DeviceCode is the opaque code used in token polling requests.
	DeviceCode string `json:"device_code"`
	// UserCode is displayed to the user to enter at VerificationURI.
	UserCode string `json:"user_code"`
	// VerificationURI is where the user visits to enter UserCode.
	VerificationURI string `json:"verification_uri"`
	// ExpiresIn is how many seconds until DeviceCode expires.
	ExpiresIn int `json:"expires_in"`
	// Interval is the polling interval in seconds (default 5 if absent).
	Interval int `json:"interval"`
}

// deviceTokenResponse is the token-poll body (success or error).
type deviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// ErrDeviceExpired is returned when the device code has expired before the
// user completed authorization.
var ErrDeviceExpired = errors.New("oauth: device code expired — start sign-in again")

// ErrDeviceAccessDenied is returned when the user explicitly denied the
// authorization request.
var ErrDeviceAccessDenied = errors.New("oauth: user denied device authorization")

// DeviceConfig parameterizes one RFC 8628 device-authorization run.
type DeviceConfig struct {
	// DeviceAuthURL is the device-authorization endpoint.
	// For GitHub: GitHubDeviceAuthURL.
	DeviceAuthURL string
	// TokenURL is the token polling endpoint.
	// For GitHub: GitHubDeviceTokenURL.
	TokenURL string
	// ClientID is the registered OAuth client identifier (no client_secret
	// is sent — public client per RFC 8628 §4.1).
	ClientID string
	// Scopes requested for the grant.
	Scopes []string
	// HTTPClient is used for all requests; nil → http.DefaultClient.
	HTTPClient *http.Client
	// Now stamps token expiry; nil → time.Now.
	Now func() time.Time
	// intervalUnit is the duration one unit of the server-provided poll
	// interval maps to. Unexported test seam: 0 → time.Second (production).
	// Tests set a tiny value to avoid real multi-second sleeps while still
	// exercising the pending / slow_down back-off paths.
	intervalUnit time.Duration
}

// RequestDeviceAuthorization POSTs to the device-authorization endpoint and
// returns the response the UI must display (user_code + verification_uri).
// No client_secret is sent.
func RequestDeviceAuthorization(ctx context.Context, cfg DeviceConfig) (*DeviceAuthorizationResponse, error) {
	if cfg.ClientID == "" {
		return nil, errors.New("oauth: device flow requires a non-empty client_id")
	}
	if cfg.DeviceAuthURL == "" {
		return nil, errors.New("oauth: device flow requires a non-empty DeviceAuthURL")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	// Explicitly no client_secret (public client).

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.DeviceAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: device auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: device auth: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth: device auth endpoint returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth: read device auth response: %w", err)
	}
	var dar DeviceAuthorizationResponse
	if err := json.Unmarshal(body, &dar); err != nil {
		return nil, fmt.Errorf("oauth: decode device auth response: %w", err)
	}
	if dar.DeviceCode == "" {
		return nil, fmt.Errorf("oauth: device auth response missing device_code")
	}
	if dar.UserCode == "" {
		return nil, fmt.Errorf("oauth: device auth response missing user_code")
	}
	if dar.VerificationURI == "" {
		return nil, fmt.Errorf("oauth: device auth response missing verification_uri")
	}
	if dar.Interval <= 0 {
		dar.Interval = 5 // RFC 8628 §3.2 default
	}
	return &dar, nil
}

// PollDeviceToken polls the token endpoint until the user completes
// authorization, the device code expires, or ctx is cancelled. On success it
// returns a Tokens value with AccessToken set. No client_secret is sent.
//
// Callers MUST ensure the ghu_ access token is stored only in the credstore
// and never exposed via RPC return values, frontend state, or logs.
func PollDeviceToken(ctx context.Context, cfg DeviceConfig, dar *DeviceAuthorizationResponse) (*Tokens, error) {
	if dar == nil {
		return nil, errors.New("oauth: nil DeviceAuthorizationResponse")
	}
	if cfg.TokenURL == "" {
		return nil, errors.New("oauth: device poll requires a non-empty TokenURL")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	unit := cfg.intervalUnit
	if unit <= 0 {
		unit = time.Second
	}
	interval := time.Duration(dar.Interval) * unit
	deadline := nowFn().Add(time.Duration(dar.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("oauth: device poll cancelled: %w", ctx.Err())
		default:
		}

		// Wait the current interval before polling.
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("oauth: device poll cancelled: %w", ctx.Err())
		case <-timer.C:
		}

		if nowFn().After(deadline) {
			return nil, ErrDeviceExpired
		}

		tok, err := pollOnce(ctx, client, cfg, dar.DeviceCode, nowFn())
		if err != nil {
			if errors.Is(err, errSlowDown) {
				interval += 5 * unit
				continue
			}
			if errors.Is(err, errAuthorizationPending) {
				continue
			}
			return nil, err
		}
		return tok, nil
	}
}

var errAuthorizationPending = errors.New("oauth: authorization_pending")
var errSlowDown = errors.New("oauth: slow_down")

// pollOnce sends one token request and returns either a Tokens value, one of
// the sentinel errors (errAuthorizationPending, errSlowDown), or a terminal
// error (ErrDeviceExpired, ErrDeviceAccessDenied, or an unexpected failure).
func pollOnce(ctx context.Context, client *http.Client, cfg DeviceConfig, deviceCode string, now time.Time) (*Tokens, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	// Explicitly no client_secret (public client).

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: device token poll: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth: read device token response: %w", err)
	}

	var dtr deviceTokenResponse
	if err := json.Unmarshal(body, &dtr); err != nil {
		return nil, fmt.Errorf("oauth: decode device token response: %w", err)
	}

	switch dtr.Error {
	case "":
		// success path — fall through
	case "authorization_pending":
		return nil, errAuthorizationPending
	case "slow_down":
		return nil, errSlowDown
	case "expired_token":
		return nil, ErrDeviceExpired
	case "access_denied":
		return nil, ErrDeviceAccessDenied
	default:
		return nil, fmt.Errorf("oauth: device token error %q: %s", dtr.Error, dtr.ErrorDesc)
	}

	if dtr.AccessToken == "" {
		return nil, errors.New("oauth: device token response carried no access_token")
	}
	// GitHub device tokens (ghu_) typically have no expires_in; treat as
	// non-expiring until a 401 says otherwise (EnsureValid re-runs sign-in).
	return &Tokens{
		AccessToken: dtr.AccessToken,
		TokenType:   dtr.TokenType,
		Scope:       dtr.Scope,
	}, nil
}

// There is deliberately no one-shot AuthorizeDevice(request → onCode →
// poll) wrapper here.
//
// agentgraph-total-convergence-01PMGX01 WP17 deleted one on 2026-08-13.
// It had zero non-test callers and could not acquire any: the device
// grant has a mandatory human step in the middle — the user must read
// user_code off the screen and enter it at verification_uri — so the
// two halves have to be separate calls the UI can drive independently.
// Production splits them across two RPCs, BeginDeviceAuth (which calls
// RequestDeviceAuthorization and parks the device_code in memory) and
// PollDeviceAuth (which calls PollDeviceToken), in
// core/rpc/views/tools/device_auth.go. A blocking wrapper with an
// onCode callback is the shape you write before you have a UI, and it
// survived in this file only because its tests kept it compiling.
//
// The returned Tokens contain only the bearer token; no refresh token is
// issued by the GitHub device flow. Store the result via credstore before
// returning it to any caller — never surface the token in RPC values, frontend
// state, or logs.

// GitHubDeviceConfig returns a DeviceConfig pre-filled with the GitHub
// device-flow endpoints and the Kameas GitHub App client_id. Scopes and
// HTTPClient can be overridden by the caller.
func GitHubDeviceConfig(clientID string, scopes []string, httpClient *http.Client) DeviceConfig {
	return DeviceConfig{
		DeviceAuthURL: GitHubDeviceAuthURL,
		TokenURL:      GitHubDeviceTokenURL,
		ClientID:      clientID,
		Scopes:        scopes,
		HTTPClient:    httpClient,
	}
}
