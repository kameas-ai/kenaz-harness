package fleet_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestTokenRoundTripper_InjectsBearer verifies that the tokenRoundTripper
// injects the Bearer token returned by the BearerProvider into every request.
func TestTokenRoundTripper_InjectsBearer(t *testing.T) {
	t.Parallel()

	const wantToken = "test-access-token-abc123"
	var gotAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bearer := func() (string, error) { return wantToken, nil }
	client := &http.Client{
		Transport: fleet.NewTokenRoundTripper(bearer, nil),
		Timeout:   5 * time.Second,
	}

	resp, err := client.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
	want := "Bearer " + wantToken
	if gotAuthHeader != want {
		t.Errorf("Authorization header: got %q want %q", gotAuthHeader, want)
	}
}

// TestTokenRoundTripper_NoToken_SkipsRequest verifies that when the
// BearerProvider returns an empty token (pre-login / signed out), the
// tokenRoundTripper returns an error and does NOT forward the request.
func TestTokenRoundTripper_NoToken_SkipsRequest(t *testing.T) {
	t.Parallel()

	var requestReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Empty token = not signed in.
	bearer := func() (string, error) { return "", nil }
	client := &http.Client{
		Transport: fleet.NewTokenRoundTripper(bearer, nil),
		Timeout:   5 * time.Second,
	}

	_, err := client.Get(srv.URL + "/ping") //nolint:bodyclose
	if err == nil {
		t.Fatal("expected error when token is empty, got nil")
	}
	if requestReceived {
		t.Error("request was forwarded despite empty token")
	}
}

// TestTokenRoundTripper_ReadsTokenPerRequest verifies that the round-tripper
// reads the token on EVERY request so a token refresh is picked up immediately
// (FR-002).
func TestTokenRoundTripper_ReadsTokenPerRequest(t *testing.T) {
	t.Parallel()

	tokens := []string{"token-v1", "token-v2"}
	idx := 0
	bearer := func() (string, error) {
		tok := tokens[idx]
		if idx < len(tokens)-1 {
			idx++
		}
		return tok, nil
	}

	var receivedHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = append(receivedHeaders, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: fleet.NewTokenRoundTripper(bearer, nil),
		Timeout:   5 * time.Second,
	}

	for range 2 {
		resp, err := client.Get(srv.URL + "/ping")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
	}

	if len(receivedHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(receivedHeaders))
	}
	if receivedHeaders[0] != "Bearer token-v1" {
		t.Errorf("first request: got %q want %q", receivedHeaders[0], "Bearer token-v1")
	}
	if receivedHeaders[1] != "Bearer token-v2" {
		t.Errorf("second request: got %q want %q", receivedHeaders[1], "Bearer token-v2")
	}
}
