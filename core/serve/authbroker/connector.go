package authbroker

// Connector-token client (spec 091 D8 /
// ADR-connector-consent-and-credentials §3).
//
// Whitelisted OAuth connectors authenticate with SHORT-LIVED access
// tokens minted by the host auth broker; the refresh token and DCR
// registration never leave the host keychain. The in-VM side calls:
//
//	POST http://<KENAZ_AUTH_BROKER_ADDR>/connector/<id>/token
//	Authorization: Bearer <KENAZ_AUTH_BROKER_TOKEN>
//
// and receives an RFC 6749 token response scoped to that one connector.
// The broker re-checks the launching profile's whitelist host-side on
// every call — a compromised guest cannot mint a token for a connector
// it was not granted.
//
// Tokens are held in memory only and refreshed on demand when within
// renewalThreshold (300 s) of expiry — the same posture and threshold as
// the SSO Session in this package. Token bytes are never logged.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// ErrBrokerNotConfigured is returned by ConnectorToken when the boot
// environment carried no broker address/token — e.g. an air-gapped image,
// where the carve-out is structurally absent and no token can exist.
var ErrBrokerNotConfigured = errors.New("authbroker: connector broker not configured")

// connectorIDRe guards the URL path segment. Mirrors the recipe-id
// pattern (recipes.ValidateRecipeID) without importing the recipes
// package into this leaf.
var connectorIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// connectorToken is one cached in-memory token.
type connectorToken struct {
	value     string
	expiresAt time.Time
}

// ConnectorTokens is a memory-only, per-connector access-token cache
// backed by the host auth broker. Safe for concurrent use.
type ConnectorTokens struct {
	cfg Config
	log *slog.Logger

	httpClient *http.Client
	now        func() time.Time

	mu     sync.Mutex
	tokens map[string]connectorToken
}

// ConnectorTokensOption configures NewConnectorTokens.
type ConnectorTokensOption func(*ConnectorTokens)

// WithConnectorHTTPClient replaces the default http.Client (tests).
func WithConnectorHTTPClient(c *http.Client) ConnectorTokensOption {
	return func(t *ConnectorTokens) { t.httpClient = c }
}

// WithConnectorClock injects a time source (tests).
func WithConnectorClock(now func() time.Time) ConnectorTokensOption {
	return func(t *ConnectorTokens) { t.now = now }
}

// NewConnectorTokens builds the client from the same Config the SSO
// Session reads (KENAZ_AUTH_BROKER_ADDR / KENAZ_AUTH_BROKER_TOKEN).
func NewConnectorTokens(cfg Config, log *slog.Logger, opts ...ConnectorTokensOption) *ConnectorTokens {
	if log == nil {
		log = slog.Default()
	}
	t := &ConnectorTokens{
		cfg:        cfg,
		log:        log,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		now:        time.Now,
		tokens:     make(map[string]connectorToken),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Configured reports whether the broker endpoint is reachable in
// principle (address + session token present at boot).
func (t *ConnectorTokens) Configured() bool {
	return t.cfg.BrokerAddr != "" && t.cfg.BrokerToken != ""
}

// ConnectorToken returns a valid access token for recipeID, fetching or
// renewing through the broker when the cached one is absent or within
// renewalThreshold of expiry. Callers MUST NOT log the returned value.
func (t *ConnectorTokens) ConnectorToken(ctx context.Context, recipeID string) (string, error) {
	if !t.Configured() {
		return "", ErrBrokerNotConfigured
	}
	if !connectorIDRe.MatchString(recipeID) {
		return "", fmt.Errorf("authbroker: invalid connector id")
	}

	t.mu.Lock()
	cached, ok := t.tokens[recipeID]
	t.mu.Unlock()
	if ok && t.now().Add(renewalThreshold).Before(cached.expiresAt) {
		return cached.value, nil
	}

	value, expiresIn, err := t.fetch(ctx, recipeID)
	if err != nil {
		// A still-live cached token outlasts a transient broker failure —
		// same keep-until-expiry posture as the SSO renewal loop.
		if ok && t.now().Before(cached.expiresAt) {
			t.log.Warn("authbroker: connector token renewal failed, reusing unexpired token",
				"connector_id", recipeID, "err", err.Error())
			return cached.value, nil
		}
		return "", err
	}

	t.mu.Lock()
	t.tokens[recipeID] = connectorToken{
		value:     value,
		expiresAt: t.now().Add(time.Duration(expiresIn) * time.Second),
	}
	t.mu.Unlock()
	t.log.Info("authbroker: connector token obtained",
		"connector_id", recipeID, "expires_in_s", expiresIn)
	return value, nil
}

// Invalidate drops the cached token for recipeID so the next
// ConnectorToken call hits the broker (e.g. after an upstream 401).
func (t *ConnectorTokens) Invalidate(recipeID string) {
	t.mu.Lock()
	delete(t.tokens, recipeID)
	t.mu.Unlock()
}

// fetch issues POST /connector/{id}/token. Token bytes are never logged.
func (t *ConnectorTokens) fetch(ctx context.Context, recipeID string) (string, int, error) {
	url := "http://" + t.cfg.BrokerAddr + "/connector/" + recipeID + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("authbroker: build connector token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.cfg.BrokerToken)
	req.ContentLength = 0

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("authbroker: connector token http do: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", 0, &brokerError{StatusCode: resp.StatusCode}
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", 0, fmt.Errorf("authbroker: decode connector token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", 0, fmt.Errorf("authbroker: empty access_token in connector token response")
	}
	if tok.ExpiresIn <= 0 {
		tok.ExpiresIn = 3600
	}
	return tok.AccessToken, tok.ExpiresIn, nil
}
