package gemini

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Authenticator is the common interface for Gemini authentication modes.
type Authenticator interface {
	// Authorize decorates req with the appropriate auth header.
	Authorize(req *http.Request) error
	// Status returns a human-readable auth status string. For OAuth modes
	// it includes the next token-expiry timestamp. For API-key mode it
	// returns "ok".
	Status() string
}

// ─── API-Key auth (AI Studio) ─────────────────────────────────────────────────

// apiKeyAuth implements Authenticator using a static x-goog-api-key header.
type apiKeyAuth struct {
	key []byte
}

// newAPIKeyAuth constructs an apiKeyAuth. Copies key bytes internally.
func newAPIKeyAuth(key []byte) *apiKeyAuth {
	cp := make([]byte, len(key))
	copy(cp, key)
	return &apiKeyAuth{key: cp}
}

func (a *apiKeyAuth) Authorize(req *http.Request) error {
	req.Header.Set("x-goog-api-key", string(a.key))
	return nil
}

func (a *apiKeyAuth) Status() string { return "ok" }

// ─── Service-account auth (Vertex AI) ────────────────────────────────────────

// serviceAccountJSON is the parsed form of a Google service-account key file.
type serviceAccountJSON struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// tokenCache holds a bearer token with its expiry.
type tokenCache struct {
	accessToken string
	expiry      time.Time
}

// serviceAccountAuth implements Authenticator by minting short-lived
// OAuth 2.0 access tokens from a service-account JSON key.
type serviceAccountAuth struct {
	sa      serviceAccountJSON
	privKey *rsa.PrivateKey
	httpc   *http.Client

	mu    sync.Mutex
	cache *tokenCache
}

// newServiceAccountAuth parses the service-account JSON key bytes and
// returns a ready-to-use Authenticator.
func newServiceAccountAuth(credBytes []byte) (*serviceAccountAuth, error) {
	var sa serviceAccountJSON
	if err := json.Unmarshal(credBytes, &sa); err != nil {
		return nil, fmt.Errorf("gemini: parse service-account JSON: %w", err)
	}
	if sa.Type != "service_account" {
		return nil, fmt.Errorf("gemini: expected type=service_account, got %q", sa.Type)
	}
	privKey, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("gemini: parse private key: %w", err)
	}
	return &serviceAccountAuth{
		sa:      sa,
		privKey: privKey,
		httpc:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (a *serviceAccountAuth) Authorize(req *http.Request) error {
	tok, err := a.getToken(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (a *serviceAccountAuth) Status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache == nil {
		return "service account: no token"
	}
	return "service account: expires at " + a.cache.expiry.Format("15:04:05")
}

// getToken returns a valid access token, refreshing if necessary.
func (a *serviceAccountAuth) getToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache != nil && time.Now().Before(a.cache.expiry.Add(-30*time.Second)) {
		return a.cache.accessToken, nil
	}
	tok, expiry, err := a.mintToken(ctx)
	if err != nil {
		return "", err
	}
	a.cache = &tokenCache{accessToken: tok, expiry: expiry}
	return tok, nil
}

// mintToken exchanges a self-signed JWT for an OAuth 2.0 access token.
func (a *serviceAccountAuth) mintToken(ctx context.Context) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(time.Hour)

	claims := fmt.Sprintf(
		`{"iss":%q,"scope":"https://www.googleapis.com/auth/cloud-platform","aud":%q,"exp":%d,"iat":%d}`,
		a.sa.ClientEmail,
		a.sa.TokenURI,
		exp.Unix(),
		now.Unix(),
	)

	jwt, err := signJWT(claims, a.privKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini: sign JWT: %w", err)
	}

	body := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}
	tokenURI := a.sa.TokenURI
	if tokenURI == "" {
		tokenURI = "https://oauth2.googleapis.com/token"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI,
		strings.NewReader(body.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini: build token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("gemini: fetch token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("gemini: token endpoint %d: %s", resp.StatusCode, raw)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", time.Time{}, fmt.Errorf("gemini: parse token response: %w", err)
	}
	expiresIn := time.Duration(tok.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	return tok.AccessToken, time.Now().Add(expiresIn), nil
}

// ─── ADC auth (Vertex AI) ─────────────────────────────────────────────────────

// adcAuth implements Authenticator using application-default credentials
// via the GCE metadata server.
type adcAuth struct {
	httpc *http.Client

	mu    sync.Mutex
	cache *tokenCache
}

// newADCAuth constructs an adcAuth. Works without credential bytes —
// authentication is sourced from the environment / metadata server.
func newADCAuth() *adcAuth {
	return &adcAuth{
		httpc: &http.Client{Timeout: 5 * time.Second},
	}
}

func (a *adcAuth) Authorize(req *http.Request) error {
	tok, err := a.getToken(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (a *adcAuth) Status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache == nil {
		return "ADC: no token"
	}
	return "ADC token expires at " + a.cache.expiry.Format("15:04:05")
}

func (a *adcAuth) getToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache != nil && time.Now().Before(a.cache.expiry.Add(-30*time.Second)) {
		return a.cache.accessToken, nil
	}
	tok, expiry, err := fetchMetadataToken(ctx, a.httpc)
	if err != nil {
		return "", fmt.Errorf("gemini: ADC: %w", err)
	}
	a.cache = &tokenCache{accessToken: tok, expiry: expiry}
	return tok, nil
}

// fetchMetadataToken requests an access token from the GCE metadata server.
func fetchMetadataToken(ctx context.Context, httpc *http.Client) (string, time.Time, error) {
	const metadataURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("metadata server %d: %s", resp.StatusCode, raw)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", time.Time{}, err
	}
	return tok.AccessToken, time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// parseRSAKey parses a PEM-encoded RSA private key (PKCS#8 or PKCS#1).
func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("not an RSA key")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// signJWT builds and signs a RS256 JWT.
func signJWT(claimsJSON string, key *rsa.PrivateKey) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	sigInput := header + "." + payload
	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
