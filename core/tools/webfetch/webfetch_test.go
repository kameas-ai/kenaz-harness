package webfetch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/tools/webfetch"
)

// setupResolver creates an ExposureIndex with the given secrets and
// wraps it in a Resolver attached to a Sanitizer.
func setupResolver(t *testing.T, secretMap map[string][]byte) (*refs.Resolver, *refs.Sanitizer, context.Context) {
	t.Helper()
	idx := secrets.NewExposureIndex()
	for locator, plain := range secretMap {
		entry := secrets.ExposedEntry{
			Locator:     locator,
			Description: "test secret",
			Scope:       secrets.ScopeSession,
			KindHint:    secrets.KindHintBearer,
		}
		idx.Add(entry, plain)
	}
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)
	return resolver, san, ctx
}

// TestWebFetch_AuthorizationHeaderSubstitution verifies that a
// @secret: reference in an Authorization header is resolved to plaintext
// before the outbound request, and that the response body has it redacted
// when the server echoes it back.
func TestWebFetch_AuthorizationHeaderSubstitution(t *testing.T) {
	const secret = "my-api-token-xyz"
	// Test server that echoes the Authorization header value in its response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, secret) {
			t.Errorf("server did not receive plaintext: Authorization=%q", auth)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("echo: " + auth))
	}))
	defer srv.Close()

	resolver, san, ctx := setupResolver(t, map[string][]byte{
		"user:mytoken": []byte(secret),
	})
	_ = resolver

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client()})
	args := map[string]any{
		"url":    srv.URL + "/api",
		"method": "GET",
		"headers": map[string]string{
			"Authorization": "Bearer @secret:user:mytoken",
		},
	}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != 200 {
		t.Errorf("status = %d", result.Status)
	}
	// The body should be redacted by the sanitizer (server echoed the token).
	if strings.Contains(result.Body, secret) {
		t.Errorf("plaintext not redacted in tool result: %q", result.Body)
	}
	if !strings.Contains(result.Body, "[redacted: user:mytoken]") {
		t.Errorf("redaction placeholder missing: %q", result.Body)
	}
	_ = san
}

// TestWebFetch_NoReference verifies that a request without @secret:
// references passes through unchanged.
func TestWebFetch_NoReference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client()})
	args := map[string]any{"url": srv.URL, "method": "GET"}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != 204 {
		t.Errorf("status = %d; want 204", result.Status)
	}
}

// TestWebFetch_DeniedResolution verifies that a denied resolution causes
// the tool to return an error result rather than sending the request.
func TestWebFetch_DeniedResolution(t *testing.T) {
	// Setup an index with no entries (so resolution will fail).
	idx := secrets.NewExposureIndex()
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)

	// Server that should NOT be called.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client()})
	args := map[string]any{
		"url":     srv.URL,
		"headers": map[string]string{"Authorization": "Bearer @secret:user:nonexistent"},
	}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("unexpected error (should return error result, not Go error): %v", err)
	}
	if called {
		t.Error("server was called despite failed resolution")
	}
	var errResult struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &errResult); err != nil {
		t.Fatalf("unmarshal error result: %v", err)
	}
	if errResult.Error == "" {
		t.Error("expected non-empty error message")
	}
}
