package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterClient_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("registration: want POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("registration: want Content-Type application/json, got %q", ct)
		}

		// Decode and validate the request body.
		var meta ClientMetadata
		if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
			t.Errorf("registration: decode body: %v", err)
		}
		if meta.ClientName != "Kenaz Harness" {
			t.Errorf("client_name = %q", meta.ClientName)
		}
		if meta.TokenEndpointAuthMethod != "none" {
			t.Errorf("token_endpoint_auth_method = %q", meta.TokenEndpointAuthMethod)
		}
		if len(meta.GrantTypes) == 0 || meta.GrantTypes[0] != "authorization_code" {
			t.Errorf("grant_types = %v", meta.GrantTypes)
		}
		if len(meta.RedirectURIs) == 0 {
			t.Errorf("redirect_uris empty")
		}
		// UNIT-3 3a (spec.md §1.12 R-2 / tasks.md 3a): RFC 8252 §7.3 relaxes
		// loopback matching on the PORT only — the registered redirect URI's
		// PATH must match what AuthorizeInteractive actually sends
		// ("http://127.0.0.1:<port>/callback", loopback.go:106). Registering
		// bare "http://127.0.0.1" (no path) fails that match on every
		// conformant server; this pins the fix.
		if len(meta.RedirectURIs) == 0 || meta.RedirectURIs[0] != "http://127.0.0.1/callback" {
			t.Errorf("redirect_uris[0] = %v, want [\"http://127.0.0.1/callback\", ...] (RFC 8252 §7.3 — path must match; only the port is negotiable)", meta.RedirectURIs)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"dcr-cid-123","client_id_issued_at":1700000000}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{
		Issuer:               srv.URL,
		RegistrationEndpoint: srv.URL + "/register",
	}
	// Repoint the registration endpoint to the test server root.
	asm.RegistrationEndpoint = srv.URL

	rc, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{
		Scopes: []string{"read", "write"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if rc.ClientID != "dcr-cid-123" {
		t.Errorf("client_id = %q", rc.ClientID)
	}
	if rc.ClientIDIssuedAt != 1700000000 {
		t.Errorf("client_id_issued_at = %d", rc.ClientIDIssuedAt)
	}
	if rc.ClientSecret != "" {
		t.Errorf("unexpected client_secret in public-client response")
	}
}

func TestRegisterClient_WithSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cid-conf","client_secret":"s3cr3t","client_secret_expires_at":9999999999}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	rc, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if rc.ClientID != "cid-conf" {
		t.Errorf("client_id = %q", rc.ClientID)
	}
	if rc.ClientSecret != "s3cr3t" {
		t.Errorf("client_secret = %q", rc.ClientSecret)
	}
	if rc.ClientSecretExpiresAt != 9999999999 {
		t.Errorf("client_secret_expires_at = %d", rc.ClientSecretExpiresAt)
	}
}

func TestRegisterClient_BadRequest_RFC7591Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client_metadata","error_description":"redirect_uris is required"}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	_, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{})
	if err == nil {
		t.Fatal("want error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_client_metadata") {
		t.Errorf("want RFC 7591 error code in message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("want HTTP status in message, got %q", err.Error())
	}
}

func TestRegisterClient_InternalServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	_, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{})
	if err == nil {
		t.Fatal("want error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("want HTTP 500 in error, got %q", err.Error())
	}
}

func TestRegisterClient_NoRegistrationEndpoint(t *testing.T) {
	asm := &AuthServerMetadata{} // no registration_endpoint
	_, err := RegisterClient(context.Background(), nil, asm, RegisterClientOptions{})
	if err == nil {
		t.Fatal("want ErrNoDCREndpoint, got nil")
	}
	if err != ErrNoDCREndpoint {
		t.Errorf("want ErrNoDCREndpoint, got %v", err)
	}
}

func TestRegisterClient_ScopeIncludedInRequest(t *testing.T) {
	var gotScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var meta ClientMetadata
		_ = json.NewDecoder(r.Body).Decode(&meta)
		gotScope = meta.Scope
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cid-scoped"}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	_, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{
		Scopes: []string{"openid", "profile", "email"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	// All three scopes should appear space-separated.
	for _, s := range []string{"openid", "profile", "email"} {
		if !strings.Contains(gotScope, s) {
			t.Errorf("scope %q missing from request scope %q", s, gotScope)
		}
	}
}

func TestRegisterClient_EmptyScopeOmitted(t *testing.T) {
	var rawBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&rawBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cid-noscope"}`))
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	_, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{
		Scopes: nil, // no scopes
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if _, ok := rawBody["scope"]; ok {
		t.Errorf("scope key should be absent when no scopes provided, body = %v", rawBody)
	}
}

func TestRegisterClient_MissingClientIDInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`)) // empty — no client_id
	}))
	defer srv.Close()

	asm := &AuthServerMetadata{RegistrationEndpoint: srv.URL}
	_, err := RegisterClient(context.Background(), srv.Client(), asm, RegisterClientOptions{})
	if err == nil {
		t.Fatal("want error for missing client_id, got nil")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("want client_id in error, got %q", err.Error())
	}
}

func TestRegisteredClient_SecretExpired(t *testing.T) {
	rc := &RegisteredClient{
		ClientID:              "cid",
		ClientSecret:          "s3cr3t",
		ClientSecretExpiresAt: 1000, // epoch 1000 — well in the past
	}
	if !rc.SecretExpired(timeUnix(2000)) {
		t.Error("want SecretExpired=true for past expiry")
	}
	if rc.SecretExpired(timeUnix(500)) {
		t.Error("want SecretExpired=false for future expiry")
	}
}

func TestRegisteredClient_SecretExpired_ZeroExpiry(t *testing.T) {
	rc := &RegisteredClient{ClientID: "cid", ClientSecret: "s"}
	if rc.SecretExpired(timeUnix(9999999999)) {
		t.Error("want SecretExpired=false when no expiry set")
	}
}

func TestRegisteredClient_SecretExpired_NoSecret(t *testing.T) {
	rc := &RegisteredClient{ClientID: "cid", ClientSecretExpiresAt: 1} // expiry but no secret
	if rc.SecretExpired(timeUnix(9999999999)) {
		t.Error("want SecretExpired=false when no client_secret issued")
	}
}
