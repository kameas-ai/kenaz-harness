package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeneratePKCEVerifier(t *testing.T) {
	v, err := generatePKCEVerifier()
	if err != nil {
		t.Fatalf("generatePKCEVerifier: %v", err)
	}
	if len(v) < 43 || len(v) > 128 {
		t.Errorf("verifier length %d out of [43,128] range", len(v))
	}
	// Must be URL-safe base64 chars only.
	for _, c := range v {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", c) {
			t.Errorf("verifier contains invalid char %q", c)
		}
	}
}

func TestComputePKCEChallenge_RoundTrip(t *testing.T) {
	verifier, err := generatePKCEVerifier()
	if err != nil {
		t.Fatalf("generatePKCEVerifier: %v", err)
	}
	challenge := computePKCEChallenge(verifier)
	if challenge == "" {
		t.Error("challenge is empty")
	}
	// Challenge must differ from verifier.
	if challenge == verifier {
		t.Error("challenge equals verifier")
	}
	// Deterministic: same verifier → same challenge.
	if c2 := computePKCEChallenge(verifier); c2 != challenge {
		t.Error("challenge is not deterministic")
	}
}

func TestGenerateState(t *testing.T) {
	s1, err := generateState()
	if err != nil {
		t.Fatalf("generateState: %v", err)
	}
	s2, err := generateState()
	if err != nil {
		t.Fatalf("generateState: %v", err)
	}
	if s1 == s2 {
		t.Error("two generated states are identical")
	}
	// Must be hex.
	for _, c := range s1 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("state contains non-hex char %q", c)
		}
	}
}

func TestParseTokenResponse(t *testing.T) {
	raw := `{
		"access_token":  "access.xyz",
		"refresh_token": "refresh.xyz",
		"id_token":      "id.xyz",
		"expires_in":    3600,
		"token_type":    "Bearer"
	}`
	ts, err := parseTokenResponse([]byte(raw))
	if err != nil {
		t.Fatalf("parseTokenResponse: %v", err)
	}
	if ts.AccessToken != "access.xyz" {
		t.Errorf("AccessToken = %q", ts.AccessToken)
	}
	if ts.RefreshToken != "refresh.xyz" {
		t.Errorf("RefreshToken = %q", ts.RefreshToken)
	}
	if ts.IDToken != "id.xyz" {
		t.Errorf("IDToken = %q", ts.IDToken)
	}
	// ExpiresAt should be within 1h + 5s tolerance.
	diff := time.Until(ts.ExpiresAt)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("ExpiresAt diff = %v, want ~1h", diff)
	}
}

func TestExchangeCode_HTTPTest(t *testing.T) {
	// Stub the Zitadel token endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		resp := map[string]any{
			"access_token":  "at-test",
			"refresh_token": "rt-test",
			"expires_in":    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	profile := EnvProfile{
		Name:           EnvLocal,
		ZitadelIssuer:  srv.URL,
		NativeClientID: "test-client",
		OIDCScopes:     DefaultOIDCScopes,
	}

	ts, err := exchangeCode(context.Background(), profile, "test-code", "http://127.0.0.1:9/callback", "verifier")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if ts.AccessToken != "at-test" {
		t.Errorf("AccessToken = %q, want at-test", ts.AccessToken)
	}
}

func TestRefreshTokenSet_HTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		resp := map[string]any{
			"access_token":  "at-refreshed",
			"refresh_token": "rt-new",
			"expires_in":    7200,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	profile := EnvProfile{
		ZitadelIssuer:  srv.URL,
		NativeClientID: "test-client",
	}
	ts, err := RefreshTokenSet(context.Background(), profile, "rt-old")
	if err != nil {
		t.Fatalf("RefreshTokenSet: %v", err)
	}
	if ts.AccessToken != "at-refreshed" {
		t.Errorf("AccessToken = %q, want at-refreshed", ts.AccessToken)
	}
	if ts.RefreshToken != "rt-new" {
		t.Errorf("RefreshToken = %q, want rt-new", ts.RefreshToken)
	}
}

func TestRefreshTokenSet_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	profile := EnvProfile{
		ZitadelIssuer:  srv.URL,
		NativeClientID: "test-client",
	}
	_, err := RefreshTokenSet(context.Background(), profile, "bad-rt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
