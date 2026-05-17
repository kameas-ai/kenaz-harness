package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// stubTokens installs a fake token set in the OS keychain for testing.
// Restores via t.Cleanup.
func stubTokens(t *testing.T, ts TokenSet) {
	t.Helper()
	_ = keyring.Set(keyringService, keyAccessToken, ts.AccessToken)
	_ = keyring.Set(keyringService, keyRefreshToken, ts.RefreshToken)
	_ = keyring.Set(keyringService, keyExpiresAt, "9999999999")
	t.Cleanup(func() { _ = ClearTokens() })
}

func makeTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return &Client{
		profile: EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  baseURL,
			NativeClientID: "test",
			FleetBaseURL:   baseURL,
			OIDCScopes:     DefaultOIDCScopes,
		},
		httpClient:  &http.Client{},
		httpTimeout: 5 * time.Second,
	}
}

func TestHTTPClient_401ThenSuccess(t *testing.T) {
	var callCount atomic.Int32

	// First request: Zitadel token endpoint for refresh.
	// Second request: original endpoint with new token.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This stub doubles as both the fleet API and the token endpoint.
		// Fleet API calls go to /test, token refresh goes to /oauth/v2/token.
		if r.URL.Path == "/oauth/v2/token" {
			resp := map[string]any{
				"access_token":  "at-refreshed",
				"refresh_token": "rt-new",
				"expires_in":    3600,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		n := callCount.Add(1)
		if n == 1 {
			// First attempt returns 401.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second attempt (after refresh) succeeds.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tokenSrv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-old",
		RefreshToken: "rt-old",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	c := makeTestClient(t, tokenSrv.URL)
	resp, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 API calls, got %d", callCount.Load())
	}
}

func TestHTTPClient_401ThenRefreshFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return 401 (both API and token endpoint).
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-bad",
		RefreshToken: "rt-bad",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	c := makeTestClient(t, srv.URL)
	_, err := c.Get(context.Background(), "/test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPClient_5xxRetryThenSuccess(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-valid",
		RefreshToken: "rt-valid",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	c := makeTestClient(t, srv.URL)
	// Speed up test by reducing timeout.
	c.httpTimeout = 1 * time.Second

	resp, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", callCount.Load())
	}
}

func TestHTTPClient_5xxExhausted(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-valid",
		RefreshToken: "rt-valid",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	c := makeTestClient(t, srv.URL)
	c.httpTimeout = 1 * time.Second

	_, err := c.Get(context.Background(), "/test")
	if err == nil {
		t.Fatal("expected error on 5xx exhaustion, got nil")
	}
}

func TestHTTPClient_DisabledClient(t *testing.T) {
	c := nopClientInstance()
	_, err := c.Get(context.Background(), "/test")
	if err != ErrFleetDisabled {
		t.Errorf("Get on nopClient: got %v, want ErrFleetDisabled", err)
	}
}
